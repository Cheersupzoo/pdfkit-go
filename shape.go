package pdfkit

import (
	"unicode"

	"github.com/boxesandglue/textshape/ot"
)

// shapedGlyph is one positioned glyph after OpenType shaping.
type shapedGlyph struct {
	OrigGID  uint16
	SubsetID uint16
	XAdvance float64 // in points at current size
	XOffset  float64
	YOffset  float64
	Cluster  int
}

func (fr *fontResource) initShaper() error {
	if fr.standard || fr.shaper != nil {
		return nil
	}
	face, err := ot.ParseFont(fr.raw, 0)
	if err != nil {
		return err
	}
	sh, err := ot.NewShaper(face)
	if err != nil {
		return err
	}
	fr.shapeFont = face
	fr.shaper = sh
	fr.upem = float64(fr.sfnt.UnitsPerEm())
	if fr.upem == 0 {
		fr.upem = 1000
	}
	return nil
}

// shape runs OpenType GSUB/GPOS (critical for Thai mark variants, Arabic, etc.).
func (fr *fontResource) shape(s string, size float64) []shapedGlyph {
	if fr.standard || s == "" {
		return nil
	}
	if err := fr.initShaper(); err != nil || fr.shaper == nil {
		return fr.shapeFallback(s, size)
	}
	buf := ot.NewBuffer()
	buf.AddString(s)
	buf.GuessSegmentProperties()
	fr.shaper.Shape(buf, nil)

	scale := size / fr.upem
	out := make([]shapedGlyph, 0, len(buf.Info))
	for i, info := range buf.Info {
		orig := uint16(info.GlyphID)
		fr.usedGlyphs[orig] = true
		sid := fr.subsetID(orig)
		pos := buf.Pos[i]
		out = append(out, shapedGlyph{
			OrigGID:  orig,
			SubsetID: sid,
			XAdvance: float64(pos.XAdvance) * scale,
			XOffset:  float64(pos.XOffset) * scale,
			YOffset:  float64(pos.YOffset) * scale,
			Cluster:  int(info.Cluster),
		})
	}
	return out
}

func (fr *fontResource) shapeFallback(s string, size float64) []shapedGlyph {
	out := make([]shapedGlyph, 0, len(s))
	for _, r := range s {
		if r == 0x00AD {
			continue
		}
		orig := fr.sfnt.GlyphIndex(r)
		fr.runeGlyph[r] = orig
		fr.usedGlyphs[orig] = true
		sid := fr.subsetID(orig)
		adv := float64(fr.sfnt.GlyphAdvance(orig)) * size / float64(fr.sfnt.UnitsPerEm())
		out = append(out, shapedGlyph{OrigGID: orig, SubsetID: sid, XAdvance: adv})
	}
	return out
}

func (fr *fontResource) measureShaped(s string, size float64) float64 {
	if fr.standard {
		return measureTextStandard(s, fr, size)
	}
	w := 0.0
	for _, g := range fr.shape(s, size) {
		w += g.XAdvance
	}
	return w
}

func measureTextStandard(s string, fr *fontResource, size float64) float64 {
	w := 0.0
	for _, r := range s {
		if r == 0x00AD {
			continue
		}
		w += fr.advance(r, size)
	}
	return w
}

func isComplexScriptRune(r rune) bool {
	switch {
	case unicode.In(r, unicode.Thai), unicode.In(r, unicode.Lao), unicode.In(r, unicode.Khmer),
		unicode.In(r, unicode.Myanmar), unicode.In(r, unicode.Tibetan),
		unicode.In(r, unicode.Arabic), unicode.In(r, unicode.Hebrew),
		unicode.In(r, unicode.Devanagari), unicode.In(r, unicode.Bengali),
		unicode.In(r, unicode.Tamil), unicode.In(r, unicode.Telugu),
		unicode.In(r, unicode.Kannada), unicode.In(r, unicode.Malayalam),
		unicode.In(r, unicode.Gujarati), unicode.In(r, unicode.Gurmukhi),
		unicode.In(r, unicode.Sinhala), unicode.In(r, unicode.Hangul):
		return true
	}
	return false
}
