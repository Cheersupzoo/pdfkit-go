package pdf

import (
	"fmt"
	"io"
	"strconv"
)

// Object is any PDF object that can be serialized.
type Object interface {
	WritePDF(w io.Writer) error
}

type Name string

func (n Name) WritePDF(w io.Writer) error {
	_, err := fmt.Fprintf(w, "/%s", escapeName(string(n)))
	return err
}

type String string

func (s String) WritePDF(w io.Writer) error {
	_, err := w.Write([]byte("(" + escapeLiteral(string(s)) + ")"))
	return err
}

// HexString is written as <hex>.
type HexString []byte

func (h HexString) WritePDF(w io.Writer) error {
	if _, err := w.Write([]byte("<")); err != nil {
		return err
	}
	const hexdigits = "0123456789ABCDEF"
	buf := make([]byte, 0, len(h)*2)
	for _, b := range h {
		buf = append(buf, hexdigits[b>>4], hexdigits[b&0x0f])
	}
	if _, err := w.Write(buf); err != nil {
		return err
	}
	_, err := w.Write([]byte(">"))
	return err
}

type Number float64

func (n Number) WritePDF(w io.Writer) error {
	_, err := w.Write([]byte(formatFloat(float64(n))))
	return err
}

type Boolean bool

func (b Boolean) WritePDF(w io.Writer) error {
	if b {
		_, err := w.Write([]byte("true"))
		return err
	}
	_, err := w.Write([]byte("false"))
	return err
}

type Null struct{}

func (Null) WritePDF(w io.Writer) error {
	_, err := w.Write([]byte("null"))
	return err
}

type Array []Object

func (a Array) WritePDF(w io.Writer) error {
	if _, err := w.Write([]byte("[")); err != nil {
		return err
	}
	for i, o := range a {
		if i > 0 {
			if _, err := w.Write([]byte(" ")); err != nil {
				return err
			}
		}
		if o == nil {
			if err := (Null{}).WritePDF(w); err != nil {
				return err
			}
			continue
		}
		if err := o.WritePDF(w); err != nil {
			return err
		}
	}
	_, err := w.Write([]byte("]"))
	return err
}

type Dict map[Name]Object

func (d Dict) WritePDF(w io.Writer) error {
	if _, err := w.Write([]byte("<<")); err != nil {
		return err
	}
	// Stable-ish order for common keys first
	order := []Name{"Type", "Subtype", "Length", "Filter", "Width", "Height", "BitsPerComponent",
		"ColorSpace", "Font", "XObject", "ExtGState", "Pattern", "Shading", "ProcSet",
		"Resources", "MediaBox", "Contents", "Parent", "Kids", "Count", "BaseFont",
		"Encoding", "FirstChar", "LastChar", "Widths", "FontDescriptor", "ToUnicode",
		"DescendantFonts", "CIDSystemInfo", "DW", "W", "CIDToGIDMap", "FontName",
		"Flags", "FontBBox", "ItalicAngle", "Ascent", "Descent", "CapHeight", "StemV",
		"FontFile2", "FontFile3", "SMask", "DecodeParms", "Predictor", "Columns",
		"Root", "Info", "Size", "Prev", "N", "First", "I", "Range"}
	seen := map[Name]bool{}
	writeKV := func(k Name, v Object) error {
		if _, err := w.Write([]byte("\n")); err != nil {
			return err
		}
		if err := k.WritePDF(w); err != nil {
			return err
		}
		if _, err := w.Write([]byte(" ")); err != nil {
			return err
		}
		if v == nil {
			return (Null{}).WritePDF(w)
		}
		return v.WritePDF(w)
	}
	for _, k := range order {
		if v, ok := d[k]; ok {
			seen[k] = true
			if err := writeKV(k, v); err != nil {
				return err
			}
		}
	}
	for k, v := range d {
		if seen[k] {
			continue
		}
		if err := writeKV(k, v); err != nil {
			return err
		}
	}
	_, err := w.Write([]byte("\n>>"))
	return err
}

// Ref is an indirect object reference.
type Ref struct {
	ID  int
	Gen int
}

func (r Ref) WritePDF(w io.Writer) error {
	_, err := fmt.Fprintf(w, "%d %d R", r.ID, r.Gen)
	return err
}

// Stream is a PDF stream object body (dictionary + data).
type Stream struct {
	Dict Dict
	Data []byte
}

func (s Stream) WritePDF(w io.Writer) error {
	d := Dict{}
	for k, v := range s.Dict {
		d[k] = v
	}
	d["Length"] = Number(len(s.Data))
	if err := d.WritePDF(w); err != nil {
		return err
	}
	if _, err := w.Write([]byte("\nstream\n")); err != nil {
		return err
	}
	if _, err := w.Write(s.Data); err != nil {
		return err
	}
	_, err := w.Write([]byte("\nendstream"))
	return err
}

func formatFloat(f float64) string {
	// Trim trailing zeros for compact content streams.
	s := strconv.FormatFloat(f, 'f', 5, 64)
	// strip trailing zeros
	for len(s) > 1 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 1 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	if s == "-0" {
		return "0"
	}
	return s
}

func escapeLiteral(s string) string {
	out := make([]byte, 0, len(s)+8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '(', ')', '\\':
			out = append(out, '\\', c)
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			out = append(out, c)
		}
	}
	return string(out)
}

func escapeName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 33 || c > 126 || c == '#' || c == '/' || c == '(' || c == ')' || c == '<' || c == '>' || c == '[' || c == ']' || c == '{' || c == '}' || c == '%' {
			out = append(out, '#')
			out = append(out, "0123456789ABCDEF"[c>>4], "0123456789ABCDEF"[c&0x0f])
		} else {
			out = append(out, c)
		}
	}
	return string(out)
}

// Int is a convenience for integer numbers in arrays like MediaBox.
func Int(n int) Number { return Number(n) }
