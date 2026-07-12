# pdfkit-go — Research, Concerns, and Plan

Inspiration: [PDFKit](https://github.com/foliojs/pdfkit) (generation + canvas-like API) and [pdf-lib](https://github.com/Hopding/pdf-lib) (create **and** modify existing PDFs).

**Dependency policy (confirmed):** pure Go dependencies are allowed; **no CGO**. Prefer stdlib when it is enough; use mature pure-Go packages for fonts/WOFF2 and other non-trivial formats.

Repo currently: empty scaffold (`README.md` only).

---

## What the inspirations actually optimize for

| Library | Strength | Implication for Go port |
| --- | --- | --- |
| **PDFKit** | Streaming *creation*; chainable canvas API; vectors, text layout, fonts via **fontkit** | Feasible as a generation-first core. Much of the “hard” font work lives in a separate engine (fontkit), not in PDFKit itself. |
| **pdf-lib** | Load / edit / merge existing PDFs in any JS runtime | Requires a full PDF object model + parser/writer. This is a different (and larger) problem than generation. |

pdf-lib’s stated motivation: most JS PDF libs can only *create*; few robustly *modify*. Combining PDFKit-style authoring with pdf-lib-style mutation is the right product direction — but it is **two stacked projects**, not one.

---

## Dependency policy

### Rules

- **Allowed:** pure Go modules (no `import "C"`, no system shared libs).
- **Disallowed:** CGO wrappers (HarfBuzz via C, FreeType C bindings, libcairo, etc.).
- **Prefer:** stdlib first (`image/jpeg`, `image/png`, `compress/zlib`, `crypto`, …).
- **Pin carefully:** few deps, MIT/BSD/Apache-friendly licenses, active maintenance.
- **Optional / build tags:** advanced font formats may live behind optional imports so core generation stays lean.

### Recommended pure-Go building blocks

| Need | Candidate | Role |
| --- | --- | --- |
| WOFF2 / Brotli | [`andybalholm/brotli`](https://github.com/andybalholm/brotli) | Decode WOFF2 payload (and any future Brotli streams) |
| SFNT fonts + subset | [`tdewolff/font`](https://github.com/tdewolff/font) | Parse/write TTF/OTF/WOFF/WOFF2, subset, merge; already depends on brotli |
| Low-level SFNT metrics | [`golang.org/x/image/font/sfnt`](https://pkg.go.dev/golang.org/x/image/font/sfnt) | Glyph index / advances if we want thinner coupling than a full font toolkit |
| Unicode / BiDi (later) | [`golang.org/x/text`](https://pkg.go.dev/golang.org/x/text) | Segmentation, BiDi, optional when shaping expands |

**Practical recommendation:** use `tdewolff/font` as the font front-end for Phase 4 (covers TTF/OTF/WOFF/WOFF2 + subsetting in one pure-Go stack). Keep PDF object embedding, ToUnicode, and CIDFont wiring in-repo — that is the library’s product surface, not something to outsource to a competing PDF generator.

**Do not** depend on another full PDF library (gpdf, pdfcpu, unidoc, …) as the core; that would blur ownership of the object model and API.

### Still in-house (even with deps)

- PDF object model, content streams, xref reader/writer
- Canvas / SVG path / gradients
- Text layout (wrap, soft hyphen, align, lists)
- Image → XObject mapping
- Document modify/merge logic

---

## Concerns (decide before coding)

### 1. Font matrix — mostly unblocked under pure-Go deps

The requested font list matches PDFKit/fontkit:

- TrueType (`.ttf`), OpenType (`.otf`), WOFF, **WOFF2**, TTC, **dfont**
- Font **subsetting**

With pure-Go deps allowed, the former stdlib hard stop (**WOFF2/Brotli**) is gone.

| Capability | Approach |
| --- | --- |
| TTF / OTF + subset | Prefer `tdewolff/font` (or implement atop `x/image/font/sfnt`) |
| WOFF / WOFF2 | Covered by `tdewolff/font` + `andybalholm/brotli` |
| TTC | Supported by several pure-Go font libs; wire face selection by name/index |
| **dfont** | Still niche; likely custom or deferred — not a common pure-Go offering |
| PDF embedding | Always our code: Type0 / CIDFontType0|2 / ToUnicode / FontDescriptor |

**Remaining font risks (not dependency-policy issues):**

- CFF vs TrueType embedding paths differ in PDF
- Full OpenType shaping is still a separate engine (see concern 3)
- dfont remains optional / last

### 2. Create vs modify/merge: asymmetric difficulty

**Create new Document** — well understood: objects, content streams, xref, trailer.

**Modify / merge existing documents** — must handle real-world PDF chaos:

- Classic xref tables **and** xref streams
- Object streams (`ObjStm`)
- Incremental updates (`/Prev`) — required to preserve signatures
- Encryption / permissions (or an explicit “unsupported” error)
- Linearized PDFs
- Broken / non-spec PDFs that Acrobat still opens
- Resource renaming when merging pages (fonts, XObjects, ExtGState collisions)

**Recommendation:** ship **generation** to a usable MVP first; then a **reader + object model**; then **page import / merge**; then **in-place edit / incremental update**. Do not promise full pdf-lib parity in v0.1.

### 3. Font engine scope (hidden dependency)

PDFKit’s advanced text/glyph behavior comes from **fontkit** (GSUB/GPOS, AAT/`morx`, complex scripts, emoji). Porting that is a multi-subsystem effort by itself.

**Recommendation for v1 text:**

- Unicode → glyph via `cmap`
- Advances via `hmtx` (+ optional basic `kern`/`kern` pair table)
- Soft hyphen + greedy/Knuth-style wrap, alignments, bullets
- **Defer** full OpenType shaping / RTL / Indic until after MVP

### 4. Crowded Go landscape — need a clear niche

Existing pure-Go options include generation-focused libs (e.g. gpdf) and create/extract/merge tools (e.g. pdfcpu, other gopdf projects). Differentiation should be intentional:

- **PDFKit-like** imperative canvas API (`moveTo` / `lineTo` / `path` / `fill` / transforms / gradients)
- **pdf-lib-like** load → mutate → save
- Pure Go only (deps OK, no CGO), MIT

Without that API story, this risks duplicating existing generators.

### 5. Images — mostly fine with stdlib

| Format | Approach |
| --- | --- |
| JPEG | Prefer embed raw DCT bytes (`/Filter /DCTDecode`) when possible — no re-encode |
| PNG | Decode via `image/png`; emit Flate image XObject; alpha → soft mask (`SMask`) |
| Indexed PNG | Map to PDF `/Indexed` or expand to RGB — both doable |

Not a blocker. Care needed for color space, bits-per-component, and not destroying JPEG quality via unnecessary decode/re-encode.

### 6. Vectors / canvas — feasible, with design choices

PDF content operators map cleanly to a canvas API. Open decisions:

- **Y-axis:** PDF is bottom-up; canvas APIs are often top-down. Pick one public convention and document it (PDFKit uses bottom-left origin).
- **Gradients:** PDF shading patterns (`Axial` / `Radial`) — stdlib-free, moderate complexity
- **SVG path `d` parser:** self-contained, good early win
- **Graphics state stack:** `q`/`Q`, CTM, dash, line caps — straightforward

### 7. Legal / licensing of fonts and tests

- Do not bundle proprietary fonts.
- Prefer SIL OFL / Apache / MIT fonts for examples and golden tests.
- PDF test corpora may have mixed licenses; prefer generating fixtures in-repo.

### 8. Spec surface and version target

Recommend targeting **PDF 1.7** for generation (wide viewer support), with:

- Standard 14 fonts for MVP text without embedding
- Optional later: PDF/A, encryption, tagged PDF (PDFKit has some of these; they expand scope a lot)

---

## Verdict

**Pure-Go dependencies clear the main WOFF2/font-format blocker.** The project is ready to proceed under this policy.

Remaining decisions before coding:

1. **Scope order**: generation-first MVP, then parse/merge, then deep edit — not all features in parallel.
2. **dfont / full OT shaping**: in v1 or explicitly out?
3. **Modify/merge** in first release, or generation-only MVP?

Recommended default: generation MVP first; adopt `tdewolff/font` (+ transitive brotli) for Phase 4; defer dfont and advanced shaping.

---

## Proposed architecture

Layered packages (names illustrative):

```
pdfkit-go/
  pdf/          # low-level: objects, streams, xref, reader, writer
  content/      # content stream builder (operators)
  graphics/     # canvas: paths, transforms, colors, gradients, SVG path
  font/         # SFNT/WOFF parse, metrics, subset, PDF font dicts
  text/         # wrapping, align, lists (uses font metrics)
  image/        # JPEG/PNG → XObject
  document/     # Document / Page high-level API (PDFKit-ish)
  (later) merge # page import, resource remapping
```

Public API sketch (generation):

```go
doc := pdfkit.New()
doc.Page(pdfkit.A4)
doc.Font("Helvetica").FontSize(24)
doc.Text("Hello", 72, 720)
doc.MoveTo(100, 100).LineTo(200, 150).Stroke()
doc.Path("M10 80 C 40 10, 65 10, 95 80 S 150 150, 180 80").Stroke()
_ = doc.WriteFile("out.pdf")
```

Modification sketch (later):

```go
doc, _ := pdfkit.Open(r)
page := doc.PageAt(0)
page.DrawText("Watermark", ...)
pages, _ := other.Pages()
doc.CopyPages(pages...)
_ = doc.Save(w)
```

---

## Phased task breakdown

### Phase 0 — Foundations (repo + contracts)

- [ ] Module path, license (MIT recommended), Go version floor
- [ ] Codify dependency policy: pure Go only, no CGO; initial allowlist (`tdewolff/font`, `andybalholm/brotli`, optional `golang.org/x/...`)
- [ ] Package layout + `Document` / `Page` / `Canvas` interfaces
- [ ] Golden-test harness (write PDF bytes, optional visual later)
- [ ] Coordinate system + units (points) documented

### Phase 1 — PDF object core (create path)

- [ ] Indirect objects, dictionaries, arrays, names, strings, streams
- [ ] Flate filter (`compress/zlib`)
- [ ] Page tree, resources dict, content streams
- [ ] Xref + trailer writer; `Save` / `Bytes` / `Write`
- [ ] Standard page sizes; multi-page; metadata (`Info`)

**Exit criteria:** empty multi-page PDF opens in common viewers.

### Phase 2 — Vector graphics (canvas)

- [ ] Path ops: `moveTo`, `lineTo`, `curveTo`, `closePath`, rect helpers
- [ ] Stroke / fill / fillAndStroke; line width, cap, join, dash
- [ ] CTM: translate, scale, rotate, transform; save/restore graphics state
- [ ] Colors: RGB, Gray, (optional CMYK)
- [ ] SVG path `d` parser → path ops
- [ ] Linear + radial gradients (shading patterns + ExtGState for opacity if needed)
- [ ] Clipping paths; fill rules (nonzero / evenodd)

**Exit criteria:** PDFKit vector samples ported as Go tests/examples.

### Phase 3 — Text (basic) + Standard 14

- [ ] Standard 14 fonts (AFM metrics embedded or generated)
- [ ] `text` drawing with positioned TJ/Tj
- [ ] Line wrapping with soft hyphen (`U+00AD`) recognition
- [ ] Alignments: left, right, center, justify
- [ ] Bulleted / numbered lists
- [ ] Simple flow: `moveDown`, margins, continued text

**Exit criteria:** multi-paragraph wrapped layouts without custom fonts.

### Phase 4 — Font embedding + subsetting

- [ ] Integrate pure-Go font stack (`tdewolff/font` recommended) for load/subset of TTF/OTF/WOFF/WOFF2
- [ ] Embed as PDF Type0 / CIDFontType2 / ToUnicode (TrueType path first)
- [ ] Track used glyphs during text draw → subset at save
- [ ] OTF/CFF path (CIDFontType0) — after TTF works
- [ ] TTC face selection by name/index
- [ ] dfont — optional / last (likely custom; low priority)

**Exit criteria:** custom TTF (and at least one WOFF2) subset embedded; file size clearly smaller than full font.

### Phase 5 — Images

- [ ] JPEG: pass-through DCT when valid baseline/progressive policies decided
- [ ] PNG: RGB/Gray + alpha soft mask
- [ ] Indexed PNG
- [ ] DrawImage with scale/position (and optional clip)

**Exit criteria:** fixture suite for JPEG/PNG variants embeds correctly.

### Phase 6 — Parse existing PDFs

- [ ] Tokenizer + object parser
- [ ] Xref table + xref stream resolution
- [ ] Object stream inflation
- [ ] Page tree walk; content stream access (read-only first)
- [ ] Explicit unsupported: encrypted / damaged → clear errors

**Exit criteria:** open common unencrypted PDFs and enumerate pages/objects.

### Phase 7 — Modify + merge

- [ ] Append content to existing page (resource merge)
- [ ] Add/remove pages
- [ ] Copy pages across documents with resource remapping
- [ ] Full rewrite save; optional incremental update later
- [ ] Merge helper API

**Exit criteria:** stamp text/image on existing PDF; merge N files into one.

### Phase 8 — Hardening

- [ ] Fuzz parsers (font, PDF, SVG path, PNG edge cases)
- [ ] Memory bounds for hostile inputs
- [ ] Benchmarks (create simple doc; subset large CJK font)
- [ ] Docs + examples mirroring PDFKit getting-started / vector / text

---

## Suggested MVP slice (first shippable)

Ship **Phases 0–3 + JPEG/PNG basics + TTF embed with subsetting** (via pure-Go font dep). Pull WOFF/WOFF2 in once TTF embedding is solid — no longer blocked by stdlib.

Defer full modify/merge and dfont until after that generator MVP.

---

## Open decisions (need owner confirmation)

1. **dfont / full OT shaping:** in scope for v1 or explicitly out?
2. **Modify/merge:** required for first release, or generation-only MVP?
3. **Public API style:** PDFKit chainable methods vs pdf-lib `drawText` options structs (or both layers)?
4. **Module / package name:** keep `pdfkit-go` or a neutral name to avoid trademark confusion with FolioJS PDFKit?
5. **Font stack choice:** adopt `tdewolff/font` as default, or thinner `x/image/font/sfnt` + custom subsetter?

---

## References

- PDFKit: https://github.com/foliojs/pdfkit
- fontkit: https://github.com/foliojs/fontkit
- pdf-lib motivation: create + modify in any JS environment — https://github.com/Hopding/pdf-lib
- ISO 32000 PDF structure: xref, incremental updates, object streams
- Pure Go: [`andybalholm/brotli`](https://github.com/andybalholm/brotli), [`tdewolff/font`](https://github.com/tdewolff/font), [`golang.org/x/image/font/sfnt`](https://pkg.go.dev/golang.org/x/image/font/sfnt)
- Go stdlib still used for images/compression: `image/jpeg`, `image/png`, `compress/zlib`
