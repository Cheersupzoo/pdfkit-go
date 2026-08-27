package pdfkit

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf16"
)

// Align is horizontal text alignment.
type Align int

const (
	AlignLeft Align = iota
	AlignCenter
	AlignRight
	AlignJustify
)

// TextOptions configures Text drawing.
type TextOptions struct {
	Width     float64
	Align     Align
	LineGap   float64
	Paragraph float64
	X, Y      float64 // if set (Y!=0 or continued), absolute position; zero X with continued uses text cursor
	Continued bool
}

// Text draws text at the current text position or options.X/Y.
func (d *Document) Text(s string, opts ...TextOptions) *Document {
	var o TextOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	p := d.Page()
	if d.currentFont == nil {
		d.Font("Helvetica")
	}
	fr := d.currentFont
	p.usedFonts[fr.name] = true

	x, y := d.textX, d.textY
	if o.X != 0 || o.Y != 0 {
		x, y = o.X, o.Y
	}
	maxW := o.Width
	if maxW <= 0 {
		maxW = p.width - d.margins.Right - x
	}
	lineGap := o.LineGap
	if lineGap == 0 {
		lineGap = fr.lineHeight(d.fontSize) - d.fontSize
	}

	paragraphs := strings.Split(s, "\n")
	for pi, para := range paragraphs {
		if pi > 0 {
			y -= d.fontSize + lineGap
			if o.Paragraph > 0 {
				y -= o.Paragraph
			}
		}
		lines := wrapText(para, fr, d.fontSize, maxW, d.autoHyphen)
		for li, line := range lines {
			drawX := x
			lineW := measureText(line, fr, d.fontSize)
			switch o.Align {
			case AlignCenter:
				drawX = x + (maxW-lineW)/2
			case AlignRight:
				drawX = x + maxW - lineW
			case AlignJustify:
				if li < len(lines)-1 {
					d.drawJustifiedLine(p, fr, line, drawX, y, maxW)
					y -= d.fontSize + lineGap
					continue
				}
			}
			d.drawSimpleLine(p, fr, line, drawX, y)
			y -= d.fontSize + lineGap
		}
	}
	d.textX = x
	d.textY = y
	return d
}

func (d *Document) MoveDown(lines float64) *Document {
	fr := d.currentFont
	if fr == nil {
		d.Font("Helvetica")
		fr = d.currentFont
	}
	d.textY -= lines * fr.lineHeight(d.fontSize)
	return d
}

func (d *Document) TextXY(x, y float64) *Document {
	d.textX, d.textY = x, y
	return d
}

// List draws a bulleted list.
func (d *Document) List(items []string, opts ...TextOptions) *Document {
	var o TextOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	x, y := d.textX, d.textY
	if o.X != 0 || o.Y != 0 {
		x, y = o.X, o.Y
	}
	indent := d.fontSize * 1.2
	bullet := "• "
	maxW := o.Width
	if maxW <= 0 {
		maxW = d.Page().width - d.margins.Right - x - indent
	}
	for _, item := range items {
		d.TextXY(x, y)
		d.Text(bullet+item, TextOptions{Width: maxW + indent, Align: AlignLeft})
		y = d.textY
	}
	return d
}

func (d *Document) drawSimpleLine(p *Page, fr *fontResource, line string, x, y float64) {
	if fr.standard {
		p.write("BT /%s %.5f Tf %.5f %.5f Td (%s) Tj ET\n", fr.name, d.fontSize, x, y, escapePDFString(toWinAnsi(line)))
		return
	}
	glyphs := fr.shape(line, d.fontSize)
	if len(glyphs) == 0 {
		return
	}
	// One TJ so PDFium builds a single text object. Each Tj is its own
	// object, so a per-cluster ActualText span (or a Td between glyphs)
	// made Chrome copy ท่ี, wrap ุ onto a new line, and insert a newline
	// at every BDC. Td is also wrong for GPOS: it offsets from the line
	// start (Tlm), not the current point, which stacked marks in the
	// margin. TJ kerning is relative to /W and keeps v0.9 paint. One
	// ActualText for the line is the logical string; ASCII-only lines
	// skip it so Find still uses real glyph boxes.
	p.write("BT /%s %.5f Tf 1 0 0 1 %.5f %.5f Tm\n", fr.name, d.fontSize, x, y)
	useActualText := shapedLineHasCombiningCluster(glyphs)
	if useActualText {
		p.write("/Span << /ActualText <%s> >> BDC\n", "FEFF"+encodeUTF16BEHex([]rune(line)))
	}
	p.write("%s", shapedTJArray(fr, glyphs, d.fontSize))
	if useActualText {
		p.write("EMC\n")
	}
	p.write("ET\n")
}

// shapedTJArray positions OpenType glyphs with CIDFont /W plus TJ kerning
// (thousandths of a text-space unit) equal to GPOS x-offset residuals.
func shapedTJArray(fr *fontResource, glyphs []shapedGlyph, size float64) string {
	var b strings.Builder
	b.WriteByte('[')
	if len(glyphs) > 0 {
		k0 := tjKerning(-glyphs[0].XOffset, size)
		if k0 != 0 {
			fmt.Fprintf(&b, "%.3f ", k0)
		}
	}
	for i, g := range glyphs {
		fmt.Fprintf(&b, "<%04X>", g.SubsetID)
		if i+1 >= len(glyphs) {
			continue
		}
		next := glyphs[i+1]
		w := fr.glyphWidthPoints(g.OrigGID, size)
		// After this glyph, PDF is at draw+W. Next draw is pen+XAdvance+next.XOffset.
		delta := g.XAdvance + next.XOffset - g.XOffset - w
		k := tjKerning(-delta, size)
		if k != 0 {
			fmt.Fprintf(&b, " %.3f ", k)
		}
	}
	b.WriteString("] TJ\n")
	return b.String()
}

func tjKerning(deltaPoints, size float64) float64 {
	if size == 0 {
		return 0
	}
	k := deltaPoints * 1000 / size
	if k > -0.05 && k < 0.05 {
		return 0
	}
	return k
}

// shapedLineHasCombiningCluster reports OpenType clusters that occupy more
// than one glyph (Thai ที่, ำ, หุ, …). Those cannot round-trip through a
// per-GID ToUnicode CMap, so drawSimpleLine tags the line with ActualText.
func shapedLineHasCombiningCluster(glyphs []shapedGlyph) bool {
	for i := 1; i < len(glyphs); i++ {
		if glyphs[i].Cluster == glyphs[i-1].Cluster {
			return true
		}
	}
	return false
}

func (d *Document) drawJustifiedLine(p *Page, fr *fontResource, line string, x, y, maxW float64) {
	words := strings.Fields(line)
	if len(words) <= 1 {
		d.drawSimpleLine(p, fr, line, x, y)
		return
	}
	total := 0.0
	for _, w := range words {
		total += measureText(w, fr, d.fontSize)
	}
	space := (maxW - total) / float64(len(words)-1)
	cx := x
	for i, w := range words {
		d.drawSimpleLine(p, fr, w, cx, y)
		cx += measureText(w, fr, d.fontSize) + space
		_ = i
	}
}

func encodeCIDHex(s string, fr *fontResource) string {
	var b strings.Builder
	for _, g := range fr.shape(s, 12) {
		fmt.Fprintf(&b, "%04X", g.SubsetID)
	}
	return b.String()
}

func measureText(s string, fr *fontResource, size float64) float64 {
	if fr.standard {
		return measureTextStandard(s, fr, size)
	}
	return fr.measureShaped(s, size)
}

func wrapText(s string, fr *fontResource, size, maxW float64, softHyphen bool) []string {
	s = strings.ReplaceAll(s, "\r", "")
	if maxW <= 0 {
		return []string{s}
	}
	words := splitWords(s)
	var lines []string
	var cur strings.Builder
	curW := 0.0
	spaceW := fr.advance(' ', size)

	flush := func() {
		if cur.Len() == 0 {
			return
		}
		lines = append(lines, cur.String())
		cur.Reset()
		curW = 0
	}

	for _, word := range words {
		ww := measureText(strings.ReplaceAll(word, "\u00ad", ""), fr, size)
		need := ww
		if cur.Len() > 0 {
			need += spaceW
		}
		if cur.Len() > 0 && curW+need > maxW {
			// try soft hyphen break
			if softHyphen && strings.ContainsRune(word, '\u00ad') {
				parts := strings.Split(word, "\u00ad")
				built := ""
				for i, part := range parts {
					trial := built + part
					if i < len(parts)-1 {
						trialDisplay := trial + "-"
						tw := measureText(trialDisplay, fr, size)
						extra := tw
						if cur.Len() > 0 {
							extra += spaceW
						}
						if curW+extra <= maxW {
							built = trial
							continue
						}
						if built != "" {
							if cur.Len() > 0 {
								cur.WriteByte(' ')
								curW += spaceW
							}
							cur.WriteString(built + "-")
							flush()
							built = part
							continue
						}
					}
					built = trial
				}
				word = built
				ww = measureText(word, fr, size)
				need = ww
				if cur.Len() > 0 {
					need += spaceW
				}
			}
			flush()
		}
		if cur.Len() > 0 {
			cur.WriteByte(' ')
			curW += spaceW
		}
		cur.WriteString(strings.ReplaceAll(word, "\u00ad", ""))
		curW += measureText(strings.ReplaceAll(word, "\u00ad", ""), fr, size)
	}
	flush()
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func splitWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r)
	})
}

func escapePDFString(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '(', ')', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func toWinAnsi(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == 0x00AD {
			continue
		}
		if r < 128 {
			b.WriteByte(byte(r))
			continue
		}
		switch r {
		case 0x2022: // bullet
			b.WriteByte(149) // WinAnsi bullet
		case 0x2013: // en dash
			b.WriteByte(150)
		case 0x2014: // em dash
			b.WriteByte(151)
		case 0x2018, 0x2019: // quotes
			b.WriteByte('\'')
		case 0x201C, 0x201D:
			b.WriteByte('"')
		case 0x00A0:
			b.WriteByte(' ')
		default:
			if r < 256 {
				b.WriteByte(byte(r))
			} else {
				b.WriteByte('?')
			}
		}
	}
	_ = utf16.Encode
	return b.String()
}
