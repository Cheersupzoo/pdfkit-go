package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	pdfkit "github.com/Cheersupzoo/pdfkit-go"
)

func main() {
	outDir := "examples/demo/out"
	_ = os.MkdirAll(outDir, 0o755)

	doc := pdfkit.New(
		pdfkit.WithPageSize(pdfkit.Letter),
		pdfkit.WithInfo(pdfkit.Info{
			Title:  "pdfkit-go demo",
			Author: "pdfkit-go",
		}),
	)
	doc.AddPage()

	// Brand / title
	doc.Font("Helvetica").FontSize(28).FillColor(pdfkit.HexColor("#0B3D5C"))
	doc.Text("pdfkit-go", pdfkit.TextOptions{X: 72, Y: 720})

	doc.FontSize(12).FillColor(pdfkit.RGB(0.15, 0.15, 0.15))
	doc.Text("Pure Go PDF creation - vectors, text, fonts, images, merge.", pdfkit.TextOptions{
		X: 72, Y: 690, Width: 468,
	})

	// Vector graphics
	doc.StrokeColor(pdfkit.HexColor("#0B3D5C")).LineWidth(2)
	doc.MoveTo(72, 660).LineTo(540, 660).Stroke()

	doc.FillColor(pdfkit.HexColor("#E8A838"))
	doc.Circle(120, 600, 28).Fill()

	doc.StrokeColor(pdfkit.HexColor("#1F7A8C")).LineWidth(1.5)
	doc.Path("M200 580 C230 640, 280 640, 310 580 S390 520, 420 580").Stroke()

	// Gradient
	sh := doc.LinearGradient(72, 500, 300, 500, []pdfkit.GradientStop{
		{Offset: 0, Color: pdfkit.HexColor("#1F7A8C")},
		{Offset: 1, Color: pdfkit.HexColor("#E8A838")},
	})
	doc.SaveGraphics()
	doc.Rect(72, 480, 228, 40).Clip()
	doc.FillShading(sh)
	doc.RestoreGraphics()

	// Wrapped text with soft hyphen
	doc.FillColor(pdfkit.RGB(0, 0, 0)).FontSize(11)
	doc.Text("This paragraph demon\u00adstrates line wrapping with soft\u00adhyphen recognition, left alignment, and comfortable reading measure across the page width.", pdfkit.TextOptions{
		X: 72, Y: 450, Width: 280, Align: pdfkit.AlignLeft,
	})

	doc.Text("Centered heading", pdfkit.TextOptions{
		X: 72, Y: 360, Width: 280, Align: pdfkit.AlignCenter,
	})

	doc.List([]string{
		"Create documents",
		"Draw vectors & SVG paths",
		"Embed images & fonts",
		"Merge existing PDFs",
	}, pdfkit.TextOptions{X: 72, Y: 330, Width: 280})

	// Images
	root, _ := os.Getwd()
	pngPath := filepath.Join(root, "testdata/images/sample.png")
	jpgPath := filepath.Join(root, "testdata/images/sample.jpg")
	if _, err := os.Stat(pngPath); err == nil {
		doc.ImageFile(pngPath, 380, 400, 140, 0)
	}
	if _, err := os.Stat(jpgPath); err == nil {
		doc.ImageFile(jpgPath, 380, 280, 140, 0)
	}

	// Embedded font page
	fontPath := filepath.Join(root, "testdata/fonts/DejaVuSans.ttf")
	doc.AddPage()
	doc.Font("Helvetica").FontSize(18).FillColor(pdfkit.HexColor("#0B3D5C"))
	doc.Text("Embedded TrueType", pdfkit.TextOptions{X: 72, Y: 720})
	if err := doc.RegisterFontFile("DejaVu", fontPath, 0); err != nil {
		log.Printf("font: %v", err)
	} else {
		doc.Font("DejaVu").FontSize(14).FillColor(pdfkit.RGB(0, 0, 0))
		doc.Text("Hello from DejaVu Sans — subset embedded glyphs only.", pdfkit.TextOptions{
			X: 72, Y: 680, Width: 468,
		})
		doc.Text("ÀÉÎÖÜ çñß  — Latin accents via custom font.", pdfkit.TextOptions{
			X: 72, Y: 650, Width: 468,
		})
	}

	doc.Font("Helvetica").FontSize(12)
	doc.Text("Transforms & opacity", pdfkit.TextOptions{X: 72, Y: 600})
	doc.SaveGraphics().Translate(120, 520).Rotate(25).Opacity(0.5)
	doc.FillColor(pdfkit.HexColor("#1F7A8C")).Rect(0, 0, 80, 50).Fill()
	doc.RestoreGraphics()

	out := filepath.Join(outDir, "demo.pdf")
	if err := doc.WriteFile(out); err != nil {
		log.Fatal(err)
	}
	fmt.Println("wrote", out)

	// Merge proof: create second doc and merge
	a := pdfkit.New(pdfkit.WithPageSize(pdfkit.Letter))
	a.AddPage()
	a.FontSize(20).Text("Document A", pdfkit.TextOptions{X: 72, Y: 700})
	aPath := filepath.Join(outDir, "a.pdf")
	_ = a.WriteFile(aPath)

	b := pdfkit.New(pdfkit.WithPageSize(pdfkit.Letter))
	b.AddPage()
	b.FontSize(20).Text("Document B", pdfkit.TextOptions{X: 72, Y: 700})
	bPath := filepath.Join(outDir, "b.pdf")
	_ = b.WriteFile(bPath)

	merged := pdfkit.New()
	if err := merged.MergeFiles(aPath, bPath); err != nil {
		log.Fatal("merge:", err)
	}
	// stamp on first page
	merged.SwitchToPage(0)
	merged.FontSize(10).FillColor(pdfkit.RGB(0.8, 0, 0))
	merged.Text("merged stamp", pdfkit.TextOptions{X: 72, Y: 650})
	mPath := filepath.Join(outDir, "merged.pdf")
	if err := merged.WriteFile(mPath); err != nil {
		log.Fatal(err)
	}
	fmt.Println("wrote", mPath)
}
