package pdf

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// DocumentModel is a parsed PDF suitable for page extraction / merge.
type DocumentModel struct {
	Objects map[int]Object // object number -> resolved object (may still contain Refs)
	Root    Ref
	Info    Ref
	Trailer Dict
}

// Open parses a PDF from r into a DocumentModel.
func Open(r io.Reader) (*DocumentModel, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(data[:min(1024, len(data))], []byte("%PDF-")) {
		return nil, errors.New("pdf: not a PDF file")
	}
	startxref := bytes.LastIndex(data, []byte("startxref"))
	if startxref < 0 {
		return nil, errors.New("pdf: missing startxref")
	}
	rest := data[startxref+len("startxref"):]
	rest = bytes.TrimLeft(rest, " \t\r\n")
	lineEnd := bytes.IndexAny(rest, "\r\n")
	if lineEnd < 0 {
		return nil, errors.New("pdf: bad startxref")
	}
	xrefOffset, err := strconv.Atoi(string(bytes.TrimSpace(rest[:lineEnd])))
	if err != nil {
		return nil, fmt.Errorf("pdf: bad startxref offset: %w", err)
	}
	objects := map[int]Object{}
	trailer, err := parseXRefSection(data, xrefOffset, objects)
	if err != nil {
		return nil, err
	}
	root, _ := trailer["Root"].(Ref)
	info, _ := trailer["Info"].(Ref)
	return &DocumentModel{
		Objects: objects,
		Root:    root,
		Info:    info,
		Trailer: trailer,
	}, nil
}

func parseXRefSection(data []byte, offset int, objects map[int]Object) (Dict, error) {
	if offset < 0 || offset >= len(data) {
		return nil, errors.New("pdf: xref offset out of range")
	}
	s := data[offset:]
	if bytes.HasPrefix(s, []byte("xref")) {
		return parseClassicXRef(data, offset, objects)
	}
	// xref stream
	obj, id, err := parseIndirectAt(data, offset)
	if err != nil {
		return nil, err
	}
	st, ok := obj.(Stream)
	if !ok {
		return nil, errors.New("pdf: expected xref stream")
	}
	objects[id] = st
	return parseXRefStream(data, st, objects)
}

func parseClassicXRef(data []byte, offset int, objects map[int]Object) (Dict, error) {
	p := offset
	if !bytes.HasPrefix(data[p:], []byte("xref")) {
		return nil, errors.New("pdf: expected xref")
	}
	p += 4
	skipWSBytes := func() {
		for p < len(data) && (data[p] == ' ' || data[p] == '\t' || data[p] == '\r' || data[p] == '\n') {
			p++
		}
	}
	skipWSBytes()
	entries := []struct{ id, offset, gen int; free bool }{}
	for {
		skipWSBytes()
		if p >= len(data) {
			break
		}
		if bytes.HasPrefix(data[p:], []byte("trailer")) {
			break
		}
		// subsection header: start count
		line := readLine(data, &p)
		parts := strings.Fields(line)
		if len(parts) != 2 {
			return nil, fmt.Errorf("pdf: bad xref subsection %q", line)
		}
		start, _ := strconv.Atoi(parts[0])
		count, _ := strconv.Atoi(parts[1])
		for i := 0; i < count; i++ {
			line = readLine(data, &p)
			fields := strings.Fields(line)
			if len(fields) < 3 {
				return nil, fmt.Errorf("pdf: bad xref entry %q", line)
			}
			off, _ := strconv.Atoi(fields[0])
			gen, _ := strconv.Atoi(fields[1])
			free := fields[2] == "f"
			entries = append(entries, struct {
				id, offset, gen int
				free            bool
			}{start + i, off, gen, free})
		}
	}
	skipWSBytes()
	if !bytes.HasPrefix(data[p:], []byte("trailer")) {
		return nil, errors.New("pdf: missing trailer")
	}
	p += len("trailer")
	skipWSBytes()
	trailerObj, err := parseObject(data, &p)
	if err != nil {
		return nil, err
	}
	trailer, ok := trailerObj.(Dict)
	if !ok {
		return nil, errors.New("pdf: trailer is not a dict")
	}
	for _, e := range entries {
		if e.free || e.id == 0 {
			continue
		}
		if _, exists := objects[e.id]; exists {
			continue
		}
		obj, _, err := parseIndirectAt(data, e.offset)
		if err != nil {
			return nil, fmt.Errorf("pdf: object %d: %w", e.id, err)
		}
		objects[e.id] = obj
	}
	if prev, ok := trailer["Prev"].(Number); ok {
		prevTrailer, err := parseXRefSection(data, int(prev), objects)
		if err != nil {
			return nil, err
		}
		// merge missing keys from older trailer
		for k, v := range prevTrailer {
			if _, ok := trailer[k]; !ok {
				trailer[k] = v
			}
		}
	}
	return trailer, nil
}

func parseXRefStream(data []byte, st Stream, objects map[int]Object) (Dict, error) {
	raw := st.Data
	if filt, ok := st.Dict["Filter"].(Name); ok && filt == "FlateDecode" {
		zr, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		raw, err = io.ReadAll(zr)
		if err != nil {
			return nil, err
		}
	}
	size, _ := st.Dict["Size"].(Number)
	wArr, _ := st.Dict["W"].(Array)
	if len(wArr) != 3 {
		return nil, errors.New("pdf: bad xref stream W")
	}
	w0 := int(wArr[0].(Number))
	w1 := int(wArr[1].(Number))
	w2 := int(wArr[2].(Number))
	entryLen := w0 + w1 + w2
	index := []int{0, int(size)}
	if idx, ok := st.Dict["Index"].(Array); ok && len(idx) >= 2 {
		index = index[:0]
		for i := 0; i+1 < len(idx); i += 2 {
			start := int(idx[i].(Number))
			count := int(idx[i+1].(Number))
			index = append(index, start, count)
		}
	}
	pos := 0
	readN := func(n int) int {
		v := 0
		for i := 0; i < n; i++ {
			v = (v << 8) | int(raw[pos])
			pos++
		}
		return v
	}
	for i := 0; i+1 < len(index); i += 2 {
		start, count := index[i], index[i+1]
		for j := 0; j < count; j++ {
			if pos+entryLen > len(raw) {
				return nil, errors.New("pdf: truncated xref stream")
			}
			var typ, field1, field2 int
			if w0 == 0 {
				typ = 1
			} else {
				typ = readN(w0)
			}
			field1 = readN(w1)
			field2 = readN(w2)
			id := start + j
			if id == 0 {
				continue
			}
			if _, exists := objects[id]; exists {
				continue
			}
			switch typ {
			case 0:
				// free
			case 1:
				obj, _, err := parseIndirectAt(data, field1)
				if err != nil {
					return nil, err
				}
				objects[id] = obj
			case 2:
				// object stream: field1 = stream obj, field2 = index — resolve later
				objects[id] = objStreamRef{streamID: field1, index: field2}
			}
		}
	}
	// inflate object streams
	if err := resolveObjStreams(data, objects); err != nil {
		return nil, err
	}
	trailer := Dict{}
	for k, v := range st.Dict {
		if k == "Type" || k == "W" || k == "Index" || k == "Length" || k == "Filter" || k == "DecodeParms" {
			continue
		}
		trailer[k] = v
	}
	if prev, ok := trailer["Prev"].(Number); ok {
		prevTrailer, err := parseXRefSection(data, int(prev), objects)
		if err != nil {
			return nil, err
		}
		for k, v := range prevTrailer {
			if _, ok := trailer[k]; !ok {
				trailer[k] = v
			}
		}
	}
	return trailer, nil
}

type objStreamRef struct {
	streamID int
	index    int
}

func (objStreamRef) WritePDF(w io.Writer) error {
	return errors.New("pdf: unresolved object stream ref")
}

func resolveObjStreams(data []byte, objects map[int]Object) error {
	// Load referenced object streams
	for id, obj := range objects {
		osr, ok := obj.(objStreamRef)
		if !ok {
			continue
		}
		stObj, ok := objects[osr.streamID]
		if !ok {
			var err error
			stObj, _, err = findObjectByID(data, osr.streamID)
			if err != nil {
				return err
			}
			objects[osr.streamID] = stObj
		}
		st, ok := stObj.(Stream)
		if !ok {
			return fmt.Errorf("pdf: object %d not a stream", osr.streamID)
		}
		decoded, err := decodeStream(st)
		if err != nil {
			return err
		}
		n, _ := st.Dict["N"].(Number)
		first, _ := st.Dict["First"].(Number)
		ids := make([]int, int(n))
		p := 0
		for i := 0; i < int(n); i++ {
			skipWSBuf(decoded, &p)
			ids[i] = readInt(decoded, &p)
			skipWSBuf(decoded, &p)
			_ = readInt(decoded, &p) // offset unused; we use First + sequential parse
		}
		p = int(first)
		for i := 0; i < int(n); i++ {
			skipWSBuf(decoded, &p)
			o, err := parseObject(decoded, &p)
			if err != nil {
				return err
			}
			if _, exists := objects[ids[i]]; !exists || isObjStreamRef(objects[ids[i]]) {
				objects[ids[i]] = o
			}
		}
		_ = id
	}
	return nil
}

func isObjStreamRef(o Object) bool {
	_, ok := o.(objStreamRef)
	return ok
}

func findObjectByID(data []byte, id int) (Object, int, error) {
	marker := []byte(fmt.Sprintf("\n%d 0 obj", id))
	idx := bytes.Index(data, marker)
	if idx < 0 {
		marker = []byte(fmt.Sprintf("%d 0 obj", id))
		idx = bytes.Index(data, marker)
		if idx < 0 {
			return nil, 0, fmt.Errorf("pdf: object %d not found", id)
		}
	} else {
		idx++ // skip leading \n
	}
	return parseIndirectAt(data, idx)
}

func decodeStream(st Stream) ([]byte, error) {
	data := st.Data
	switch f := st.Dict["Filter"].(type) {
	case Name:
		if f == "FlateDecode" {
			zr, err := zlib.NewReader(bytes.NewReader(data))
			if err != nil {
				return nil, err
			}
			defer zr.Close()
			return io.ReadAll(zr)
		}
	case Array:
		// only support single Flate for now
		if len(f) == 1 {
			if n, ok := f[0].(Name); ok && n == "FlateDecode" {
				zr, err := zlib.NewReader(bytes.NewReader(data))
				if err != nil {
					return nil, err
				}
				defer zr.Close()
				return io.ReadAll(zr)
			}
		}
	}
	return data, nil
}

func parseIndirectAt(data []byte, offset int) (Object, int, error) {
	p := offset
	skipWSBuf(data, &p)
	id := readInt(data, &p)
	skipWSBuf(data, &p)
	_ = readInt(data, &p) // gen
	skipWSBuf(data, &p)
	if !bytes.HasPrefix(data[p:], []byte("obj")) {
		return nil, 0, fmt.Errorf("pdf: expected obj at %d", offset)
	}
	p += 3
	skipWSBuf(data, &p)
	obj, err := parseObject(data, &p)
	if err != nil {
		return nil, 0, err
	}
	skipWSBuf(data, &p)
	if bytes.HasPrefix(data[p:], []byte("endobj")) {
		p += 6
	}
	return obj, id, nil
}

func parseObject(data []byte, p *int) (Object, error) {
	skipWSBuf(data, p)
	if *p >= len(data) {
		return nil, io.ErrUnexpectedEOF
	}
	switch data[*p] {
	case 'n':
		if bytes.HasPrefix(data[*p:], []byte("null")) {
			*p += 4
			return Null{}, nil
		}
	case 't':
		if bytes.HasPrefix(data[*p:], []byte("true")) {
			*p += 4
			return Boolean(true), nil
		}
	case 'f':
		if bytes.HasPrefix(data[*p:], []byte("false")) {
			*p += 5
			return Boolean(false), nil
		}
	case '/':
		*p++
		start := *p
		for *p < len(data) && !isDelim(data[*p]) && data[*p] > 32 {
			*p++
		}
		return Name(decodeName(string(data[start:*p]))), nil
	case '(':
		s, err := parseLiteralString(data, p)
		return String(s), err
	case '<':
		if *p+1 < len(data) && data[*p+1] == '<' {
			return parseDict(data, p)
		}
		return parseHexString(data, p)
	case '[':
		return parseArray(data, p)
	case '+', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '.':
		return parseNumberOrRef(data, p)
	}
	return nil, fmt.Errorf("pdf: unexpected byte %q at %d", data[*p], *p)
}

func parseDict(data []byte, p *int) (Object, error) {
	*p += 2 // <<
	d := Dict{}
	for {
		skipWSBuf(data, p)
		if *p+1 < len(data) && data[*p] == '>' && data[*p+1] == '>' {
			*p += 2
			skipWSBuf(data, p)
			if bytes.HasPrefix(data[*p:], []byte("stream")) {
				return parseStreamAfterDict(data, p, d)
			}
			return d, nil
		}
		keyObj, err := parseObject(data, p)
		if err != nil {
			return nil, err
		}
		key, ok := keyObj.(Name)
		if !ok {
			return nil, errors.New("pdf: dict key not a name")
		}
		val, err := parseObject(data, p)
		if err != nil {
			return nil, err
		}
		d[key] = val
	}
}

func parseStreamAfterDict(data []byte, p *int, d Dict) (Object, error) {
	*p += len("stream")
	if *p < len(data) && data[*p] == '\r' {
		*p++
	}
	if *p < len(data) && data[*p] == '\n' {
		*p++
	}
	length := 0
	if n, ok := d["Length"].(Number); ok {
		length = int(n)
	} else if ref, ok := d["Length"].(Ref); ok {
		// Length as indirect — best effort scan until endstream
		_ = ref
		length = -1
	}
	start := *p
	var raw []byte
	if length >= 0 && start+length <= len(data) {
		raw = data[start : start+length]
		*p = start + length
		skipWSBuf(data, p)
		if bytes.HasPrefix(data[*p:], []byte("endstream")) {
			*p += len("endstream")
		}
	} else {
		idx := bytes.Index(data[start:], []byte("endstream"))
		if idx < 0 {
			return nil, errors.New("pdf: missing endstream")
		}
		raw = bytes.TrimRight(data[start:start+idx], "\r\n")
		*p = start + idx + len("endstream")
	}
	return Stream{Dict: d, Data: append([]byte(nil), raw...)}, nil
}

func parseArray(data []byte, p *int) (Object, error) {
	*p++ // [
	var a Array
	for {
		skipWSBuf(data, p)
		if *p < len(data) && data[*p] == ']' {
			*p++
			return a, nil
		}
		o, err := parseObject(data, p)
		if err != nil {
			return nil, err
		}
		a = append(a, o)
	}
}

func parseNumberOrRef(data []byte, p *int) (Object, error) {
	start := *p
	num1 := readNumberToken(data, p)
	save := *p
	skipWSBuf(data, p)
	// look ahead for "int int R"
	if *p < len(data) && (data[*p] == '+' || data[*p] == '-' || (data[*p] >= '0' && data[*p] <= '9')) {
		num2Start := *p
		_ = readNumberToken(data, p)
		skipWSBuf(data, p)
		if *p < len(data) && data[*p] == 'R' {
			// ensure not part of longer token
			if *p+1 >= len(data) || isDelim(data[*p+1]) || data[*p+1] <= 32 {
				*p++
				id, _ := strconv.Atoi(num1)
				gen, _ := strconv.Atoi(string(data[num2Start : num2Start+len(readNumberTokenAt(data, num2Start))]))
				// re-parse gen cleanly
				pp := num2Start
				gen = readInt(data, &pp)
				return Ref{ID: id, Gen: gen}, nil
			}
		}
	}
	*p = save
	f, err := strconv.ParseFloat(num1, 64)
	if err != nil {
		return nil, fmt.Errorf("pdf: bad number %q at %d", data[start:*p], start)
	}
	return Number(f), nil
}

func readNumberTokenAt(data []byte, p int) string {
	pp := p
	return readNumberToken(data, &pp)
}

func parseLiteralString(data []byte, p *int) (string, error) {
	*p++ // (
	var out bytes.Buffer
	depth := 1
	for *p < len(data) {
		c := data[*p]
		*p++
		if c == '\\' {
			if *p >= len(data) {
				break
			}
			esc := data[*p]
			*p++
			switch esc {
			case 'n':
				out.WriteByte('\n')
			case 'r':
				out.WriteByte('\r')
			case 't':
				out.WriteByte('\t')
			case 'b':
				out.WriteByte('\b')
			case 'f':
				out.WriteByte('\f')
			case '(', ')', '\\':
				out.WriteByte(esc)
			case '\r':
				if *p < len(data) && data[*p] == '\n' {
					*p++
				}
			case '\n':
				// line continuation
			default:
				if esc >= '0' && esc <= '7' {
					val := int(esc - '0')
					for i := 0; i < 2 && *p < len(data) && data[*p] >= '0' && data[*p] <= '7'; i++ {
						val = val*8 + int(data[*p]-'0')
						*p++
					}
					out.WriteByte(byte(val))
				} else {
					out.WriteByte(esc)
				}
			}
			continue
		}
		if c == '(' {
			depth++
			out.WriteByte(c)
			continue
		}
		if c == ')' {
			depth--
			if depth == 0 {
				return out.String(), nil
			}
			out.WriteByte(c)
			continue
		}
		out.WriteByte(c)
	}
	return "", errors.New("pdf: unterminated string")
}

func parseHexString(data []byte, p *int) (Object, error) {
	*p++ // <
	var hex []byte
	for *p < len(data) && data[*p] != '>' {
		c := data[*p]
		*p++
		if c <= 32 {
			continue
		}
		hex = append(hex, c)
	}
	if *p < len(data) && data[*p] == '>' {
		*p++
	}
	if len(hex)%2 == 1 {
		hex = append(hex, '0')
	}
	out := make([]byte, len(hex)/2)
	for i := 0; i < len(out); i++ {
		v, err := strconv.ParseUint(string(hex[i*2:i*2+2]), 16, 8)
		if err != nil {
			return nil, err
		}
		out[i] = byte(v)
	}
	return HexString(out), nil
}

func decodeName(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '#' && i+2 < len(s) {
			v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
			if err == nil {
				out.WriteByte(byte(v))
				i += 2
				continue
			}
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

func skipWSBuf(data []byte, p *int) {
	for *p < len(data) {
		c := data[*p]
		if c == '%' {
			for *p < len(data) && data[*p] != '\n' && data[*p] != '\r' {
				*p++
			}
			continue
		}
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' {
			*p++
			continue
		}
		break
	}
}

func isDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func readLine(data []byte, p *int) string {
	start := *p
	for *p < len(data) && data[*p] != '\n' && data[*p] != '\r' {
		*p++
	}
	line := string(data[start:*p])
	if *p < len(data) && data[*p] == '\r' {
		*p++
	}
	if *p < len(data) && data[*p] == '\n' {
		*p++
	}
	return line
}

func readInt(data []byte, p *int) int {
	tok := readNumberToken(data, p)
	n, _ := strconv.Atoi(tok)
	return n
}

func readNumberToken(data []byte, p *int) string {
	start := *p
	if *p < len(data) && (data[*p] == '+' || data[*p] == '-') {
		*p++
	}
	for *p < len(data) && data[*p] >= '0' && data[*p] <= '9' {
		*p++
	}
	if *p < len(data) && data[*p] == '.' {
		*p++
		for *p < len(data) && data[*p] >= '0' && data[*p] <= '9' {
			*p++
		}
	}
	return string(data[start:*p])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Resolve follows refs in the document model.
func (d *DocumentModel) Resolve(obj Object) Object {
	for i := 0; i < 32; i++ {
		ref, ok := obj.(Ref)
		if !ok {
			return obj
		}
		obj = d.Objects[ref.ID]
		if obj == nil {
			return nil
		}
	}
	return obj
}

// PageRefs returns page object refs in order.
func (d *DocumentModel) PageRefs() ([]Ref, error) {
	root := d.Resolve(d.Root)
	catalog, ok := root.(Dict)
	if !ok {
		return nil, errors.New("pdf: bad catalog")
	}
	pages := d.Resolve(catalog["Pages"])
	pagesDict, ok := pages.(Dict)
	if !ok {
		return nil, errors.New("pdf: bad pages dict")
	}
	var out []Ref
	var walkRef func(Ref) error
	walkRef = func(ref Ref) error {
		node := d.Resolve(ref)
		dict, ok := node.(Dict)
		if !ok {
			return errors.New("pdf: page tree node not dict")
		}
		typ, _ := dict["Type"].(Name)
		if typ == "Page" {
			out = append(out, ref)
			return nil
		}
		kids, _ := dict["Kids"].(Array)
		for _, k := range kids {
			kr, ok := k.(Ref)
			if !ok {
				continue
			}
			if err := walkRef(kr); err != nil {
				return err
			}
		}
		return nil
	}
	// pages dict itself may be direct; walk its kids
	kids, _ := pagesDict["Kids"].(Array)
	for _, k := range kids {
		if ref, ok := k.(Ref); ok {
			if err := walkRef(ref); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// GetPageDict returns the page dictionary for a page ref.
func (d *DocumentModel) GetPageDict(ref Ref) (Dict, error) {
	obj := d.Resolve(ref)
	dict, ok := obj.(Dict)
	if !ok {
		return nil, errors.New("pdf: page is not a dict")
	}
	return dict, nil
}
