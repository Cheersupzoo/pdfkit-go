# pdfkit-go

Pure Go PDF library inspired by [PDFKit](https://github.com/foliojs/pdfkit) and [pdf-lib](https://github.com/Hopding/pdf-lib).

No CGO. Dependencies are limited to pure-Go font tooling (`tdewolff/font`, `boxesandglue/textshape`).

## Features

- Create documents; stream with `Save(io.Writer)` (HTTP-friendly)
- Open, stamp, and merge existing unencrypted PDFs
- Vector graphics: canvas API, SVG paths, transforms, gradients, opacity, rounded rects
- Text: soft-hyphen wrapping, alignments, lists, Standard 14 fonts
- Font embedding + subsetting (TTF/OTF/WOFF/WOFF2/TTC) with OpenType shaping
- Images: JPEG pass-through, PNG (including alpha)

## Install

```bash
go get github.com/Cheersupzoo/pdfkit-go
```

## Quick start

```go
package main

import (
	"log"
	"net/http"

	pdfkit "github.com/Cheersupzoo/pdfkit-go"
)

func main() {
	http.HandleFunc("/pdf", func(w http.ResponseWriter, r *http.Request) {
		doc := pdfkit.New(pdfkit.WithPageSize(pdfkit.A4))
		doc.AddPage()
		doc.Font("Helvetica").FontSize(24)
		doc.Text("Hello from pdfkit-go", pdfkit.TextOptions{X: 72, Y: 750})
		doc.MoveTo(72, 720).LineTo(300, 720).Stroke()

		w.Header().Set("Content-Type", "application/pdf")
		if err := doc.Save(w); err != nil {
			http.Error(w, err.Error(), 500)
		}
	})
	log.Fatal(http.ListenAndServe(":8080", nil))
}
```

## Examples

```bash
go test ./...
go run ./examples/demo/
go run ./examples/cover/
go run ./examples/cover-th/   # Thai + TH Sarabun
```

## Design

See [docs/DESIGN.md](docs/DESIGN.md).

## License

MIT
