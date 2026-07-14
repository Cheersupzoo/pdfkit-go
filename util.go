package pdfkit

import "github.com/Cheersupzoo/pdfkit-go/internal/pdf"

func mapDict(m map[string]any) pdf.Dict {
	d := pdf.Dict{}
	for k, v := range m {
		d[pdf.Name(k)] = toPDF(v)
	}
	return d
}

func toPDF(v any) pdf.Object {
	switch t := v.(type) {
	case pdf.Object:
		return t
	case string:
		// treat as Name if looks like PDF name tokens we use, else String —
		// callers pass ColorSpace etc. as names
		return pdf.Name(t)
	case float64:
		return pdf.Number(t)
	case int:
		return pdf.Number(t)
	case bool:
		return pdf.Boolean(t)
	case Color:
		return pdf.Array{pdf.Number(t.R), pdf.Number(t.G), pdf.Number(t.B)}
	case []float64:
		a := make(pdf.Array, len(t))
		for i, x := range t {
			a[i] = pdf.Number(x)
		}
		return a
	case []bool:
		a := make(pdf.Array, len(t))
		for i, x := range t {
			a[i] = pdf.Boolean(x)
		}
		return a
	case []any:
		a := make(pdf.Array, len(t))
		for i, x := range t {
			a[i] = toPDF(x)
		}
		return a
	case pdf.Dict:
		return t
	case map[string]any:
		return mapDict(t)
	default:
		return pdf.Null{}
	}
}

func buildGradientFunction(stops []GradientStop) pdf.Dict {
	if len(stops) == 0 {
		stops = []GradientStop{{0, RGB(0, 0, 0)}, {1, RGB(1, 1, 1)}}
	}
	if len(stops) == 1 {
		stops = append(stops, GradientStop{1, stops[0].Color})
	}
	// stitch exponential/linear type 2 functions between stops
	if len(stops) == 2 {
		c0 := stops[0].Color
		c1 := stops[1].Color
		return pdf.Dict{
			"FunctionType": pdf.Number(2),
			"Domain":       pdf.Array{pdf.Number(0), pdf.Number(1)},
			"C0":           pdf.Array{pdf.Number(c0.R), pdf.Number(c0.G), pdf.Number(c0.B)},
			"C1":           pdf.Array{pdf.Number(c1.R), pdf.Number(c1.G), pdf.Number(c1.B)},
			"N":            pdf.Number(1),
		}
	}
	fns := make(pdf.Array, 0, len(stops)-1)
	bounds := make(pdf.Array, 0, len(stops)-2)
	encode := make(pdf.Array, 0, (len(stops)-1)*2)
	for i := 0; i < len(stops)-1; i++ {
		c0 := stops[i].Color
		c1 := stops[i+1].Color
		fns = append(fns, pdf.Dict{
			"FunctionType": pdf.Number(2),
			"Domain":       pdf.Array{pdf.Number(0), pdf.Number(1)},
			"C0":           pdf.Array{pdf.Number(c0.R), pdf.Number(c0.G), pdf.Number(c0.B)},
			"C1":           pdf.Array{pdf.Number(c1.R), pdf.Number(c1.G), pdf.Number(c1.B)},
			"N":            pdf.Number(1),
		})
		encode = append(encode, pdf.Number(0), pdf.Number(1))
		if i < len(stops)-2 {
			bounds = append(bounds, pdf.Number(stops[i+1].Offset))
		}
	}
	return pdf.Dict{
		"FunctionType": pdf.Number(3),
		"Domain":       pdf.Array{pdf.Number(0), pdf.Number(1)},
		"Functions":    fns,
		"Bounds":       bounds,
		"Encode":       encode,
	}
}
