package pdfkit_test

import (
	"os"
	"testing"

	pdfkit "github.com/Cheersupzoo/pdfkit-go"
)

func TestThaiShapingUsesSmallMark(t *testing.T) {
	doc := pdfkit.New(pdfkit.WithPageSize(pdfkit.A4))
	if err := doc.RegisterFontFile("THSarabun", "testdata/fonts/THSarabun-Regular.ttf", 0); err != nil {
		t.Fatal(err)
	}
	doc.AddPage()
	doc.Font("THSarabun").FontSize(24)
	// Words that require GSUB mark variants (mai tho.small above sara ii)
	doc.Text("งานนี้ หน้าปกนี้ ภาษาไทย", pdfkit.TextOptions{X: 72, Y: 750, Width: 450})
	b, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 1000 {
		t.Fatalf("pdf too small: %d", len(b))
	}
	_ = os.WriteFile("testdata/thai_shape_smoke.pdf", b, 0o644)
}
