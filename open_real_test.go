package pdfkit_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	pdfkit "github.com/Cheersupzoo/pdfkit-go"
)

func TestOpenSheetPDFWithXRefPredictor(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "testdata", "sheet_xref_predictor.pdf")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open testdata: %v", err)
	}
	defer f.Close()

	doc, err := pdfkit.Open(f)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if doc.PageCount() != 15 {
		t.Fatalf("PageCount = %d, want 15", doc.PageCount())
	}
	// Touch first and last pages to ensure objects resolved.
	if p := doc.SwitchToPage(0); p == nil {
		t.Fatal("SwitchToPage(0) returned nil")
	}
	if p := doc.SwitchToPage(14); p == nil {
		t.Fatal("SwitchToPage(14) returned nil")
	}
}
