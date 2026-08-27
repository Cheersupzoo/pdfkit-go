package pdfkit_test

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/hex"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	pdfkit "github.com/Cheersupzoo/pdfkit-go"
	"github.com/Cheersupzoo/pdfkit-go/internal/pdf"
)

func TestEmbeddedFontCopyPasteASCII(t *testing.T) {
	const want = "(M-MKY-KNB-D-5-1)"
	doc := pdfkit.New(pdfkit.WithPageSize(pdfkit.A4))
	if err := doc.RegisterFontFile("DejaVu", "testdata/fonts/DejaVuSans.ttf", 0); err != nil {
		t.Fatal(err)
	}
	doc.AddPage()
	doc.Font("DejaVu").FontSize(18)
	doc.Text(want, pdfkit.TextOptions{X: 72, Y: 750})
	raw, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got := extractEmbeddedCIDText(t, raw)
	if !strings.Contains(got, want) {
		t.Fatalf("ToUnicode copy-paste mismatch:\n got %q\nwant substring %q", got, want)
	}
}

func TestEmbeddedFontCopyPasteThaiAndCode(t *testing.T) {
	const code = "(M-MKY-KNB-D-5-1)"
	const thai = "การเปรียบเทียบเศษส่วนทศนิยม"
	doc := pdfkit.New(pdfkit.WithPageSize(pdfkit.A4))
	if err := doc.RegisterFontFile("THSarabun", "testdata/fonts/THSarabun-Regular.ttf", 0); err != nil {
		t.Fatal(err)
	}
	doc.AddPage()
	doc.Font("THSarabun").FontSize(20)
	doc.Text("ชีท > "+thai, pdfkit.TextOptions{X: 72, Y: 750, Width: 450})
	doc.Text(code, pdfkit.TextOptions{X: 72, Y: 720, Width: 450})
	raw, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got := extractEmbeddedCIDText(t, raw)
	if !strings.Contains(got, code) {
		t.Fatalf("code copy-paste mismatch:\n got %q\nwant substring %q", got, code)
	}
	if strings.Contains(got, `!"#"$%#$&'#(#`) {
		t.Fatalf("got the old Identity-H garbage mapping: %q", got)
	}
	spans := actualTextSpans(t, raw)
	joined := strings.Join(spans, "")
	if !strings.Contains(joined, code) || !strings.Contains(joined, thai) {
		t.Fatalf("ActualText missing source text:\n spans=%q\n joined=%q", spans, joined)
	}
	for _, s := range spans {
		if s == "ชีท > "+thai || s == thai {
			t.Fatalf("ActualText still wraps a whole line; Chrome will highlight only the first glyph: %q", s)
		}
	}
}

func TestCIDFontWidthsMatchRenderedAdvance(t *testing.T) {
	const label = "คะแนนรวม :"
	doc := pdfkit.New(pdfkit.WithPageSize(pdfkit.A4))
	if err := doc.RegisterFontFile("THSarabun", "testdata/fonts/THSarabun-Regular.ttf", 0); err != nil {
		t.Fatal(err)
	}
	doc.AddPage()
	doc.Font("THSarabun").FontSize(24)
	doc.Text(label, pdfkit.TextOptions{X: 72, Y: 750})
	raw, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("/W")) {
		t.Fatal("CIDFont missing /W; Chrome uses DW=0 and highlights only the first glyph")
	}
	widths := cidFontWidths(t, raw)
	nonzero := 0
	for _, w := range widths {
		if w > 200 {
			nonzero++
		}
	}
	if nonzero < 5 {
		t.Fatalf("expected several real CID widths for %q, got %v", label, widths)
	}
	got := extractEmbeddedCIDText(t, raw)
	if !strings.Contains(got, label) {
		t.Fatalf("copy-paste mismatch: got %q want %q", got, label)
	}
	spans := actualTextSpans(t, raw)
	if strings.Join(spans, "") != label {
		t.Fatalf("ActualText clusters = %q want %q", strings.Join(spans, ""), label)
	}
	for _, s := range spans {
		if s == label {
			t.Fatal("ActualText still wraps the whole line; Chrome will highlight only the first glyph")
		}
	}
}

func TestEmbeddedThaiUsesAbsoluteTm(t *testing.T) {
	const sample = "วันที่ : ผู้สอนเซ็นชื่อ : O-NET (50 คะแนน +++)"
	doc := pdfkit.New(pdfkit.WithPageSize(pdfkit.A4))
	if err := doc.RegisterFontFile("THSarabun", "testdata/fonts/THSarabun-Regular.ttf", 0); err != nil {
		t.Fatal(err)
	}
	doc.AddPage()
	doc.Font("THSarabun").FontSize(16)
	doc.Text(sample, pdfkit.TextOptions{X: 72, Y: 750, Width: 450})
	raw, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !contentContains(t, raw, []byte(" Tm\n")) {
		t.Fatal("embedded Thai must be placed with Tm so /W cannot collapse GPOS runs")
	}
	got := extractEmbeddedCIDText(t, raw)
	if !strings.Contains(got, "O-NET") {
		t.Fatalf("missing ASCII in ToUnicode text: %q", got)
	}
	joined := strings.Join(actualTextSpans(t, raw), "")
	if !strings.Contains(joined, "วันที่") || !strings.Contains(joined, "ผู้สอนเซ็นชื่อ") || !strings.Contains(joined, "O-NET") {
		t.Fatalf("missing labels in ActualText: %q", joined)
	}
}

func TestThaiCombiningMarksUseLogicalActualText(t *testing.T) {
	samples := []string{
		"วันที่ผ่าน :",
		"1. พื้นฐานสำหรับ ม.ปลาย",
		"1. สมการพหุนามและเศษส่วนพหุนาม",
	}
	doc := pdfkit.New(pdfkit.WithPageSize(pdfkit.A4))
	if err := doc.RegisterFontFile("THSarabun", "testdata/fonts/THSarabun-Regular.ttf", 0); err != nil {
		t.Fatal(err)
	}
	doc.AddPage()
	doc.Font("THSarabun").FontSize(20)
	y := 750.0
	for _, s := range samples {
		doc.Text(s, pdfkit.TextOptions{X: 72, Y: y, Width: 500})
		y -= 28
	}
	raw, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !contentContains(t, raw, []byte(" Tm\n")) {
		t.Fatal("glyphs must still be placed with Tm")
	}
	spans := actualTextSpans(t, raw)
	joined := strings.Join(spans, "")
	for _, s := range samples {
		if !strings.Contains(joined, s) {
			t.Fatalf("ActualText missing %q\n joined=%q\n spans=%q", s, joined, spans)
		}
		if containsAny(spans, s) {
			t.Fatalf("ActualText wrapped the whole line %q; Chrome highlights only the first glyph", s)
		}
	}
	// Logical cluster for ที่ is ท+ี+่, not visual ท+่+ี.
	if !containsAny(spans, "ที่") {
		t.Fatalf("expected ActualText cluster %q, got %q", "ที่", spans)
	}
	if containsAny(spans, "ท่ี") {
		t.Fatalf("ActualText still uses visual mark order ท่ี: %q", spans)
	}
	if containsAny(spans, "ุ") {
		t.Fatalf("sara u leaked as its own ActualText span (Chrome wraps it onto a new line): %q", spans)
	}
	got := extractEmbeddedCIDText(t, raw)
	if strings.Contains(got, "ุ") {
		t.Fatalf("sara u still in ToUnicode; Chrome copies it onto its own line: %q", got)
	}
}

func containsAny(spans []string, want string) bool {
	for _, s := range spans {
		if s == want {
			return true
		}
	}
	return false
}

var (
	bfcharRe = regexp.MustCompile(`([0-9]+)\s+beginbfchar\s*((?:<[0-9A-Fa-f]+>\s*<(?:[0-9A-Fa-f]*)>\s*)+)endbfchar`)
	pairRe   = regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]*)>`)
	tjCIDRe  = regexp.MustCompile(`<([0-9A-Fa-f]{4,})>\s*Tj`)
)

func extractEmbeddedCIDText(t *testing.T, raw []byte) string {
	t.Helper()
	model, err := pdf.Open(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer model.Close()

	pageRefs, err := model.PageRefs()
	if err != nil {
		t.Fatal(err)
	}
	if len(pageRefs) == 0 {
		t.Fatal("no pages")
	}

	toUni := map[uint16]string{}
	var b strings.Builder
	for _, pref := range pageRefs {
		pd, err := model.GetPageDict(pref)
		if err != nil {
			t.Fatal(err)
		}
		res, _ := model.Resolve(pd["Resources"]).(pdf.Dict)
		fonts, _ := model.Resolve(res["Font"]).(pdf.Dict)
		for _, fontObj := range fonts {
			fontDict, _ := model.Resolve(fontObj).(pdf.Dict)
			tu := model.Resolve(fontDict["ToUnicode"])
			st, ok := tu.(pdf.Stream)
			if !ok {
				continue
			}
			cmap, err := decodeFlateStream(st)
			if err != nil {
				t.Fatal(err)
			}
			for cid, s := range parseBFChar(string(cmap)) {
				toUni[cid] = s
			}
		}
		contentObj := model.Resolve(pd["Contents"])
		for _, st := range contentStreams(model, contentObj) {
			decoded, err := decodeFlateStream(st)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range tjCIDRe.FindAllSubmatch(decoded, -1) {
				hexIDs := string(m[1])
				if len(hexIDs)%4 != 0 {
					t.Fatalf("odd CID hex length %q", hexIDs)
				}
				for i := 0; i < len(hexIDs); i += 4 {
					cid64, err := strconv.ParseUint(hexIDs[i:i+4], 16, 16)
					if err != nil {
						t.Fatal(err)
					}
					b.WriteString(toUni[uint16(cid64)])
				}
			}
		}
	}
	if len(toUni) == 0 {
		t.Fatal("no ToUnicode CMap with bfchar mappings found")
	}
	got := b.String()
	if got == "" {
		t.Fatal("extracted empty text from CID operators")
	}
	return got
}

func contentStreams(model *pdf.DocumentModel, obj pdf.Object) []pdf.Stream {
	switch v := obj.(type) {
	case pdf.Stream:
		return []pdf.Stream{v}
	case pdf.Array:
		var out []pdf.Stream
		for _, item := range v {
			if st, ok := model.Resolve(item).(pdf.Stream); ok {
				out = append(out, st)
			}
		}
		return out
	default:
		if st, ok := model.Resolve(obj).(pdf.Stream); ok {
			return []pdf.Stream{st}
		}
		return nil
	}
}

func decodeFlateStream(st pdf.Stream) ([]byte, error) {
	data := st.Data
	switch filter := st.Dict["Filter"].(type) {
	case pdf.Name:
		if filter != "FlateDecode" && filter != "Fl" {
			return data, nil
		}
	case pdf.Array:
		if len(filter) == 0 {
			return data, nil
		}
		name, _ := filter[0].(pdf.Name)
		if name != "FlateDecode" && name != "Fl" {
			return data, nil
		}
	default:
		return data, nil
	}
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

var actualTextRe = regexp.MustCompile(`/ActualText\s*<([0-9A-Fa-f]+)>`)

func actualTextSpans(t *testing.T, raw []byte) []string {
	t.Helper()
	model, err := pdf.Open(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer model.Close()
	pageRefs, err := model.PageRefs()
	if err != nil {
		t.Fatal(err)
	}
	var spans []string
	for _, pref := range pageRefs {
		pd, err := model.GetPageDict(pref)
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range contentStreams(model, model.Resolve(pd["Contents"])) {
			decoded, err := decodeFlateStream(st)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range actualTextRe.FindAllSubmatch(decoded, -1) {
				spans = append(spans, decodeUTF16BEHex(t, string(m[1])))
			}
		}
	}
	return spans
}

func decodeUTF16BEHex(t *testing.T, h string) string {
	t.Helper()
	if strings.HasPrefix(strings.ToUpper(h), "FEFF") {
		h = h[4:]
	}
	b, err := hex.DecodeString(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(b)%2 != 0 {
		t.Fatalf("odd UTF-16BE hex length %q", h)
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, binary.BigEndian.Uint16(b[i:i+2]))
	}
	return string(utf16.Decode(u))
}

func contentContains(t *testing.T, raw []byte, needle []byte) bool {
	t.Helper()
	model, err := pdf.Open(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer model.Close()
	pageRefs, err := model.PageRefs()
	if err != nil {
		t.Fatal(err)
	}
	for _, pref := range pageRefs {
		pd, err := model.GetPageDict(pref)
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range contentStreams(model, model.Resolve(pd["Contents"])) {
			decoded, err := decodeFlateStream(st)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(decoded, needle) {
				return true
			}
		}
	}
	return false
}

var cidWidthRe = regexp.MustCompile(`/W\s*\[\s*0\s*\[([^\]]+)\]`)

func cidFontWidths(t *testing.T, raw []byte) []float64 {
	t.Helper()
	m := cidWidthRe.FindSubmatch(raw)
	if m == nil {
		t.Fatal("could not parse CIDFont /W array")
	}
	fields := strings.Fields(string(m[1]))
	out := make([]float64, 0, len(fields))
	for _, f := range fields {
		v, err := strconv.ParseFloat(f, 64)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func parseBFChar(cmap string) map[uint16]string {
	out := map[uint16]string{}
	for _, block := range bfcharRe.FindAllStringSubmatch(cmap, -1) {
		for _, p := range pairRe.FindAllStringSubmatch(block[2], -1) {
			cid64, err := strconv.ParseUint(p[1], 16, 16)
			if err != nil {
				continue
			}
			out[uint16(cid64)] = utf16BEHexToString(p[2])
		}
	}
	return out
}

func utf16BEHexToString(h string) string {
	if h == "" {
		return ""
	}
	raw, err := hex.DecodeString(h)
	if err != nil || len(raw)%2 != 0 {
		return ""
	}
	u16 := make([]uint16, len(raw)/2)
	for i := 0; i < len(raw); i += 2 {
		u16[i/2] = binary.BigEndian.Uint16(raw[i:])
	}
	return string(utf16.Decode(u16))
}
