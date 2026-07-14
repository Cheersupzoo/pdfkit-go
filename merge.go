package pdfkit

import (
	"bytes"
	"io"
	"os"

	"github.com/Cheersupzoo/pdfkit-go/internal/pdf"
)

// Open reads an existing PDF for modification / page copying.
func Open(r io.Reader) (*Document, error) {
	model, err := pdf.Open(r)
	if err != nil {
		return nil, err
	}
	d := New()
	d.imported = model
	refs, err := model.PageRefs()
	if err != nil {
		return nil, err
	}
	d.importedPages = refs
	for _, ref := range refs {
		pd, err := model.GetPageDict(ref)
		if err != nil {
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
	return d, nil
}

// OpenFile opens a PDF from disk.
func OpenFile(path string) (*Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Open(f)
}

type importedPage struct {
	src *pdf.DocumentModel
	ref pdf.Ref
}

// Merge appends all pages from other into d.
func (d *Document) Merge(other *Document) error {
	if other.imported != nil && len(other.importedPages) > 0 {
		for i, ref := range other.importedPages {
			pd, err := other.imported.GetPageDict(ref)
			if err != nil {
				return err
			}
			w, h := pageSizeFromDict(pd)
			p := &Page{
				doc:      d,
				width:    w,
				height:   h,
				margin:   d.margins,
				imported: &importedPage{src: other.imported, ref: ref},
			}
			p.setDefaults()
			// also carry overlay content from other's page if any
			if i < len(other.pages) && other.pages[i].content.Len() > 0 {
				p.content.Write(other.pages[i].content.Bytes())
				for k := range other.pages[i].usedFonts {
					p.usedFonts[k] = true
					if fr, ok := other.fonts[k]; ok {
						d.fonts[k] = fr
					}
				}
				for k := range other.pages[i].usedImages {
					p.usedImages[k] = true
					if ir, ok := other.images[k]; ok {
						d.images[k] = ir
					}
				}
			}
			d.pages = append(d.pages, p)
		}
		return nil
	}
	// merge generated pages by copying content buffers
	for _, op := range other.pages {
		p := &Page{
			doc:    d,
			width:  op.width,
			height: op.height,
			margin: op.margin,
		}
		p.setDefaults()
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
		d.pages = append(d.pages, p)
	}
	return nil
}

// MergeFiles opens and merges PDF files into d.
func (d *Document) MergeFiles(paths ...string) error {
	for _, path := range paths {
		other, err := OpenFile(path)
		if err != nil {
			return err
		}
		if err := d.Merge(other); err != nil {
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

// --- deep copy helpers for imported pages during Save ---

func (d *Document) saveWithImports(w io.Writer) error {
	// Fallback path invoked when any page is imported.
	cat := pdf.NewCatalog()
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
			srcPage, err := page.imported.src.GetPageDict(page.imported.ref)
			if err != nil {
				return err
			}
			// copy original contents
			origContents := page.imported.src.Resolve(srcPage["Contents"])
			switch c := origContents.(type) {
			case pdf.Stream:
				contentRefs = append(contentRefs, cat.Add(cloneObject(c, page.imported.src, cat, map[int]pdf.Ref{})))
			case pdf.Array:
				for _, item := range c {
					obj := page.imported.src.Resolve(item)
					contentRefs = append(contentRefs, cat.Add(cloneObject(obj, page.imported.src, cat, map[int]pdf.Ref{})))
				}
			case pdf.Ref:
				obj := page.imported.src.Resolve(c)
				contentRefs = append(contentRefs, cat.Add(cloneObject(obj, page.imported.src, cat, map[int]pdf.Ref{})))
			}
			// merge original resources shallowly (fonts/xobjects by cloning)
			if res := page.imported.src.Resolve(srcPage["Resources"]); res != nil {
				if rd, ok := res.(pdf.Dict); ok {
					resources = mergeResources(resources, cloneObject(rd, page.imported.src, cat, map[int]pdf.Ref{}).(pdf.Dict))
				}
			}
		}

		overlay := page.content.Bytes()
		if len(overlay) > 0 {
			// ensure graphics state balanced for overlay
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

func cloneObject(obj pdf.Object, src *pdf.DocumentModel, cat *pdf.Catalog, seen map[int]pdf.Ref) pdf.Object {
	switch o := obj.(type) {
	case pdf.Ref:
		if r, ok := seen[o.ID]; ok {
			return r
		}
		resolved := src.Resolve(o)
		// reserve id
		placeholder := cat.Add(pdf.Null{})
		seen[o.ID] = placeholder
		cloned := cloneObject(resolved, src, cat, seen)
		cat.Set(placeholder.ID, cloned.(pdf.Object))
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

// helper used by tests
func LoadPDFBytes(b []byte) (*Document, error) {
	return Open(bytes.NewReader(b))
}
