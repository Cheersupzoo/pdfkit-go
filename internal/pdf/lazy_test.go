package pdf

import (
	"bytes"
	"testing"
)

func TestLazyOpenIndexesWithoutLoadingAllObjects(t *testing.T) {
	// Minimal valid PDF with classic xref and a few objects — built via Catalog.
	cat := NewCatalog()
	content, err := FlateStream(Dict{}, []byte("BT /F1 12 Tf 100 700 Td (Hi) Tj ET"))
	if err != nil {
		t.Fatal(err)
	}
	contentRef := cat.Add(content)
	fontRef := cat.Add(Dict{
		"Type":     Name("Font"),
		"Subtype":  Name("Type1"),
		"BaseFont": Name("Helvetica"),
	})
	var pageRefs []Ref
	for i := 0; i < 6; i++ {
		pageRefs = append(pageRefs, cat.Add(Dict{
			"Type":      Name("Page"),
			"MediaBox":  Array{Number(0), Number(0), Number(612), Number(792)},
			"Contents":  contentRef,
			"Resources": Dict{"Font": Dict{"F1": fontRef}},
		}))
	}
	kids := make(Array, len(pageRefs))
	for i, r := range pageRefs {
		kids[i] = r
	}
	pagesRef := cat.Add(Dict{"Type": Name("Pages"), "Kids": kids, "Count": Number(len(pageRefs))})
	for _, pref := range pageRefs {
		if pd, ok := cat.Get(pref.ID).(Dict); ok {
			pd["Parent"] = pagesRef
			cat.Set(pref.ID, pd)
		}
	}
	root := cat.Add(Dict{"Type": Name("Catalog"), "Pages": pagesRef})
	var buf bytes.Buffer
	if err := cat.Write(&buf, root, Ref{}); err != nil {
		t.Fatal(err)
	}

	model, err := Open(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer model.Close()

	xrefN := len(model.xref)
	cacheN := len(model.cache)
	if xrefN < 6 {
		t.Fatalf("expected xref entries, got %d", xrefN)
	}
	if cacheN > 2 {
		t.Fatalf("open should not load page contents; cache=%d xref=%d", cacheN, xrefN)
	}
	refs, err := model.PageRefs()
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 6 {
		t.Fatalf("pages=%d", len(refs))
	}
	// Page tree walk should not have pulled every content stream yet.
	if len(model.cache) >= xrefN {
		t.Fatalf("page walk loaded too much: cache=%d xref=%d", len(model.cache), xrefN)
	}
}

func TestOpenReaderAtRoundTrip(t *testing.T) {
	cat := NewCatalog()
	content, err := FlateStream(Dict{}, []byte("BT /F1 12 Tf 72 720 Td (A) Tj ET"))
	if err != nil {
		t.Fatal(err)
	}
	cRef := cat.Add(content)
	font := cat.Add(Dict{"Type": Name("Font"), "Subtype": Name("Type1"), "BaseFont": Name("Helvetica")})
	page := cat.Add(Dict{
		"Type": Name("Page"),
		"MediaBox": Array{Number(0), Number(0), Number(612), Number(792)},
		"Contents": cRef,
		"Resources": Dict{"Font": Dict{"F1": font}},
	})
	pages := cat.Add(Dict{"Type": Name("Pages"), "Kids": Array{page}, "Count": Number(1)})
	if pd, ok := cat.Get(page.ID).(Dict); ok {
		pd["Parent"] = pages
		cat.Set(page.ID, pd)
	}
	root := cat.Add(Dict{"Type": Name("Catalog"), "Pages": pages})
	var buf bytes.Buffer
	if err := cat.Write(&buf, root, Ref{}); err != nil {
		t.Fatal(err)
	}
	model, err := Open(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer model.Close()
	refs, err := model.PageRefs()
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("pages=%d", len(refs))
	}
	pd, err := model.GetPageDict(refs[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := model.Resolve(pd["Contents"]).(Stream); !ok {
		t.Fatalf("expected content stream, got %T", model.Resolve(pd["Contents"]))
	}
}
