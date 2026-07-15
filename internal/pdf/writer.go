package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
)

// Catalog holds document-level indirect objects being built for writing.
type Catalog struct {
	objects []Object // 1-indexed via append; index 0 unused
}

func NewCatalog() *Catalog {
	return &Catalog{objects: []Object{nil}}
}

func (c *Catalog) Add(obj Object) Ref {
	c.objects = append(c.objects, obj)
	return Ref{ID: len(c.objects) - 1, Gen: 0}
}

func (c *Catalog) Set(id int, obj Object) {
	if id <= 0 || id >= len(c.objects) {
		panic("pdf: invalid object id")
	}
	c.objects[id] = obj
}

func (c *Catalog) Len() int { return len(c.objects) - 1 }

func (c *Catalog) Get(id int) Object {
	if id <= 0 || id >= len(c.objects) {
		return nil
	}
	return c.objects[id]
}

// countingWriter tracks bytes written so xref offsets can be computed
// without buffering the entire PDF in memory.
type countingWriter struct {
	w io.Writer
	n int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	nw, err := c.w.Write(p)
	c.n += nw
	return nw, err
}

// Write writes a complete PDF 1.7 file, streaming objects directly to w.
func (c *Catalog) Write(w io.Writer, root Ref, info Ref) error {
	cw := &countingWriter{w: w}
	if _, err := io.WriteString(cw, "%PDF-1.7\n%\xE2\xE3\xCF\xD3\n"); err != nil {
		return err
	}
	offsets := make([]int, len(c.objects))
	for id := 1; id < len(c.objects); id++ {
		offsets[id] = cw.n
		obj := c.objects[id]
		if _, err := fmt.Fprintf(cw, "%d 0 obj\n", id); err != nil {
			return err
		}
		if obj == nil {
			if err := (Null{}).WritePDF(cw); err != nil {
				return err
			}
		} else if err := obj.WritePDF(cw); err != nil {
			return err
		}
		if _, err := io.WriteString(cw, "\nendobj\n"); err != nil {
			return err
		}
	}
	xrefPos := cw.n
	n := len(c.objects)
	if _, err := fmt.Fprintf(cw, "xref\n0 %d\n", n); err != nil {
		return err
	}
	if _, err := io.WriteString(cw, "0000000000 65535 f \n"); err != nil {
		return err
	}
	for id := 1; id < n; id++ {
		if _, err := fmt.Fprintf(cw, "%010d 00000 n \n", offsets[id]); err != nil {
			return err
		}
	}
	trailer := Dict{
		"Size": Number(n),
		"Root": root,
	}
	if info.ID > 0 {
		trailer["Info"] = info
	}
	if _, err := io.WriteString(cw, "trailer\n"); err != nil {
		return err
	}
	if err := trailer.WritePDF(cw); err != nil {
		return err
	}
	_, err := fmt.Fprintf(cw, "\nstartxref\n%d\n%%%%EOF\n", xrefPos)
	return err
}

// Flate compresses data with zlib (PDF FlateDecode).
func Flate(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// FlateStream builds a compressed stream with /Filter /FlateDecode.
func FlateStream(dict Dict, data []byte) (Stream, error) {
	compressed, err := Flate(data)
	if err != nil {
		return Stream{}, err
	}
	d := Dict{}
	for k, v := range dict {
		d[k] = v
	}
	d["Filter"] = Name("FlateDecode")
	return Stream{Dict: d, Data: compressed}, nil
}
