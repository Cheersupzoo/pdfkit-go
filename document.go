package pdfkit

import (
	"fmt"
	"io"
	"os"

	"github.com/Cheersupzoo/pdfkit-go/internal/pdf"
)

// PageSize in PDF points (1/72 inch).
type PageSize struct {
	Width, Height float64
}

var (
	A4     = PageSize{595.28, 841.89}
	Letter = PageSize{612, 792}
	Legal  = PageSize{612, 1008}
	A3     = PageSize{841.89, 1190.55}
)

// Info holds document metadata.
type Info struct {
	Title, Author, Subject, Keywords, Creator, Producer string
}

// Document is a PDF being created or modified.
type Document struct {
	pages          []*Page
	fonts          map[string]*fontResource
	images         map[string]*imageResource
	patterns       map[string]*patternResource
	shadings       map[string]*shadingResource
	extGStates     map[string]*extGStateResource
	info           Info
	pageSize       PageSize
	margins        margins
	fontSize       float64
	fillRGB        [3]float64
	strokeRGB      [3]float64
	lineWidth      float64
	currentFont    *fontResource
	textX, textY   float64
	autoHyphen     bool
	err            error
	imported       *pdf.DocumentModel
	importedPages  []pdf.Ref
}

type margins struct {
	Top, Right, Bottom, Left float64
}

// New creates an empty document.
func New(opts ...Option) *Document {
	d := &Document{
		fonts:      map[string]*fontResource{},
		images:     map[string]*imageResource{},
		patterns:   map[string]*patternResource{},
		shadings:   map[string]*shadingResource{},
		extGStates: map[string]*extGStateResource{},
		pageSize:   Letter,
		margins:    margins{72, 72, 72, 72},
		fontSize:   12,
		fillRGB:    [3]float64{0, 0, 0},
		strokeRGB:  [3]float64{0, 0, 0},
		lineWidth:  1,
		info:       Info{Creator: "pdfkit-go", Producer: "pdfkit-go"},
		autoHyphen: true,
	}
	_ = d.Font("Helvetica")
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Option configures a Document.
type Option func(*Document)

func WithPageSize(s PageSize) Option {
	return func(d *Document) { d.pageSize = s }
}

func WithMargins(all float64) Option {
	return func(d *Document) { d.margins = margins{all, all, all, all} }
}

func WithInfo(info Info) Option {
	return func(d *Document) {
		if info.Creator == "" {
			info.Creator = d.info.Creator
		}
		if info.Producer == "" {
			info.Producer = d.info.Producer
		}
		d.info = info
	}
}

// AddPage appends a page and makes it current.
func (d *Document) AddPage(size ...PageSize) *Page {
	s := d.pageSize
	if len(size) > 0 {
		s = size[0]
	}
	p := &Page{
		doc:    d,
		width:  s.Width,
		height: s.Height,
		margin: d.margins,
	}
	p.content.WriteString("q\n")
	p.setDefaults()
	d.pages = append(d.pages, p)
	d.textX = d.margins.Left
	d.textY = s.Height - d.margins.Top - d.fontSize
	return p
}

// Page returns the current page, creating one if needed.
func (d *Document) Page() *Page {
	if len(d.pages) == 0 {
		return d.AddPage()
	}
	return d.pages[len(d.pages)-1]
}

// SwitchToPage selects a page by 0-based index.
func (d *Document) SwitchToPage(i int) *Page {
	if i < 0 || i >= len(d.pages) {
		d.err = fmt.Errorf("pdfkit: page index %d out of range", i)
		return d.Page()
	}
	return d.pages[i]
}

func (d *Document) PageCount() int { return len(d.pages) }

func (d *Document) Err() error { return d.err }

func (d *Document) setErr(err error) {
	if err != nil && d.err == nil {
		d.err = err
	}
}

// Save writes the PDF to w.
func (d *Document) Save(w io.Writer) error {
	if d.err != nil {
		return d.err
	}
	if len(d.pages) == 0 {
		d.AddPage()
	}
	for _, p := range d.pages {
		if p.imported != nil {
			return d.saveWithImports(w)
		}
	}
	return d.saveFresh(w)
}

func (d *Document) saveFresh(w io.Writer) error {
	cat := pdf.NewCatalog()

	// Build font objects (dedupe aliases)
	fontRefs := map[string]pdf.Ref{}
	embeddedFonts := map[*fontResource]pdf.Ref{}
	for name, fr := range d.fonts {
		if r, ok := embeddedFonts[fr]; ok {
			fontRefs[name] = r
			fontRefs[fr.name] = r
			continue
		}
		ref, err := fr.embed(cat)
		if err != nil {
			return err
		}
		embeddedFonts[fr] = ref
		fontRefs[name] = ref
		fontRefs[fr.name] = ref
	}
	imageRefs := map[string]pdf.Ref{}
	embeddedImages := map[*imageResource]pdf.Ref{}
	for name, ir := range d.images {
		if r, ok := embeddedImages[ir]; ok {
			imageRefs[name] = r
			continue
		}
		ref, err := ir.embed(cat)
		if err != nil {
			return err
		}
		embeddedImages[ir] = ref
		imageRefs[name] = ref
	}
	shadingRefs := map[string]pdf.Ref{}
	for name, sh := range d.shadings {
		shadingRefs[name] = cat.Add(sh.dict)
	}
	extRefs := map[string]pdf.Ref{}
	for name, eg := range d.extGStates {
		extRefs[name] = cat.Add(eg.dict)
	}
	patternRefs := map[string]pdf.Ref{}
	for name, pr := range d.patterns {
		patternRefs[name] = cat.Add(pr.dict)
	}

	pageRefs := make([]pdf.Object, 0, len(d.pages))
	pagesRefPlaceholder := pdf.Ref{ID: 0} // filled later
	_ = pagesRefPlaceholder

	// Reserve pages tree and catalog early for parent refs
	// We'll create page objects first with temporary parent, then set pages dict.

	var builtPages []pdf.Ref
	for _, page := range d.pages {
		contentData := append(append([]byte(nil), page.content.Bytes()...), []byte("Q\n")...)
		stream, err := pdf.FlateStream(pdf.Dict{}, contentData)
		if err != nil {
			return err
		}
		contentRef := cat.Add(stream)

		resources := pdf.Dict{
			"ProcSet": pdf.Array{pdf.Name("PDF"), pdf.Name("Text"), pdf.Name("ImageB"), pdf.Name("ImageC"), pdf.Name("ImageI")},
		}
		if len(page.usedFonts) > 0 || len(fontRefs) > 0 {
			fd := pdf.Dict{}
			for fn := range page.usedFonts {
				if r, ok := fontRefs[fn]; ok {
					fd[pdf.Name(fn)] = r
				}
			}
			resources["Font"] = fd
		}
		if len(page.usedImages) > 0 {
			xd := pdf.Dict{}
			for in := range page.usedImages {
				if r, ok := imageRefs[in]; ok {
					xd[pdf.Name(in)] = r
				}
			}
			resources["XObject"] = xd
		}
		if len(page.usedShadings) > 0 {
			sd := pdf.Dict{}
			for sn := range page.usedShadings {
				if r, ok := shadingRefs[sn]; ok {
					sd[pdf.Name(sn)] = r
				}
			}
			resources["Shading"] = sd
		}
		if len(page.usedExtG) > 0 {
			ed := pdf.Dict{}
			for en := range page.usedExtG {
				if r, ok := extRefs[en]; ok {
					ed[pdf.Name(en)] = r
				}
			}
			resources["ExtGState"] = ed
		}
		if len(page.usedPatterns) > 0 {
			pd := pdf.Dict{}
			for pn := range page.usedPatterns {
				if r, ok := patternRefs[pn]; ok {
					pd[pdf.Name(pn)] = r
				}
			}
			resources["Pattern"] = pd
		}

		pageDict := pdf.Dict{
			"Type":      pdf.Name("Page"),
			"MediaBox":  pdf.Array{pdf.Number(0), pdf.Number(0), pdf.Number(page.width), pdf.Number(page.height)},
			"Contents":  contentRef,
			"Resources": resources,
		}
		builtPages = append(builtPages, cat.Add(pageDict))
		pageRefs = append(pageRefs, builtPages[len(builtPages)-1])
	}

	kids := make(pdf.Array, len(builtPages))
	for i, r := range builtPages {
		kids[i] = r
	}
	pagesDict := pdf.Dict{
		"Type":  pdf.Name("Pages"),
		"Kids":  kids,
		"Count": pdf.Number(len(builtPages)),
	}
	pagesRef := cat.Add(pagesDict)
	for _, pref := range builtPages {
		if pd, ok := cat.Get(pref.ID).(pdf.Dict); ok {
			pd["Parent"] = pagesRef
			cat.Set(pref.ID, pd)
		}
	}

	catalog := pdf.Dict{
		"Type":  pdf.Name("Catalog"),
		"Pages": pagesRef,
	}
	rootRef := cat.Add(catalog)

	infoDict := pdf.Dict{}
	if d.info.Title != "" {
		infoDict["Title"] = pdf.String(d.info.Title)
	}
	if d.info.Author != "" {
		infoDict["Author"] = pdf.String(d.info.Author)
	}
	if d.info.Subject != "" {
		infoDict["Subject"] = pdf.String(d.info.Subject)
	}
	if d.info.Keywords != "" {
		infoDict["Keywords"] = pdf.String(d.info.Keywords)
	}
	if d.info.Creator != "" {
		infoDict["Creator"] = pdf.String(d.info.Creator)
	}
	if d.info.Producer != "" {
		infoDict["Producer"] = pdf.String(d.info.Producer)
	}
	infoRef := pdf.Ref{}
	if len(infoDict) > 0 {
		infoRef = cat.Add(infoDict)
	}
	return cat.Write(w, rootRef, infoRef)
}

// Bytes returns the PDF as a byte slice.
func (d *Document) Bytes() ([]byte, error) {
	var buf writerBuffer
	if err := d.Save(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteFile saves to a path.
func (d *Document) WriteFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return d.Save(f)
}

type writerBuffer struct {
	b []byte
}

func (w *writerBuffer) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}

func (w *writerBuffer) WriteString(s string) (int, error) {
	w.b = append(w.b, s...)
	return len(s), nil
}

func (w *writerBuffer) String() string { return string(w.b) }

func (w *writerBuffer) Bytes() []byte { return w.b }

func (w *writerBuffer) Len() int { return len(w.b) }

// Page is a single PDF page with a content stream builder.
type Page struct {
	doc          *Document
	width        float64
	height       float64
	margin       margins
	content      writerBuffer
	usedFonts    map[string]bool
	usedImages   map[string]bool
	usedShadings map[string]bool
	usedExtG     map[string]bool
	usedPatterns map[string]bool
	pathOpen     bool
	imported     *importedPage
}

func (p *Page) Width() float64  { return p.width }
func (p *Page) Height() float64 { return p.height }

func (p *Page) setDefaults() {
	p.usedFonts = map[string]bool{}
	p.usedImages = map[string]bool{}
	p.usedShadings = map[string]bool{}
	p.usedExtG = map[string]bool{}
	p.usedPatterns = map[string]bool{}
	fmt.Fprintf(&p.content, "%.5f w\n", p.doc.lineWidth)
	fmt.Fprintf(&p.content, "%.5f %.5f %.5f RG\n", p.doc.strokeRGB[0], p.doc.strokeRGB[1], p.doc.strokeRGB[2])
	fmt.Fprintf(&p.content, "%.5f %.5f %.5f rg\n", p.doc.fillRGB[0], p.doc.fillRGB[1], p.doc.fillRGB[2])
}

func (p *Page) write(format string, args ...any) {
	fmt.Fprintf(&p.content, format, args...)
}
