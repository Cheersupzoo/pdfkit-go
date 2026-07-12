# pdfkit-go

Pure Go library for creating and manipulating PDFs, inspired by [PDFKit](https://github.com/foliojs/pdfkit) and [pdf-lib](https://github.com/Hopding/pdf-lib).

**Policy:** pure Go only (dependencies allowed, no CGO). Font parsing/subsetting uses [`tdewolff/font`](https://github.com/tdewolff/font); OpenType shaping (Thai/Arabic/Indic mark positioning, GSUB/GPOS) uses [`boxesandglue/textshape`](https://github.com/boxesandglue/textshape).

## Features

- Create new documents
- Open, stamp, and merge existing PDFs
- Vector graphics (canvas-like API, SVG path `d`, transforms, linear/radial gradients, opacity)
- Text (wrapping with soft hyphens, alignments, bulleted lists)
- Font embedding with subsetting (TTF/OTF/WOFF/WOFF2/TTC via `RegisterFont`)
- Image embedding (JPEG pass-through, PNG including alpha)

## Quick start

```go
package main

import pdfkit "github.com/Cheersupzoo/pdfkit-go"

func main() {
	doc := pdfkit.New(pdfkit.WithPageSize(pdfkit.Letter))
	doc.AddPage()
	doc.Font("Helvetica").FontSize(24)
	doc.Text("Hello from pdfkit-go", pdfkit.TextOptions{X: 72, Y: 720})
	doc.MoveTo(72, 680).LineTo(300, 680).Stroke()
	_ = doc.WriteFile("hello.pdf")
}
```

## Demo

```bash
go run ./examples/demo/
# writes examples/demo/out/demo.pdf and merged.pdf
```

## Design notes

See [docs/RESEARCH_AND_PLAN.md](docs/RESEARCH_AND_PLAN.md).
