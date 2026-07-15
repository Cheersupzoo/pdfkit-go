package pdfkit

import (
	"bytes"
	"io"
	"os"

	"github.com/Cheersupzoo/pdfkit-go/internal/pdf"
)

// Open reads an existing PDF for modification / page copying.
// Non-seekable readers are spooled to a temp file. Objects are loaded lazily
// from the file until pages are materialized (Merge or Save).
func Open(r io.Reader) (*Document, error) {
	model, err := pdf.Open(r)
	if err != nil {
		return nil, err
	}
	return documentFromModel(model)
}

// OpenFile opens a PDF from disk without copying it into memory up front.
// The file remains open until Document.Close or after pages are materialized and the source is released.
func OpenFile(path string) (*Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	model, err := pdf.OpenReaderAt(f, st.Size(), f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return documentFromModel(model)
}

func documentFromModel(model *pdf.DocumentModel) (*Document, error) {
	d := New()
	d.imported = model
	refs, err := model.PageRefs()
	if err != nil {
		_ = model.Close()
		return nil, err
	}
	for _, ref := range refs {
		pd, err := model.GetPageDict(ref)
		if err != nil {
			_ = model.Close()
			return nil, err
		}
		w, h := pageSizeFromDict(pd)
		p := &Page{
			doc:      d,
			width:    w,
			height:   h,
			margin:   d.margins,
			imported: &importedPage{src: model, ref: ref},
		}
		p.setDefaults()
		d.pages = append(d.pages, p)
	}
	if len(d.pages) > 0 {
		d.currentPage = 0
	}
	return d, nil
}

// importedPage is either a lazy reference into a source DocumentModel,
// or materialized content/resources owned by Document.importCat.
type importedPage struct {
	src *pdf.DocumentModel
	ref pdf.Ref

	contents  pdf.Object // Ref or Array of Refs into importCat
	resources pdf.Dict
}

func (ip *importedPage) isMaterialized() bool {
	return ip != nil && ip.src == nil && (ip.contents != nil || ip.resources != nil)
}

func (d *Document) ensureImportCat() *pdf.Catalog {
	if d.importCat == nil {
		d.importCat = pdf.NewCatalog()
	}
	return d.importCat
}

// Merge appends all pages from other into d.
// Imported pages are materialized into d (cloned into d.importCat) so other can be Closed
// and its source file released without keeping all merge inputs in memory.
func (d *Document) Merge(other *Document) error {
	if other == nil {
		return nil
	}
	cat := d.ensureImportCat()
	seenBySrc := map[*pdf.DocumentModel]map[int]pdf.Ref{}
	var remapSeen map[int]pdf.Ref

	for _, op := range other.pages {
		p := &Page{
			doc:    d,
			width:  op.width,
			height: op.height,
			margin: op.margin,
		}
		p.setDefaults()
		if op.content.Len() > 0 {
			p.content.Write(op.content.Bytes())
			for k := range op.usedFonts {
				p.usedFonts[k] = true
				if fr, ok := other.fonts[k]; ok {
					d.fonts[k] = fr
				}
			}
			for k := range op.usedImages {
				p.usedImages[k] = true
				if ir, ok := other.images[k]; ok {
					d.images[k] = ir
				}
			}
			for k := range op.usedShadings {
				p.usedShadings[k] = true
				if sh, ok := other.shadings[k]; ok {
					d.shadings[k] = sh
				}
			}
		}

		if op.imported != nil {
			switch {
			case op.imported.src != nil:
				seen := seenBySrc[op.imported.src]
				if seen == nil {
					seen = map[int]pdf.Ref{}
					seenBySrc[op.imported.src] = seen
				}
				ip, err := materializeFromSrc(op.imported, cat, seen)
				if err != nil {
					return err
				}
				p.imported = ip
			case op.imported.isMaterialized():
				if remapSeen == nil {
					remapSeen = map[int]pdf.Ref{}
				}
				ip, err := remapMaterialized(op.imported, other.importCat, cat, remapSeen)
				if err != nil {
					return err
				}
				p.imported = ip
			}
		}
		d.pages = append(d.pages, p)
	}
	if len(d.pages) > 0 && d.currentPage < 0 {
		d.currentPage = 0
	}
	return nil
}

// MergeFiles opens and merges PDF files into d one at a time, releasing each
// source after its pages are materialized into d.
func (d *Document) MergeFiles(paths ...string) error {
	for _, path := range paths {
		other, err := OpenFile(path)
		if err != nil {
			return err
		}
		err = d.Merge(other)
		_ = other.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func pageSizeFromDict(pd pdf.Dict) (float64, float64) {
	mb, _ := pd["MediaBox"].(pdf.Array)
	if len(mb) >= 4 {
		x0, _ := mb[0].(pdf.Number)
		y0, _ := mb[1].(pdf.Number)
		x1, _ := mb[2].(pdf.Number)
		y1, _ := mb[3].(pdf.Number)
		return float64(x1 - x0), float64(y1 - y0)
	}
	return Letter.Width, Letter.Height
}

func materializeFromSrc(srcIP *importedPage, cat *pdf.Catalog, seen map[int]pdf.Ref) (*importedPage, error) {
	src := srcIP.src
	srcPage, err := src.GetPageDict(srcIP.ref)
	if err != nil {
		return nil, err
	}
	var contentRefs pdf.Array
	switch c := srcPage["Contents"].(type) {
	case pdf.Ref, pdf.Stream:
		contentRefs = append(contentRefs, cloneIntoCatalog(c, src, cat, seen))
	case pdf.Array:
		for _, item := range c {
			contentRefs = append(contentRefs, cloneIntoCatalog(item, src, cat, seen))
		}
	default:
		if resolved := src.Resolve(srcPage["Contents"]); resolved != nil {
			contentRefs = append(contentRefs, cloneIntoCatalog(resolved, src, cat, seen))
		}
	}
	var contents pdf.Object
	switch len(contentRefs) {
	case 0:
		contents = nil
	case 1:
		contents = contentRefs[0]
	default:
		contents = contentRefs
	}
	resources := pdf.Dict{}
	if res := src.Resolve(srcPage["Resources"]); res != nil {
		if rd, ok := res.(pdf.Dict); ok {
			resources = cloneObject(rd, src, cat, seen).(pdf.Dict)
		}
	}
	return &importedPage{contents: contents, resources: resources}, nil
}

func remapMaterialized(srcIP *importedPage, srcCat, dstCat *pdf.Catalog, seen map[int]pdf.Ref) (*importedPage, error) {
	if srcCat == nil {
		return &importedPage{
			contents:  srcIP.contents,
			resources: srcIP.resources,
		}, nil
	}
	var contents pdf.Object
	if srcIP.contents != nil {
		contents = remapObject(srcIP.contents, srcCat, dstCat, seen)
	}
	var resources pdf.Dict
	if srcIP.resources != nil {
		resources = remapObject(srcIP.resources, srcCat, dstCat, seen).(pdf.Dict)
	}
	return &importedPage{contents: contents, resources: resources}, nil
}

// materializeAllPending clones any still-lazy imported pages into importCat and closes sources.
func (d *Document) materializeAllPending() error {
	seenBySrc := map[*pdf.DocumentModel]map[int]pdf.Ref{}
	cat := d.ensureImportCat()
	for i, p := range d.pages {
		if p.imported == nil || p.imported.src == nil {
			continue
		}
		src := p.imported.src
		seen := seenBySrc[src]
		if seen == nil {
			seen = map[int]pdf.Ref{}
			seenBySrc[src] = seen
		}
		ip, err := materializeFromSrc(p.imported, cat, seen)
		if err != nil {
			return err
		}
		d.pages[i].imported = ip
	}
	for src := range seenBySrc {
		_ = src.Close()
	}
	if d.imported != nil {
		// Closed above if it had pages; still clear the handle.
		d.imported = nil
	}
	return nil
}

// --- save path for imported / materialized pages ---

func (d *Document) saveWithImports(w io.Writer) error {
	if err := d.materializeAllPending(); err != nil {
		return err
	}
	cat := d.ensureImportCat()

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

	var builtPages []pdf.Ref
	for _, page := range d.pages {
		var contentRefs pdf.Array
		resources := pdf.Dict{
			"ProcSet": pdf.Array{pdf.Name("PDF"), pdf.Name("Text"), pdf.Name("ImageB"), pdf.Name("ImageC"), pdf.Name("ImageI")},
		}

		if page.imported != nil {
			switch c := page.imported.contents.(type) {
			case pdf.Ref:
				contentRefs = append(contentRefs, c)
			case pdf.Array:
				contentRefs = append(contentRefs, c...)
			case nil:
				// no content
			default:
				contentRefs = append(contentRefs, cat.Add(c))
			}
			if page.imported.resources != nil {
				resources = mergeResources(resources, page.imported.resources)
			}
		}

		overlay := page.content.Bytes()
		if len(overlay) > 0 {
			data := overlay
			if page.imported != nil {
				data = append([]byte("q\n"), overlay...)
				data = append(data, []byte("\nQ\n")...)
			} else {
				data = append(overlay, []byte("Q\n")...)
			}
			stream, err := pdf.FlateStream(pdf.Dict{}, data)
			if err != nil {
				return err
			}
			contentRefs = append(contentRefs, cat.Add(stream))
		}

		if len(page.usedFonts) > 0 {
			fd, _ := resources["Font"].(pdf.Dict)
			if fd == nil {
				fd = pdf.Dict{}
			}
			for fn := range page.usedFonts {
				if r, ok := fontRefs[fn]; ok {
					fd[pdf.Name(fn)] = r
				}
			}
			resources["Font"] = fd
		}
		if len(page.usedImages) > 0 {
			xd, _ := resources["XObject"].(pdf.Dict)
			if xd == nil {
				xd = pdf.Dict{}
			}
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

		var contents pdf.Object
		if len(contentRefs) == 1 {
			contents = contentRefs[0]
		} else {
			contents = contentRefs
		}
		pageDict := pdf.Dict{
			"Type":      pdf.Name("Page"),
			"MediaBox":  pdf.Array{pdf.Number(0), pdf.Number(0), pdf.Number(page.width), pdf.Number(page.height)},
			"Contents":  contents,
			"Resources": resources,
		}
		builtPages = append(builtPages, cat.Add(pageDict))
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
	rootRef := cat.Add(pdf.Dict{"Type": pdf.Name("Catalog"), "Pages": pagesRef})
	infoRef := cat.Add(pdf.Dict{"Producer": pdf.String(d.info.Producer), "Creator": pdf.String(d.info.Creator)})
	return cat.Write(w, rootRef, infoRef)
}

func mergeResources(a, b pdf.Dict) pdf.Dict {
	out := pdf.Dict{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if existing, ok := out[k]; ok {
			ed, eOK := existing.(pdf.Dict)
			vd, vOK := v.(pdf.Dict)
			if eOK && vOK {
				merged := pdf.Dict{}
				for kk, vv := range ed {
					merged[kk] = vv
				}
				for kk, vv := range vd {
					merged[kk] = vv
				}
				out[k] = merged
				continue
			}
		}
		out[k] = v
	}
	return out
}

// cloneIntoCatalog clones obj into cat and returns a Ref. If obj is already a Ref,
// the shared seen map ensures the same source object is only materialized once.
func cloneIntoCatalog(obj pdf.Object, src *pdf.DocumentModel, cat *pdf.Catalog, seen map[int]pdf.Ref) pdf.Ref {
	cloned := cloneObject(obj, src, cat, seen)
	if ref, ok := cloned.(pdf.Ref); ok {
		return ref
	}
	return cat.Add(cloned)
}

func cloneObject(obj pdf.Object, src *pdf.DocumentModel, cat *pdf.Catalog, seen map[int]pdf.Ref) pdf.Object {
	switch o := obj.(type) {
	case pdf.Ref:
		if r, ok := seen[o.ID]; ok {
			return r
		}
		resolved := src.Resolve(o)
		placeholder := cat.Add(pdf.Null{})
		seen[o.ID] = placeholder
		cloned := cloneObject(resolved, src, cat, seen)
		cat.Set(placeholder.ID, cloned)
		return placeholder
	case pdf.Dict:
		nd := pdf.Dict{}
		for k, v := range o {
			if k == "Parent" {
				continue
			}
			nd[k] = cloneObject(v, src, cat, seen)
		}
		return nd
	case pdf.Array:
		na := make(pdf.Array, len(o))
		for i, v := range o {
			na[i] = cloneObject(v, src, cat, seen)
		}
		return na
	case pdf.Stream:
		nd := pdf.Dict{}
		for k, v := range o.Dict {
			nd[k] = cloneObject(v, src, cat, seen)
		}
		return pdf.Stream{Dict: nd, Data: append([]byte(nil), o.Data...)}
	default:
		return o
	}
}

// remapObject copies objects from srcCat into dstCat with a shared seen map.
func remapObject(obj pdf.Object, srcCat, dstCat *pdf.Catalog, seen map[int]pdf.Ref) pdf.Object {
	switch o := obj.(type) {
	case pdf.Ref:
		if r, ok := seen[o.ID]; ok {
			return r
		}
		placeholder := dstCat.Add(pdf.Null{})
		seen[o.ID] = placeholder
		raw := srcCat.Get(o.ID)
		dstCat.Set(placeholder.ID, remapObject(raw, srcCat, dstCat, seen))
		return placeholder
	case pdf.Dict:
		nd := pdf.Dict{}
		for k, v := range o {
			if k == "Parent" {
				continue
			}
			nd[k] = remapObject(v, srcCat, dstCat, seen)
		}
		return nd
	case pdf.Array:
		na := make(pdf.Array, len(o))
		for i, v := range o {
			na[i] = remapObject(v, srcCat, dstCat, seen)
		}
		return na
	case pdf.Stream:
		nd := pdf.Dict{}
		for k, v := range o.Dict {
			nd[k] = remapObject(v, srcCat, dstCat, seen)
		}
		return pdf.Stream{Dict: nd, Data: append([]byte(nil), o.Data...)}
	default:
		return o
	}
}

// HasLiveImportSource reports whether any page still references an open source model.
// Intended for tests and diagnostics.
func (d *Document) HasLiveImportSource() bool {
	if d.imported != nil {
		return true
	}
	for _, p := range d.pages {
		if p.imported != nil && p.imported.src != nil {
			return true
		}
	}
	return false
}

// helper used by tests
func LoadPDFBytes(b []byte) (*Document, error) {
	return Open(bytes.NewReader(b))
}
