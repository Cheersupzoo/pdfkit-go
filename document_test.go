package pdfkit_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	pdfkit "github.com/Cheersupzoo/pdfkit-go"
	"github.com/Cheersupzoo/pdfkit-go/internal/svgpath"
)

func TestCreateBasicPDF(t *testing.T) {
	doc := pdfkit.New(pdfkit.WithPageSize(pdfkit.A4))
	doc.AddPage()
	doc.Font("Helvetica").FontSize(24)
	doc.Text("Hello PDF", pdfkit.TextOptions{X: 72, Y: 750})
	doc.MoveTo(72, 700).LineTo(200, 700).Stroke()
	doc.Rect(72, 600, 100, 50).Fill()

	b, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("%PDF-1.7")) {
		t.Fatalf("missing header: %q", b[:min(20, len(b))])
	}
	if !bytes.Contains(b, []byte("%%EOF")) {
		t.Fatal("missing EOF")
	}
}

func TestSVGPath(t *testing.T) {
	cmds, err := svgpath.Parse("M10 20 L30 40 C1 2 3 4 5 6 Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) < 3 {
		t.Fatalf("expected commands, got %d", len(cmds))
	}
}

func TestWrapSoftHyphen(t *testing.T) {
	doc := pdfkit.New()
	doc.AddPage()
	doc.FontSize(12)
	doc.Text("super\u00adcalifragilistic\u00adexpialidocious appears with soft hyphens", pdfkit.TextOptions{
		X: 72, Y: 700, Width: 120,
	})
	if _, err := doc.Bytes(); err != nil {
		t.Fatal(err)
	}
}

func TestEmbedFontAndImage(t *testing.T) {
	doc := pdfkit.New()
	doc.AddPage()
	if err := doc.RegisterFontFile("DejaVu", "testdata/fonts/DejaVuSans.ttf", 0); err != nil {
		t.Fatal(err)
	}
	doc.Font("DejaVu").FontSize(14)
	doc.Text("Embedded", pdfkit.TextOptions{X: 72, Y: 700})
	if _, err := doc.RegisterImageFile("img", "testdata/images/sample.png"); err != nil {
		t.Fatal(err)
	}
	doc.Image("img", 72, 500, 100, 0)
	b, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 1000 {
		t.Fatalf("pdf too small: %d", len(b))
	}
}

func TestMerge(t *testing.T) {
	a := pdfkit.New()
	a.AddPage()
	a.Text("A", pdfkit.TextOptions{X: 72, Y: 700})
	ab, err := a.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile("testdata/tmp_a.pdf", ab, 0o644)
	defer os.Remove("testdata/tmp_a.pdf")

	bdoc := pdfkit.New()
	bdoc.AddPage()
	bdoc.Text("B", pdfkit.TextOptions{X: 72, Y: 700})
	bb, err := bdoc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile("testdata/tmp_b.pdf", bb, 0o644)
	defer os.Remove("testdata/tmp_b.pdf")

	m := pdfkit.New()
	if err := m.MergeFiles("testdata/tmp_a.pdf", "testdata/tmp_b.pdf"); err != nil {
		t.Fatal(err)
	}
	if m.PageCount() != 2 {
		t.Fatalf("expected 2 pages, got %d", m.PageCount())
	}
	out, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF")) {
		t.Fatal("bad merge output")
	}
}

// TestImportSaveDedupesFonts ensures open→save reuses shared font objects across pages
// instead of cloning FontFile2 once per page.
func TestImportSaveDedupesFonts(t *testing.T) {
	doc := pdfkit.New()
	if err := doc.RegisterFontFile("DejaVu", "testdata/fonts/DejaVuSans.ttf", 0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		doc.AddPage()
		doc.Font("DejaVu").FontSize(12)
		doc.Text("page", pdfkit.TextOptions{X: 72, Y: 700})
	}
	orig, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	origFonts := bytes.Count(orig, []byte("/FontFile2"))
	if origFonts < 1 {
		t.Fatalf("expected embedded font in original, got %d FontFile2", origFonts)
	}

	opened, err := pdfkit.Open(bytes.NewReader(orig))
	if err != nil {
		t.Fatal(err)
	}
	out, err := opened.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	outFonts := bytes.Count(out, []byte("/FontFile2"))
	if outFonts != origFonts {
		t.Fatalf("font streams not deduped on import save: orig=%d out=%d", origFonts, outFonts)
	}
	if opened.PageCount() != 5 {
		t.Fatalf("expected 5 pages, got %d", opened.PageCount())
	}
}

func TestOpenSpoolsPipe(t *testing.T) {
	doc := pdfkit.New()
	doc.AddPage()
	doc.Text("pipe", pdfkit.TextOptions{X: 72, Y: 700})
	raw, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write(raw)
		_ = pw.Close()
	}()
	opened, err := pdfkit.Open(pr)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if opened.PageCount() != 1 {
		t.Fatalf("pages=%d", opened.PageCount())
	}
	out, err := opened.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF")) {
		t.Fatal("bad output")
	}
}

func TestMergeFilesMaterializesAndDropsSources(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 3; i++ {
		d := pdfkit.New()
		d.AddPage()
		d.Text("m", pdfkit.TextOptions{X: 72, Y: 700})
		b, err := d.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, string(rune('a'+i))+".pdf")
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	out := pdfkit.New()
	if err := out.MergeFiles(paths...); err != nil {
		t.Fatal(err)
	}
	if out.PageCount() != 3 {
		t.Fatalf("pages=%d", out.PageCount())
	}
	if out.HasLiveImportSource() {
		t.Fatal("MergeFiles should release source models after materialize")
	}
	b, err := out.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("%PDF")) {
		t.Fatal("bad merge output")
	}
}

