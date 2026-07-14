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

// Write writes a complete PDF 1.7 file.
func (c *Catalog) Write(w io.Writer, root Ref, info Ref) error {
	var buf bytes.Buffer
	if _, err := buf.WriteString("%PDF-1.7\n%\xE2\xE3\xCF\xD3\n"); err != nil {
		return err
	}
	offsets := make([]int, len(c.objects))
	for id := 1; id < len(c.objects); id++ {
		offsets[id] = buf.Len()
		obj := c.objects[id]
		if _, err := fmt.Fprintf(&buf, "%d 0 obj\n", id); err != nil {
			return err
		}
		if obj == nil {
			if err := (Null{}).WritePDF(&buf); err != nil {
				return err
			}
		} else if err := obj.WritePDF(&buf); err != nil {
			return err
		}
		if _, err := buf.WriteString("\nendobj\n"); err != nil {
			return err
		}
	}
	xrefPos := buf.Len()
	n := len(c.objects)
	if _, err := fmt.Fprintf(&buf, "xref\n0 %d\n", n); err != nil {
		return err
	}
	if _, err := buf.WriteString("0000000000 65535 f \n"); err != nil {
		return err
	}
	for id := 1; id < n; id++ {
		if _, err := fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[id]); err != nil {
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
	if _, err := buf.WriteString("trailer\n"); err != nil {
		return err
	}
	if err := trailer.WritePDF(&buf); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(&buf, "\nstartxref\n%d\n%%%%EOF\n", xrefPos); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
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
