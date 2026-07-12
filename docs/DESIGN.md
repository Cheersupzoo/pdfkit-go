# Design notes

Inspiration: [PDFKit](https://github.com/foliojs/pdfkit) (generation + canvas API) and [pdf-lib](https://github.com/Hopding/pdf-lib) (create + modify).

## Goals

- Pure Go, **no CGO**
- PDFKit-like imperative canvas API
- pdf-lib-like open / stamp / merge
- Stream output via `Save(io.Writer)` for servers

## Dependency policy

| Rule | Detail |
| --- | --- |
| Allowed | Pure Go modules |
| Disallowed | CGO (HarfBuzz C, FreeType C, Cairo, …) |
| Prefer | Stdlib first (`image/jpeg`, `image/png`, `compress/zlib`) |

### Current dependencies

| Package | Role |
| --- | --- |
| [`tdewolff/font`](https://github.com/tdewolff/font) | TTF/OTF/WOFF/WOFF2/TTC parse + subset (+ Brotli) |
| [`boxesandglue/textshape`](https://github.com/boxesandglue/textshape) | OpenType GSUB/GPOS shaping (Thai marks, etc.) |

PDF object model, canvas, text layout, images, and merge stay in-repo. Do not depend on another full PDF library as the core.

## Architecture

```
pdfkit-go/                 # public API: Document, Page, graphics, text, fonts, images, merge
  internal/pdf/            # objects, writer, reader (xref / ObjStm)
  internal/svgpath/        # SVG path `d` parser
```

- Coordinates: PDF points, origin bottom-left (PDFKit-compatible)
- Target: PDF 1.7
- Output: `Save(io.Writer)`, `Bytes()`, `WriteFile(path)`

## Status

| Area | Status | Notes |
| --- | --- | --- |
| Create document | Done | Multi-page, metadata, Flate streams |
| Canvas / vectors | Done | Paths, SVG `d`, transforms, gradients, opacity, rounded rects |
| Text layout | Done | Soft hyphen wrap, align, lists, Standard 14 |
| Font embed + subset | Done | Via `tdewolff/font`; TrueType CIDFontType2 path |
| OpenType shaping | Done | Via `textshape` for embedded fonts |
| Images | Done | JPEG DCT pass-through; PNG + alpha SMask |
| Open / merge / stamp | Done | Unencrypted PDFs; full rewrite save |
| Stream to `io.Writer` | Done | `doc.Save(w)` |
| dfont | Deferred | Niche Mac format |
| CFF/OTF Type0 path | Partial | Prefer TrueType embeds for now |
| Incremental update | Deferred | Needed later for signed PDFs |
| Encryption / PDF/A | Deferred | Out of v1 scope |
| Fuzz / benches | Later | Hardening pass |

## Examples

```bash
go run ./examples/demo/       # vectors, text, images, merge
go run ./examples/cover/      # A4 English research cover
go run ./examples/cover-th/   # A4 Thai cover (TH Sarabun)
```

## References

- PDFKit: https://github.com/foliojs/pdfkit
- pdf-lib: https://github.com/Hopding/pdf-lib
- fontkit: https://github.com/foliojs/fontkit
- ISO 32000 (PDF structure, xref, incremental updates)
