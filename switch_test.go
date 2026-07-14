package pdfkit

import (
	"bytes"
	"fmt"
	"testing"
)

func TestSwitchToPageTargetsDraws(t *testing.T) {
	src := New(WithPageSize(A4))
	for i := 1; i <= 3; i++ {
		src.AddPage()
		src.Font("Helvetica").FontSize(14)
		src.Text(fmt.Sprintf("BODY-%d", i), TextOptions{X: 72, Y: 750})
	}
	raw, err := src.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	doc, err := LoadPDFBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	doc.SwitchToPage(0)
	doc.Font("Helvetica").FontSize(12)
	doc.Text("STAMP-0", TextOptions{X: 72, Y: 100})
	doc.SwitchToPage(1)
	doc.Text("STAMP-1", TextOptions{X: 72, Y: 100})

	if doc.currentPage != 1 {
		t.Fatalf("currentPage=%d, want 1", doc.currentPage)
	}
	if doc.Page() != doc.pages[1] {
		t.Fatal("Page() should return page selected by SwitchToPage")
	}
	if !bytes.Contains(doc.pages[0].content.Bytes(), []byte("STAMP-0")) {
		t.Fatal("stamp 0 missing from page 0")
	}
	if !bytes.Contains(doc.pages[1].content.Bytes(), []byte("STAMP-1")) {
		t.Fatal("stamp 1 missing from page 1")
	}
	if bytes.Contains(doc.pages[2].content.Bytes(), []byte("STAMP-")) {
		t.Fatal("stamps incorrectly landed on last page")
	}
}
