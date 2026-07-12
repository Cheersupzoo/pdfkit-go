package pdfkit

import (
	"fmt"
	"math"

	"github.com/Cheersupzoo/pdfkit-go/internal/pdf"
	"github.com/Cheersupzoo/pdfkit-go/internal/svgpath"
)

// Color is an RGB color with components in 0..1.
type Color struct {
	R, G, B float64
}

func RGB(r, g, b float64) Color { return Color{r, g, b} }

func HexColor(hex string) Color {
	if len(hex) > 0 && hex[0] == '#' {
		hex = hex[1:]
	}
	var n uint32
	fmt.Sscanf(hex, "%x", &n)
	if len(hex) == 3 {
		r := float64((n>>8)&0xf) / 15
		g := float64((n>>4)&0xf) / 15
		b := float64(n&0xf) / 15
		return Color{r, g, b}
	}
	r := float64((n>>16)&0xff) / 255
	g := float64((n>>8)&0xff) / 255
	b := float64(n&0xff) / 255
	return Color{r, g, b}
}

func (d *Document) FillColor(c Color) *Document {
	d.fillRGB = [3]float64{c.R, c.G, c.B}
	p := d.Page()
	p.write("%.5f %.5f %.5f rg\n", c.R, c.G, c.B)
	return d
}

func (d *Document) StrokeColor(c Color) *Document {
	d.strokeRGB = [3]float64{c.R, c.G, c.B}
	p := d.Page()
	p.write("%.5f %.5f %.5f RG\n", c.R, c.G, c.B)
	return d
}

func (d *Document) LineWidth(w float64) *Document {
	d.lineWidth = w
	d.Page().write("%.5f w\n", w)
	return d
}

func (d *Document) LineCap(style int) *Document {
	d.Page().write("%d J\n", style)
	return d
}

func (d *Document) LineJoin(style int) *Document {
	d.Page().write("%d j\n", style)
	return d
}

func (d *Document) Dash(pattern []float64, phase float64) *Document {
	p := d.Page()
	p.write("[")
	for i, v := range pattern {
		if i > 0 {
			p.write(" ")
		}
		p.write("%.5f", v)
	}
	p.write("] %.5f d\n", phase)
	return d
}

func (d *Document) Undash() *Document {
	d.Page().write("[] 0 d\n")
	return d
}

func (d *Document) SaveGraphics() *Document {
	d.Page().write("q\n")
	return d
}

func (d *Document) RestoreGraphics() *Document {
	d.Page().write("Q\n")
	return d
}

func (d *Document) Translate(x, y float64) *Document {
	d.Page().write("1 0 0 1 %.5f %.5f cm\n", x, y)
	return d
}

func (d *Document) Scale(sx, sy float64) *Document {
	d.Page().write("%.5f 0 0 %.5f 0 0 cm\n", sx, sy)
	return d
}

func (d *Document) Rotate(angleDeg float64) *Document {
	rad := angleDeg * math.Pi / 180
	c := math.Cos(rad)
	s := math.Sin(rad)
	d.Page().write("%.5f %.5f %.5f %.5f 0 0 cm\n", c, s, -s, c)
	return d
}

func (d *Document) Transform(a, b, c, e, f, g float64) *Document {
	d.Page().write("%.5f %.5f %.5f %.5f %.5f %.5f cm\n", a, b, c, e, f, g)
	return d
}

func (d *Document) MoveTo(x, y float64) *Document {
	p := d.Page()
	p.write("%.5f %.5f m\n", x, y)
	p.pathOpen = true
	return d
}

func (d *Document) LineTo(x, y float64) *Document {
	d.Page().write("%.5f %.5f l\n", x, y)
	return d
}

func (d *Document) CurveTo(x1, y1, x2, y2, x3, y3 float64) *Document {
	d.Page().write("%.5f %.5f %.5f %.5f %.5f %.5f c\n", x1, y1, x2, y2, x3, y3)
	return d
}

func (d *Document) QuadraticCurveTo(x1, y1, x, y float64) *Document {
	// Convert using last point approximation via current point unknown —
	// PDF needs cubic; callers should prefer CurveTo. Use control as both.
	d.Page().write("%.5f %.5f %.5f %.5f %.5f %.5f c\n", x1, y1, x1, y1, x, y)
	return d
}

func (d *Document) ClosePath() *Document {
	d.Page().write("h\n")
	return d
}

func (d *Document) Rect(x, y, w, h float64) *Document {
	d.Page().write("%.5f %.5f %.5f %.5f re\n", x, y, w, h)
	d.Page().pathOpen = true
	return d
}

func (d *Document) Circle(x, y, r float64) *Document {
	k := 0.5522847498307936
	d.MoveTo(x+r, y)
	d.CurveTo(x+r, y+k*r, x+k*r, y+r, x, y+r)
	d.CurveTo(x-k*r, y+r, x-r, y+k*r, x-r, y)
	d.CurveTo(x-r, y-k*r, x-k*r, y-r, x, y-r)
	d.CurveTo(x+k*r, y-r, x+r, y-k*r, x+r, y)
	return d.ClosePath()
}

// RoundedRect appends a rectangle with corner radius r (clamped to half sides).
func (d *Document) RoundedRect(x, y, w, h, r float64) *Document {
	if r < 0 {
		r = 0
	}
	if r > w/2 {
		r = w / 2
	}
	if r > h/2 {
		r = h / 2
	}
	k := 0.5522847498307936 * r
	// start at bottom-left + r along bottom edge (PDF y-up)
	d.MoveTo(x+r, y)
	d.LineTo(x+w-r, y)
	d.CurveTo(x+w-r+k, y, x+w, y+k, x+w, y+r)
	d.LineTo(x+w, y+h-r)
	d.CurveTo(x+w, y+h-r+k, x+w-r+k, y+h, x+w-r, y+h)
	d.LineTo(x+r, y+h)
	d.CurveTo(x+r-k, y+h, x, y+h-r+k, x, y+h-r)
	d.LineTo(x, y+r)
	d.CurveTo(x, y+r-k, x+r-k, y, x+r, y)
	return d.ClosePath()
}

func (d *Document) Stroke() *Document {
	d.Page().write("S\n")
	d.Page().pathOpen = false
	return d
}

func (d *Document) Fill() *Document {
	d.Page().write("f\n")
	d.Page().pathOpen = false
	return d
}

func (d *Document) FillEvenOdd() *Document {
	d.Page().write("f*\n")
	d.Page().pathOpen = false
	return d
}

func (d *Document) FillAndStroke() *Document {
	d.Page().write("B\n")
	d.Page().pathOpen = false
	return d
}

func (d *Document) Clip() *Document {
	d.Page().write("W n\n")
	d.Page().pathOpen = false
	return d
}

// Path appends an SVG path data string.
func (d *Document) Path(dpath string) *Document {
	cmds, err := svgpath.Parse(dpath)
	if err != nil {
		d.setErr(err)
		return d
	}
	for _, c := range cmds {
		switch c.Op {
		case 'M':
			d.MoveTo(c.Args[0], c.Args[1])
		case 'L':
			d.LineTo(c.Args[0], c.Args[1])
		case 'C':
			d.CurveTo(c.Args[0], c.Args[1], c.Args[2], c.Args[3], c.Args[4], c.Args[5])
		case 'Z':
			d.ClosePath()
		}
	}
	return d
}

// LinearGradient creates an axial shading. Returns a shading name for FillShading.
func (d *Document) LinearGradient(x1, y1, x2, y2 float64, stops []GradientStop) string {
	name := fmt.Sprintf("Sh%d", len(d.shadings)+1)
	fn := buildGradientFunction(stops)
	dict := mapDict(map[string]any{
		"ShadingType": 2,
		"ColorSpace":  "DeviceRGB",
		"Coords":      []float64{x1, y1, x2, y2},
		"Function":    fn,
		"Extend":      []bool{true, true},
	})
	d.shadings[name] = &shadingResource{dict: dict}
	return name
}

// RadialGradient creates a radial shading.
func (d *Document) RadialGradient(x1, y1, r1, x2, y2, r2 float64, stops []GradientStop) string {
	name := fmt.Sprintf("Sh%d", len(d.shadings)+1)
	fn := buildGradientFunction(stops)
	dict := mapDict(map[string]any{
		"ShadingType": 3,
		"ColorSpace":  "DeviceRGB",
		"Coords":      []float64{x1, y1, r1, x2, y2, r2},
		"Function":    fn,
		"Extend":      []bool{true, true},
	})
	d.shadings[name] = &shadingResource{dict: dict}
	return name
}

// GradientStop is a color stop for gradients (Offset in 0..1).
type GradientStop struct {
	Offset float64
	Color  Color
}

func (d *Document) FillShading(name string) *Document {
	p := d.Page()
	p.usedShadings[name] = true
	p.write("/%s sh\n", name)
	return d
}

type shadingResource struct {
	dict pdf.Dict
}

type patternResource struct {
	dict pdf.Dict
}

type extGStateResource struct {
	dict pdf.Dict
}

// Opacity sets fill/stroke alpha via ExtGState.
func (d *Document) Opacity(alpha float64) *Document {
	if alpha < 0 {
		alpha = 0
	}
	if alpha > 1 {
		alpha = 1
	}
	name := fmt.Sprintf("GS%d", len(d.extGStates)+1)
	d.extGStates[name] = &extGStateResource{dict: mapDict(map[string]any{
		"Type": "ExtGState",
		"ca":   alpha,
		"CA":   alpha,
	})}
	p := d.Page()
	p.usedExtG[name] = true
	p.write("/%s gs\n", name)
	return d
}
