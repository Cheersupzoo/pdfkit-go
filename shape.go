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
	fr.recordShapedUnicode(s, buf.Info)
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

// recordShapedUnicode maps original glyph IDs to source characters so the
// embedded ToUnicode CMap can round-trip copy/paste. Identity-H CIDs are
// subset glyph IDs, not Unicode; without this map Chrome copies garbage.
func (fr *fontResource) recordShapedUnicode(s string, info []ot.GlyphInfo) {
	if len(info) == 0 || s == "" {
		return
	}
	runes := []rune(s)
	for i, g := range info {
		orig := uint16(g.GlyphID)
		if orig == 0 {
			continue
		}
		start, end := clusterRuneRange(runes, info, i)
		prevSame := i > 0 && info[i-1].Cluster == g.Cluster
		nextSame := i+1 < len(info) && info[i+1].Cluster == g.Cluster
		idxInCluster := 0
		if prevSame {
			j := i
			for j > 0 && info[j-1].Cluster == g.Cluster {
				j--
			}
			idxInCluster = i - j
		}

		var mapped []rune
		clusterRunes := runes[start:end]
		switch {
		case idxInCluster == 0 && !nextSame && len(clusterRunes) > 0:
			mapped = clusterRunes
		case idxInCluster < len(clusterRunes):
			mapped = []rune{clusterRunes[idxInCluster]}
		case g.Codepoint != 0 && g.Codepoint <= 0x10FFFF:
			mapped = []rune{rune(g.Codepoint)}
		}
		fr.recordGlyphUnicode(orig, mapped)
	}
}

func clusterRuneRange(runes []rune, info []ot.GlyphInfo, i int) (start, end int) {
	start = info[i].Cluster
	if start < 0 {
		start = 0
	}
	if start > len(runes) {
		start = len(runes)
	}
	end = len(runes)
	for j := i + 1; j < len(info); j++ {
		if info[j].Cluster > info[i].Cluster {
			end = info[j].Cluster
			break
		}
	}
	if end > len(runes) {
		end = len(runes)
	}
	if end < start {
		end = start
	}
	return start, end
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
		fr.recordGlyphUnicode(orig, []rune{r})
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
