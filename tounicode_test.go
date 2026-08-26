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
	if !strings.Contains(got, thai) {
		t.Fatalf("Thai copy-paste mismatch:\n got %q\nwant substring %q", got, thai)
	}
	if strings.Contains(got, `!"#"$%#$&'#(#`) {
		t.Fatalf("got the old Identity-H garbage mapping: %q", got)
	}
	if !contentContains(t, raw, []byte("/ActualText")) {
		t.Fatal("embedded text missing ActualText marked content for copy-paste")
	}
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
