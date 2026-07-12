package pdfkit

import (
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/boxesandglue/textshape/ot"
	fontlib "github.com/tdewolff/font"

	"github.com/Cheersupzoo/pdfkit-go/internal/pdf"
)

type fontResource struct {
	name        string // resource name e.g. F1
	baseName    string // Helvetica or embedded PostScript name
	standard    bool
	sfnt        *fontlib.SFNT
	raw         []byte
	usedGlyphs  map[uint16]bool
	runeGlyph   map[rune]uint16
	subset      bool
	subsetIndex map[uint16]uint16 // original glyph -> subset glyph id
	subsetOrder []uint16          // subset glyph id -> original
	shapeFont   *ot.Font
	shaper      *ot.Shaper
	upem        float64
}

var standardFonts = map[string]bool{
	"Helvetica": true, "Helvetica-Bold": true, "Helvetica-Oblique": true, "Helvetica-BoldOblique": true,
	"Times-Roman": true, "Times-Bold": true, "Times-Italic": true, "Times-BoldItalic": true,
	"Courier": true, "Courier-Bold": true, "Courier-Oblique": true, "Courier-BoldOblique": true,
	"Symbol": true, "ZapfDingbats": true,
}

// Font selects a standard font by name or previously registered embedded font family.
func (d *Document) Font(name string) *Document {
	if fr, ok := d.fonts[name]; ok {
		d.currentFont = fr
		return d
	}
	if standardFonts[name] {
		fr := &fontResource{
			name:     sanitizeFontRes(name),
			baseName: name,
			standard: true,
		}
		// store under both resource key and friendly name
		d.fonts[fr.name] = fr
		d.fonts[name] = fr
		d.currentFont = fr
		return d
	}
	d.setErr(fmt.Errorf("pdfkit: unknown font %q (register with RegisterFont)", name))
	return d
}

// FontSize sets the current font size in points.
func (d *Document) FontSize(size float64) *Document {
	d.fontSize = size
	return d
}

// RegisterFont loads a TTF/OTF/WOFF/WOFF2/TTC font from bytes.
// index selects a face in a collection (usually 0).
func (d *Document) RegisterFont(family string, data []byte, index int) error {
	sfntBytes, err := fontlib.ToSFNT(data)
	if err != nil {
		return fmt.Errorf("pdfkit: font convert: %w", err)
	}
	sfnt, err := fontlib.ParseSFNT(sfntBytes, index)
	if err != nil {
		return fmt.Errorf("pdfkit: font parse: %w", err)
	}
	resName := sanitizeFontRes(family)
	fr := &fontResource{
		name:        resName,
		baseName:    family,
		standard:    false,
		sfnt:        sfnt,
		raw:         sfntBytes,
		usedGlyphs:  map[uint16]bool{0: true},
		runeGlyph:   map[rune]uint16{},
		subset:      true,
		subsetIndex: map[uint16]uint16{0: 0},
		subsetOrder: []uint16{0},
	}
	d.fonts[resName] = fr
	d.fonts[family] = fr
	d.currentFont = fr
	return nil
}

// RegisterFontFile loads a font from disk.
func (d *Document) RegisterFontFile(family, path string, index int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return d.RegisterFont(family, data, index)
}

func sanitizeFontRes(name string) string {
	out := make([]byte, 0, len(name)+1)
	out = append(out, 'F')
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			out = append(out, c)
		}
	}
	if len(out) == 1 {
		out = append(out, 'X')
	}
	return string(out)
}

func (fr *fontResource) glyphFor(r rune) uint16 {
	if fr.standard {
		return 0
	}
	if g, ok := fr.runeGlyph[r]; ok {
		return fr.subsetID(g)
	}
	g := fr.sfnt.GlyphIndex(r)
	fr.runeGlyph[r] = g
	fr.usedGlyphs[g] = true
	return fr.subsetID(g)
}

func (fr *fontResource) subsetID(orig uint16) uint16 {
	if !fr.subset {
		return orig
	}
	if id, ok := fr.subsetIndex[orig]; ok {
		return id
	}
	id := uint16(len(fr.subsetOrder))
	fr.subsetIndex[orig] = id
	fr.subsetOrder = append(fr.subsetOrder, orig)
	return id
}

func (fr *fontResource) advance(r rune, size float64) float64 {
	if fr.standard {
		return standardAdvance(fr.baseName, r, size)
	}
	var orig uint16
	if g, ok := fr.runeGlyph[r]; ok {
		orig = g
	} else {
		orig = fr.sfnt.GlyphIndex(r)
		fr.runeGlyph[r] = orig
		fr.usedGlyphs[orig] = true
		_ = fr.subsetID(orig)
	}
	upem := float64(fr.sfnt.UnitsPerEm())
	if upem == 0 {
		upem = 1000
	}
	return float64(fr.sfnt.GlyphAdvance(orig)) * size / upem
}

func (fr *fontResource) lineHeight(size float64) float64 {
	if fr.standard {
		return size * 1.2
	}
	upem := float64(fr.sfnt.UnitsPerEm())
	if upem == 0 {
		upem = 1000
	}
	asc, desc := 800.0, 200.0
	if fr.sfnt.Hhea != nil {
		if fr.sfnt.Hhea.Ascender > 0 {
			asc = float64(fr.sfnt.Hhea.Ascender)
		}
		if fr.sfnt.Hhea.Descender < 0 {
			desc = float64(-fr.sfnt.Hhea.Descender)
		}
	}
	return (asc + desc) * size / upem * 1.1
}

func (fr *fontResource) embed(cat *pdf.Catalog) (pdf.Ref, error) {
	if fr.standard {
		dict := pdf.Dict{
			"Type":     pdf.Name("Font"),
			"Subtype":  pdf.Name("Type1"),
			"BaseFont": pdf.Name(fr.baseName),
			"Encoding": pdf.Name("WinAnsiEncoding"),
		}
		return cat.Add(dict), nil
	}
	return fr.embedTrueType(cat)
}

func (fr *fontResource) embedTrueType(cat *pdf.Catalog) (pdf.Ref, error) {
	glyphs := fr.subsetOrder
	if len(glyphs) == 0 {
		glyphs = []uint16{0}
	}

	sfnt := fr.sfnt
	glyphMap := map[uint16]uint16{} // old -> new
	if fr.subset {
		sub, err := fr.sfnt.Subset(glyphs, fontlib.SubsetOptions{Tables: fontlib.KeepMinTables})
		if err != nil {
			// Fallback: embed without subsetting if subsetter fails for this face.
			sub, err = fr.sfnt.Subset(glyphs, fontlib.SubsetOptions{Tables: fontlib.KeepAllTables})
			if err != nil {
				return pdf.Ref{}, err
			}
		}
		sfnt = sub
		for i, g := range glyphs {
			glyphMap[g] = uint16(i)
		}
	} else {
		for _, g := range glyphs {
			glyphMap[g] = g
		}
	}
	fontBytes := sfnt.Write()
	compressed, err := pdf.Flate(fontBytes)
	if err != nil {
		return pdf.Ref{}, err
	}
	fontFile := pdf.Stream{
		Dict: pdf.Dict{
			"Length1": pdf.Number(len(fontBytes)),
			"Filter":  pdf.Name("FlateDecode"),
		},
		Data: compressed,
	}
	fontFileRef := cat.Add(fontFile)

	upem := float64(fr.sfnt.UnitsPerEm())
	if upem == 0 {
		upem = 1000
	}
	asc, desc := uint16(800), uint16(200)
	if fr.sfnt.Hhea != nil {
		if fr.sfnt.Hhea.Ascender > 0 {
			asc = uint16(fr.sfnt.Hhea.Ascender)
		}
		if fr.sfnt.Hhea.Descender < 0 {
			desc = uint16(-fr.sfnt.Hhea.Descender)
		}
	}
	scale := 1000 / upem
	bbox := pdf.Array{pdf.Number(-1000), pdf.Number(-1000), pdf.Number(2000), pdf.Number(2000)}
	flags := 32 // non-symbolic
	psName := "ABCDEF+" + sanitizeFontRes(fr.baseName)

	descriptor := pdf.Dict{
		"Type":        pdf.Name("FontDescriptor"),
		"FontName":    pdf.Name(psName),
		"Flags":       pdf.Number(flags),
		"FontBBox":    bbox,
		"ItalicAngle": pdf.Number(0),
		"Ascent":      pdf.Number(float64(asc) * scale),
		"Descent":     pdf.Number(-float64(desc) * scale),
		"CapHeight":   pdf.Number(float64(asc) * scale * 0.7),
		"StemV":       pdf.Number(80),
		"FontFile2":   fontFileRef,
	}
	descRef := cat.Add(descriptor)

	// Widths are applied in the content stream via Td after OpenType shaping
	// (needed for complex scripts). Keep DW=0 so Tj does not double-advance.
	cidFont := pdf.Dict{
		"Type":           pdf.Name("Font"),
		"Subtype":        pdf.Name("CIDFontType2"),
		"BaseFont":       pdf.Name(psName),
		"CIDSystemInfo":  pdf.Dict{"Registry": pdf.String("Adobe"), "Ordering": pdf.String("Identity"), "Supplement": pdf.Number(0)},
		"FontDescriptor": descRef,
		"DW":             pdf.Number(0),
		"CIDToGIDMap":    pdf.Name("Identity"),
	}
	cidRef := cat.Add(cidFont)

	toUnicode := buildToUnicode(fr, glyphMap)
	toUniStream, err := pdf.FlateStream(pdf.Dict{}, []byte(toUnicode))
	if err != nil {
		return pdf.Ref{}, err
	}
	toUniRef := cat.Add(toUniStream)

	fontDict := pdf.Dict{
		"Type":            pdf.Name("Font"),
		"Subtype":         pdf.Name("Type0"),
		"BaseFont":        pdf.Name(psName),
		"Encoding":        pdf.Name("Identity-H"),
		"DescendantFonts": pdf.Array{cidRef},
		"ToUnicode":       toUniRef,
	}
	return cat.Add(fontDict), nil
}

func buildToUnicode(fr *fontResource, glyphMap map[uint16]uint16) string {
	// map new glyph id -> rune
	rev := map[uint16]rune{}
	for r, old := range fr.runeGlyph {
		if ng, ok := glyphMap[old]; ok {
			rev[ng] = r
		}
	}
	var b []byte
	b = append(b, []byte("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n")...)
	b = append(b, []byte("/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def\n")...)
	b = append(b, []byte("/CMapName /Adobe-Identity-UCS def\n/CMapType 2 def\n")...)
	b = append(b, []byte("1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n")...)
	type pair struct {
		g uint16
		r rune
	}
	var pairs []pair
	for g, r := range rev {
		pairs = append(pairs, pair{g, r})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].g < pairs[j].g })
	const chunk = 100
	for i := 0; i < len(pairs); i += chunk {
		end := i + chunk
		if end > len(pairs) {
			end = len(pairs)
		}
		b = append(b, []byte(fmt.Sprintf("%d beginbfchar\n", end-i))...)
		for _, p := range pairs[i:end] {
			b = append(b, []byte(fmt.Sprintf("<%04X> <%04X>\n", p.g, p.r))...)
		}
		b = append(b, []byte("endbfchar\n")...)
	}
	b = append(b, []byte("endcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n")...)
	return string(b)
}

// --- Standard font widths (WinAnsi, Helvetica-ish metrics) ---

var (
	helveticaWidthsOnce sync.Once
	helveticaWidths     [256]float64
)

func initHelvetica() {
	helveticaWidthsOnce.Do(func() {
		// Approximate Helvetica widths in 1000 units (AFM-derived subset)
		w := [256]float64{}
		for i := range w {
			w[i] = 556
		}
		set := func(c byte, width float64) { w[c] = width }
		set(' ', 278)
		set('!', 278)
		set('"', 355)
		set('#', 556)
		set('$', 556)
		set('%', 889)
		set('&', 667)
		set('\'', 191)
		set('(', 333)
		set(')', 333)
		set('*', 389)
		set('+', 584)
		set(',', 278)
		set('-', 333)
		set('.', 278)
		set('/', 278)
		set('0', 556)
		set('1', 556)
		set('2', 556)
		set('3', 556)
		set('4', 556)
		set('5', 556)
		set('6', 556)
		set('7', 556)
		set('8', 556)
		set('9', 556)
		set(':', 278)
		set(';', 278)
		set('<', 584)
		set('=', 584)
		set('>', 584)
		set('?', 556)
		set('@', 1015)
		set('A', 667)
		set('B', 667)
		set('C', 722)
		set('D', 722)
		set('E', 667)
		set('F', 611)
		set('G', 778)
		set('H', 722)
		set('I', 278)
		set('J', 500)
		set('K', 667)
		set('L', 556)
		set('M', 833)
		set('N', 722)
		set('O', 778)
		set('P', 667)
		set('Q', 778)
		set('R', 722)
		set('S', 667)
		set('T', 611)
		set('U', 722)
		set('V', 667)
		set('W', 944)
		set('X', 667)
		set('Y', 667)
		set('Z', 611)
		set('[', 278)
		set('\\', 278)
		set(']', 278)
		set('^', 469)
		set('_', 556)
		set('`', 333)
		set('a', 556)
		set('b', 556)
		set('c', 500)
		set('d', 556)
		set('e', 556)
		set('f', 278)
		set('g', 556)
		set('h', 556)
		set('i', 222)
		set('j', 222)
		set('k', 500)
		set('l', 222)
		set('m', 833)
		set('n', 556)
		set('o', 556)
		set('p', 556)
		set('q', 556)
		set('r', 333)
		set('s', 500)
		set('t', 278)
		set('u', 556)
		set('v', 500)
		set('w', 722)
		set('x', 500)
		set('y', 500)
		set('z', 500)
		helveticaWidths = w
	})
}

func standardAdvance(font string, r rune, size float64) float64 {
	initHelvetica()
	if r < 0 || r > 255 {
		if r == 0x00AD { // soft hyphen
			return helveticaWidths['-'] * size / 1000
		}
		return 556 * size / 1000
	}
	_ = font // same metrics for all standard for MVP
	return helveticaWidths[byte(r)] * size / 1000
}
