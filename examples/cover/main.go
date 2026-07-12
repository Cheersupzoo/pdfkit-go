package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	pdfkit "github.com/Cheersupzoo/pdfkit-go"
)

func main() {
	outDir := "examples/cover/out"
	_ = os.MkdirAll(outDir, 0o755)

	doc := pdfkit.New(
		pdfkit.WithPageSize(pdfkit.A4),
		pdfkit.WithMargins(0),
		pdfkit.WithInfo(pdfkit.Info{
			Title:   "A Formal Framework for Pure-Go PDF Generation",
			Author:  "S. Thanrukprasert",
			Subject: "Research paper cover",
		}),
	)
	page := doc.AddPage(pdfkit.A4)
	w, h := page.Width(), page.Height()

	ink := pdfkit.HexColor("#1A2332")
	accent := pdfkit.HexColor("#2F6F8F")
	muted := pdfkit.HexColor("#5A6570")
	rule := pdfkit.HexColor("#C5CED6")

	// Soft page wash
	doc.FillColor(pdfkit.HexColor("#F7F5F1"))
	doc.Rect(0, 0, w, h).Fill()

	// Outer rounded border frame
	margin := 36.0
	radius := 18.0
	doc.StrokeColor(ink).LineWidth(1.75)
	doc.RoundedRect(margin, margin, w-2*margin, h-2*margin, radius).Stroke()

	// Inner hairline rounded border
	inner := 10.0
	doc.StrokeColor(rule).LineWidth(0.6)
	doc.RoundedRect(margin+inner, margin+inner, w-2*(margin+inner), h-2*(margin+inner), radius-4).Stroke()

	// Top accent bar inside the frame
	doc.FillColor(accent)
	doc.Rect(margin+28, h-margin-72, w-2*(margin+28), 3).Fill()

	// Journal / series line
	doc.FillColor(accent).Font("Helvetica").FontSize(10)
	doc.Text("TECHNICAL REPORT  ·  VOL. 12  ·  NO. 3", pdfkit.TextOptions{
		X: margin + 40, Y: h - margin - 100, Width: w - 2*(margin+40), Align: pdfkit.AlignCenter,
	})

	// Title
	doc.FillColor(ink).FontSize(26)
	doc.Text("A Formal Framework for Pure-Go\nPDF Generation and Document Assembly", pdfkit.TextOptions{
		X: margin + 48, Y: h - margin - 160, Width: w - 2*(margin+48), Align: pdfkit.AlignCenter, LineGap: 8,
	})

	// Subtitle
	doc.FillColor(muted).FontSize(12)
	doc.Text("Toward a canvas-oriented API with embeddable fonts, vector graphics,\nand lossless merge of existing PDF structures", pdfkit.TextOptions{
		X: margin + 56, Y: h - margin - 260, Width: w - 2*(margin+56), Align: pdfkit.AlignCenter, LineGap: 4,
	})

	// Decorative short rules
	cx := w / 2
	doc.StrokeColor(accent).LineWidth(1)
	doc.MoveTo(cx-36, h/2+40).LineTo(cx+36, h/2+40).Stroke()

	// Authors
	doc.FillColor(ink).FontSize(13)
	doc.Text("Suppachai Thanrukprasert", pdfkit.TextOptions{
		X: margin + 48, Y: h/2 + 10, Width: w - 2*(margin+48), Align: pdfkit.AlignCenter,
	})
	doc.FillColor(muted).FontSize(10)
	doc.Text("Department of Software Systems  ·  Independent Research", pdfkit.TextOptions{
		X: margin + 48, Y: h/2 - 12, Width: w - 2*(margin+48), Align: pdfkit.AlignCenter,
	})

	// Abstract box with rounded corners
	boxX, boxY := margin+48, margin+110
	boxW, boxH := w-2*(margin+48), 150.0
	doc.StrokeColor(rule).LineWidth(0.8)
	doc.RoundedRect(boxX, boxY, boxW, boxH, 10).Stroke()

	doc.FillColor(accent).FontSize(9)
	doc.Text("ABSTRACT", pdfkit.TextOptions{
		X: boxX + 16, Y: boxY + boxH - 22, Width: boxW - 32, Align: pdfkit.AlignLeft,
	})
	doc.FillColor(ink).FontSize(10)
	doc.Text("We present pdfkit-go, a pure-Go library for constructing and assembling PDF documents. The design combines a PDFKit-inspired imperative canvas with pdf-lib-style open and merge operations, while remaining free of CGO. This cover page demonstrates A4 layout, typographic hierarchy, and stroked rounded rectangles used as structural borders.", pdfkit.TextOptions{
		X: boxX + 16, Y: boxY + boxH - 42, Width: boxW - 32, Align: pdfkit.AlignLeft, LineGap: 3,
	})

	// Footer meta
	doc.FillColor(muted).FontSize(9)
	doc.Text("July 2026                                                    preprint", pdfkit.TextOptions{
		X: margin + 48, Y: margin + 58, Width: w - 2*(margin+48), Align: pdfkit.AlignLeft,
	})

	out := filepath.Join(outDir, "research-cover.pdf")
	if err := doc.WriteFile(out); err != nil {
		log.Fatal(err)
	}
	fmt.Println("wrote", out)
}
