package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	pdfkit "github.com/Cheersupzoo/pdfkit-go"
)

func main() {
	outDir := "examples/cover-th/out"
	_ = os.MkdirAll(outDir, 0o755)

	root, _ := os.Getwd()
	regular := filepath.Join(root, "testdata/fonts/THSarabun-Regular.ttf")
	bold := filepath.Join(root, "testdata/fonts/THSarabun-Bold.ttf")

	doc := pdfkit.New(
		pdfkit.WithPageSize(pdfkit.A4),
		pdfkit.WithMargins(0),
		pdfkit.WithInfo(pdfkit.Info{
			Title:   "กรอบแนวคิดเชิงรูปนัยสำหรับการสร้างเอกสาร PDF ด้วยภาษา Go",
			Author:  "ศุภชัย ธารรักประเสริฐ",
			Subject: "ปกบทความวิจัย",
		}),
	)

	if err := doc.RegisterFontFile("THSarabun", regular, 0); err != nil {
		log.Fatal(err)
	}
	if err := doc.RegisterFontFile("THSarabunBold", bold, 0); err != nil {
		log.Fatal(err)
	}

	page := doc.AddPage(pdfkit.A4)
	w, h := page.Width(), page.Height()

	ink := pdfkit.HexColor("#1A2332")
	accent := pdfkit.HexColor("#2F6F8F")
	muted := pdfkit.HexColor("#5A6570")
	rule := pdfkit.HexColor("#C5CED6")

	doc.FillColor(pdfkit.HexColor("#F7F5F1"))
	doc.Rect(0, 0, w, h).Fill()

	margin := 36.0
	radius := 18.0
	doc.StrokeColor(ink).LineWidth(1.75)
	doc.RoundedRect(margin, margin, w-2*margin, h-2*margin, radius).Stroke()

	inner := 10.0
	doc.StrokeColor(rule).LineWidth(0.6)
	doc.RoundedRect(margin+inner, margin+inner, w-2*(margin+inner), h-2*(margin+inner), radius-4).Stroke()

	doc.FillColor(accent)
	doc.Rect(margin+28, h-margin-72, w-2*(margin+28), 3).Fill()

	doc.Font("THSarabun").FontSize(14).FillColor(accent)
	doc.Text("รายงานทางเทคนิค  ·  ปีที่ 12  ·  ฉบับที่ 3", pdfkit.TextOptions{
		X: margin + 40, Y: h - margin - 100, Width: w - 2*(margin+40), Align: pdfkit.AlignCenter,
	})

	doc.Font("THSarabunBold").FontSize(28).FillColor(ink)
	doc.Text("กรอบแนวคิดเชิงรูปนัยสำหรับการสร้าง", pdfkit.TextOptions{
		X: margin + 48, Y: h - margin - 155, Width: w - 2*(margin+48), Align: pdfkit.AlignCenter,
	})
	doc.Text("และประกอบเอกสาร PDF ด้วยภาษา Go", pdfkit.TextOptions{
		X: margin + 48, Y: h - margin - 190, Width: w - 2*(margin+48), Align: pdfkit.AlignCenter,
	})

	doc.Font("THSarabun").FontSize(14).FillColor(muted)
	doc.Text("สู่เอพีไอแบบแคนวาส พร้อมแบบอักษรฝังตัว กราฟิกเวกเตอร์", pdfkit.TextOptions{
		X: margin + 56, Y: h - margin - 240, Width: w - 2*(margin+56), Align: pdfkit.AlignCenter,
	})
	doc.Text("และการรวมไฟล์ PDF เดิมโดยไม่สูญเสียโครงสร้าง", pdfkit.TextOptions{
		X: margin + 56, Y: h - margin - 258, Width: w - 2*(margin+56), Align: pdfkit.AlignCenter,
	})

	cx := w / 2
	doc.StrokeColor(accent).LineWidth(1)
	doc.MoveTo(cx-36, h/2+28).LineTo(cx+36, h/2+28).Stroke()

	doc.Font("THSarabunBold").FontSize(16).FillColor(ink)
	doc.Text("ศุภชัย ธารรักประเสริฐ", pdfkit.TextOptions{
		X: margin + 48, Y: h/2 - 2, Width: w - 2*(margin+48), Align: pdfkit.AlignCenter,
	})
	doc.Font("THSarabun").FontSize(12).FillColor(muted)
	doc.Text("ภาควิชาระบบซอฟต์แวร์  ·  งานวิจัยอิสระ", pdfkit.TextOptions{
		X: margin + 48, Y: h/2 - 24, Width: w - 2*(margin+48), Align: pdfkit.AlignCenter,
	})

	boxX, boxY := margin+48, margin+110
	boxW, boxH := w-2*(margin+48), 150.0
	doc.StrokeColor(rule).LineWidth(0.8)
	doc.RoundedRect(boxX, boxY, boxW, boxH, 10).Stroke()

	doc.Font("THSarabunBold").FontSize(12).FillColor(accent)
	doc.Text("บทคัดย่อ", pdfkit.TextOptions{
		X: boxX + 16, Y: boxY + boxH - 22, Width: boxW - 32, Align: pdfkit.AlignLeft,
	})
	doc.Font("THSarabun").FontSize(13).FillColor(ink)
	doc.Text("งานนี้เสนอ pdfkit-go ไลบรารีภาษา Go ล้วน สำหรับสร้างและประกอบเอกสาร PDF โดยผสมแนวทางแบบ PDFKit สำหรับแคนวาส และ pdf-lib สำหรับเปิดรวมไฟล์ โดยไม่พึ่งพา CGO หน้าปกนี้แสดงเค้าโครงกระดาษ A4 ลำดับชั้นตัวอักษรไทยด้วยฟอนต์ TH Sarabun และกรอบมุมโค้ง", pdfkit.TextOptions{
		X: boxX + 16, Y: boxY + boxH - 44, Width: boxW - 32, Align: pdfkit.AlignLeft, LineGap: 4,
	})

	doc.Font("THSarabun").FontSize(12).FillColor(muted)
	doc.Text("กรกฎาคม 2569                                                    ฉบับร่างก่อนตีพิมพ์", pdfkit.TextOptions{
		X: margin + 48, Y: margin + 58, Width: w - 2*(margin+48), Align: pdfkit.AlignLeft,
	})

	out := filepath.Join(outDir, "research-cover-th.pdf")
	if err := doc.WriteFile(out); err != nil {
		log.Fatal(err)
	}
	fmt.Println("wrote", out)
}
