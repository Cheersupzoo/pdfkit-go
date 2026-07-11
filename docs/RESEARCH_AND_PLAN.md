# pdfkit-go — Research, Concerns, and Plan

Inspiration: [PDFKit](https://github.com/foliojs/pdfkit) (generation + canvas-like API) and [pdf-lib](https://github.com/Hopding/pdf-lib) (create **and** modify existing PDFs). Goal: a pure Go library using the standard library as much as possible.

Repo currently: empty scaffold (`README.md` only).

---

## What the inspirations actually optimize for

| Library | Strength | Implication for Go port |
| --- | --- | --- |
| **PDFKit** | Streaming *creation*; chainable canvas API; vectors, text layout, fonts via **fontkit** | Feasible as a generation-first core. Much of the “hard” font work lives in a separate engine (fontkit), not in PDFKit itself. |
| **pdf-lib** | Load / edit / merge existing PDFs in any JS runtime | Requires a full PDF object model + parser/writer. This is a different (and larger) problem than generation. |

pdf-lib’s stated motivation: most JS PDF libs can only *create*; few robustly *modify*. Combining PDFKit-style authoring with pdf-lib-style mutation is the right product direction — but it is **two stacked projects**, not one.

---

## Concerns (decide before coding)

### 1. “Stdlib only” conflicts with the stated font matrix

The requested font list matches PDFKit/fontkit:

- TrueType (`.ttf`), OpenType (`.otf`), WOFF, **WOFF2**, TTC, **dfont**
- Font **subsetting**

**Hard blockers for strict stdlib:**

| Capability | Stdlib? | Notes |
| --- | --- | --- |
| TTF parse + embed + subset | No (must implement) | Large but well-specified; doable in pure Go |
| OTF/CFF embed | No | Different PDF font objects (`CIDFontType0` vs `CIDFontType2`) |
| WOFF | No | zlib per-table; zlib is stdlib — format parse is local code |
| **WOFF2** | **No** | Needs **Brotli**; Brotli is **not** in Go stdlib |
| TTC | No | Directory of SFNT faces — moderate |
| **dfont** | No | Classic Mac resource-fork layout; niche, easy to defer |
| Subsetting | No | Non-trivial table rewriting (`glyf`/`loca`, `cmap`, `hmtx`, CFF charstrings, etc.) |

**Recommendation (policy choice):**

1. **Strict zero `go.mod` deps** → implement or vendor Brotli in-tree for WOFF2, *or* defer WOFF2 to a later phase.
2. **“At most stdlib + one pure-Go dep”** → allow `andybalholm/brotli` (or equivalent) only for WOFF2.
3. **Phase fonts**: TTF + subsetting first; OTF/CFF; WOFF; WOFF2; TTC; dfont last (or never).

Without an explicit policy, “stdlib only” and “full PDFKit font parity” cannot both be true.

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
- Zero (or near-zero) deps, MIT, no CGO

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

**No show-stopper that kills the project**, but two decisions must be locked before implementation:

1. **Dependency policy** for WOFF2/Brotli (defer vs vendor vs allow one pure-Go dep).
2. **Scope order**: generation-first MVP, then parse/merge, then deep edit — not all features in parallel.

With those accepted, proceed with the phased plan below.

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
- [ ] Document dependency policy (stdlib / WOFF2 decision)
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

- [ ] TTF parse: `head`, `maxp`, `cmap`, `hhea`, `hmtx`, `loca`, `glyf`, `name`, `post`
- [ ] Embed as PDF Type0 / CIDFontType2 / ToUnicode
- [ ] Subset used glyphs; rewrite tables; identity or custom encoding
- [ ] OTF/CFF path (CIDFontType0) — after TTF works
- [ ] WOFF decode (zlib)
- [ ] WOFF2 decode (per dependency policy)
- [ ] TTC face selection by name/index
- [ ] dfont — optional / last

**Exit criteria:** custom TTF subset embedded; file size clearly smaller than full font.

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

Ship **Phases 0–3 + JPEG/PNG basics + TTF embed (subset optional but preferred)** before investing in full modify/merge or WOFF2/dfont.

That yields a usable PDFKit-like generator while keeping the door open for pdf-lib-style APIs in Phases 6–7.

---

## Open decisions (need owner confirmation)

1. **WOFF2:** defer | vendor Brotli | allow one pure-Go dependency?
2. **dfont / full OTF shaping:** in scope for v1 or explicitly out?
3. **Modify/merge:** required for first release, or generation-only MVP?
4. **Public API style:** PDFKit chainable methods vs pdf-lib `drawText` options structs (or both layers)?
5. **Module / package name:** keep `pdfkit-go` or a neutral name to avoid trademark confusion with FolioJS PDFKit?

---

## References

- PDFKit: https://github.com/foliojs/pdfkit
- fontkit: https://github.com/foliojs/fontkit
- pdf-lib motivation: create + modify in any JS environment — https://github.com/Hopding/pdf-lib
- ISO 32000 PDF structure: xref, incremental updates, object streams
- Go stdlib: `image/jpeg`, `image/png`, `compress/zlib`, `compress/flate` — no Brotli
