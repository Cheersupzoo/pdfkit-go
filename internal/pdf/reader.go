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

// xrefEntry describes where an object lives in the file (lazy; not yet parsed).
type xrefEntry struct {
	typ        int // 0=free, 1=uncompressed, 2=compressed (objstm)
	offset     int // typ1: file offset; typ2: object stream id
	genOrIndex int // typ1: generation; typ2: index within object stream
}

// DocumentModel is a lazily-parsed PDF suitable for page extraction / merge.
// Objects are loaded from ra on demand via Get/Resolve.
type DocumentModel struct {
	ra     io.ReaderAt
	size   int64
	closer io.Closer

	xref  map[int]xrefEntry
	cache map[int]Object

	// objStmCache holds fully decoded object-stream contents keyed by stream object id.
	objStmCache map[int][]Object

	Root    Ref
	Info    Ref
	Trailer Dict

	sortedOff []int // sorted typ1 offsets for object bounds; built lazily
}

// Open parses a PDF from r into a DocumentModel.
// Non-seekable readers are spooled to a temporary file. The file is not fully
// decoded up front — only the xref index is built; objects load on demand.
func Open(r io.Reader) (*DocumentModel, error) {
	ra, size, closer, err := readerAtOf(r)
	if err != nil {
		return nil, err
	}
	return OpenReaderAt(ra, size, closer)
}

// Close releases any owned underlying reader (temp spool or owned file).
func (d *DocumentModel) Close() error {
	if d == nil || d.closer == nil {
		return nil
	}
	err := d.closer.Close()
	d.closer = nil
	return err
}

func (d *DocumentModel) initFromReaderAt() error {
	headLen := int64(1024)
	if headLen > d.size {
		headLen = d.size
	}
	head, err := d.readRange(0, int(headLen))
	if err != nil {
		return err
	}
	if !bytes.Contains(head, []byte("%PDF-")) {
		return errors.New("pdf: not a PDF file")
	}

	tailLen := int64(8192)
	if tailLen > d.size {
		tailLen = d.size
	}
	tailOff := d.size - tailLen
	tail, err := d.readRange(int(tailOff), int(tailLen))
	if err != nil {
		return err
	}
	idx := bytes.LastIndex(tail, []byte("startxref"))
	if idx < 0 {
		return errors.New("pdf: missing startxref")
	}
	rest := tail[idx+len("startxref"):]
	rest = bytes.TrimLeft(rest, " \t\r\n")
	lineEnd := bytes.IndexAny(rest, "\r\n")
	if lineEnd < 0 {
		return errors.New("pdf: bad startxref")
	}
	xrefOffset, err := strconv.Atoi(string(bytes.TrimSpace(rest[:lineEnd])))
	if err != nil {
		return fmt.Errorf("pdf: bad startxref offset: %w", err)
	}

	trailer, err := d.parseXRefSection(xrefOffset, map[int]bool{})
	if err != nil {
		return err
	}
	root, _ := trailer["Root"].(Ref)
	info, _ := trailer["Info"].(Ref)
	d.Root = root
	d.Info = info
	d.Trailer = trailer
	return nil
}

func (d *DocumentModel) readRange(offset, length int) ([]byte, error) {
	if offset < 0 || length < 0 {
		return nil, errors.New("pdf: invalid read range")
	}
	if int64(offset) >= d.size {
		return nil, io.EOF
	}
	if int64(offset+length) > d.size {
		length = int(d.size) - offset
	}
	buf := make([]byte, length)
	total := 0
	for total < length {
		n, err := d.ra.ReadAt(buf[total:], int64(offset+total))
		total += n
		if err == io.EOF {
			break
		}
		if err != nil {
			return buf[:total], err
		}
	}
	return buf[:total], nil
}

func (d *DocumentModel) parseXRefSection(offset int, visited map[int]bool) (Dict, error) {
	if offset < 0 || int64(offset) >= d.size {
		return nil, errors.New("pdf: xref offset out of range")
	}
	if visited[offset] {
		return nil, errors.New("pdf: cyclic xref Prev")
	}
	visited[offset] = true

	probe, err := d.readRange(offset, 16)
	if err != nil && len(probe) == 0 {
		return nil, err
	}
	if bytes.HasPrefix(probe, []byte("xref")) {
		return d.parseClassicXRef(offset, visited)
	}
	obj, id, err := d.parseIndirectAtOffset(offset)
	if err != nil {
		return nil, err
	}
	st, ok := obj.(Stream)
	if !ok {
		return nil, errors.New("pdf: expected xref stream")
	}
	d.cache[id] = st
	return d.parseXRefStream(st, visited)
}

func (d *DocumentModel) parseClassicXRef(offset int, visited map[int]bool) (Dict, error) {
	// Xref tables are small relative to content; read a generous window.
	window := 4 << 20
	if int64(offset+window) > d.size {
		window = int(d.size) - offset
	}
	data, err := d.readRange(offset, window)
	if err != nil && len(data) == 0 {
		return nil, err
	}
	p := 0
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
	type xrefRow struct {
		id, offset, gen int
		free           bool
	}
	entries := []xrefRow{}
	for {
		skipWSBytes()
		if p >= len(data) {
			break
		}
		if bytes.HasPrefix(data[p:], []byte("trailer")) {
			break
		}
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
			entries = append(entries, xrefRow{start + i, off, gen, free})
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
		if _, exists := d.xref[e.id]; exists {
			continue
		}
		d.xref[e.id] = xrefEntry{typ: 1, offset: e.offset, genOrIndex: e.gen}
	}
	if prev, ok := trailer["Prev"].(Number); ok {
		prevTrailer, err := d.parseXRefSection(int(prev), visited)
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

func (d *DocumentModel) parseXRefStream(st Stream, visited map[int]bool) (Dict, error) {
	raw, err := decodeStream(st)
	if err != nil {
		return nil, fmt.Errorf("pdf: xref stream decode: %w", err)
	}
	size, _ := st.Dict["Size"].(Number)
	wArr, ok := st.Dict["W"].(Array)
	if !ok || len(wArr) != 3 {
		return nil, errors.New("pdf: bad xref stream W")
	}
	w0, ok0 := wArr[0].(Number)
	w1, ok1 := wArr[1].(Number)
	w2, ok2 := wArr[2].(Number)
	if !ok0 || !ok1 || !ok2 {
		return nil, errors.New("pdf: bad xref stream W types")
	}
	wi0, wi1, wi2 := int(w0), int(w1), int(w2)
	entryLen := wi0 + wi1 + wi2
	index := []int{0, int(size)}
	if idx, ok := st.Dict["Index"].(Array); ok && len(idx) >= 2 {
		index = index[:0]
		for i := 0; i+1 < len(idx); i += 2 {
			startN, okS := idx[i].(Number)
			countN, okC := idx[i+1].(Number)
			if !okS || !okC {
				return nil, errors.New("pdf: bad xref stream Index")
			}
			index = append(index, int(startN), int(countN))
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
			if wi0 == 0 {
				typ = 1
			} else {
				typ = readN(wi0)
			}
			field1 = readN(wi1)
			field2 = readN(wi2)
			id := start + j
			if id == 0 {
				continue
			}
			if _, exists := d.xref[id]; exists {
				continue
			}
			switch typ {
			case 0:
				// free
			case 1:
				d.xref[id] = xrefEntry{typ: 1, offset: field1, genOrIndex: field2}
			case 2:
				d.xref[id] = xrefEntry{typ: 2, offset: field1, genOrIndex: field2}
			}
		}
	}
	trailer := Dict{}
	for k, v := range st.Dict {
		if k == "Type" || k == "W" || k == "Index" || k == "Length" || k == "Filter" || k == "DecodeParms" {
			continue
		}
		trailer[k] = v
	}
	if prev, ok := trailer["Prev"].(Number); ok {
		prevTrailer, err := d.parseXRefSection(int(prev), visited)
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

// parseIndirectAtOffset reads and parses one indirect object starting at file offset.
func (d *DocumentModel) parseIndirectAtOffset(offset int) (Object, int, error) {
	chunk := 64 << 10
	for {
		end := offset + chunk
		if int64(end) > d.size {
			end = int(d.size)
		}
		data, err := d.readRange(offset, end-offset)
		if err != nil && len(data) == 0 {
			return nil, 0, err
		}
		obj, id, err := parseIndirectAt(data, 0)
		if err == nil {
			return obj, id, nil
		}
		if !errors.Is(err, errTruncatedStream) && !errors.Is(err, io.ErrUnexpectedEOF) {
			// Keep expanding on truncated streams; other errors may still be from a short buffer.
			if int64(end) >= d.size {
				return nil, 0, err
			}
		}
		if int64(end) >= d.size {
			return nil, 0, err
		}
		chunk *= 2
		if chunk > 64<<20 {
			chunk = 64 << 20
		}
	}
}

// streamLength resolves /Length to a concrete byte count when possible.
func (d *DocumentModel) streamLength(dict Dict) (int, bool, error) {
	switch v := dict["Length"].(type) {
	case Number:
		n := int(v)
		if n < 0 {
			return 0, false, errors.New("pdf: negative stream Length")
		}
		return n, true, nil
	case Ref:
		obj, err := d.Get(v.ID)
		if err != nil {
			return 0, false, err
		}
		n, ok := obj.(Number)
		if !ok {
			return 0, false, fmt.Errorf("pdf: stream Length ref %d is %T, not number", v.ID, obj)
		}
		if int(n) < 0 {
			return 0, false, errors.New("pdf: negative stream Length")
		}
		return int(n), true, nil
	default:
		return 0, false, nil
	}
}

// ensureStreamComplete re-reads stream bytes using a resolved /Length so FontFile2
// and other binary streams are not truncated by endstream false-matches.
func (d *DocumentModel) ensureStreamComplete(objOffset int, st Stream) (Stream, error) {
	_, lengthWasRef := st.Dict["Length"].(Ref)
	length, ok, err := d.streamLength(st.Dict)
	if err != nil {
		return st, err
	}
	if !ok {
		return st, nil
	}
	st.Dict["Length"] = Number(length)
	// Always re-read when Length was indirect: the initial parse may have scanned for
	// "endstream" inside binary font/image data and truncated incorrectly.
	if !lengthWasRef && len(st.Data) == length {
		return st, nil
	}
	return d.rereadStreamExact(objOffset, st.Dict, length)
}

func (d *DocumentModel) rereadStreamExact(objOffset int, dict Dict, length int) (Stream, error) {
	// Copy dict and force direct Length so parseStreamAfterDict takes the exact path.
	nd := Dict{}
	for k, v := range dict {
		nd[k] = v
	}
	nd["Length"] = Number(length)

	chunk := length + 64<<10
	if chunk < 64<<10 {
		chunk = 64 << 10
	}
	for {
		end := objOffset + chunk
		if int64(end) > d.size {
			end = int(d.size)
		}
		data, err := d.readRange(objOffset, end-objOffset)
		if err != nil && len(data) == 0 {
			return Stream{}, err
		}
		// Locate the stream keyword after the object header/dict.
		streamAt := findStreamKeyword(data)
		if streamAt < 0 {
			if int64(end) >= d.size {
				return Stream{}, errors.New("pdf: missing stream keyword")
			}
			chunk *= 2
			continue
		}
		p := streamAt
		obj, err := parseStreamAfterDict(data, &p, nd)
		if errors.Is(err, errTruncatedStream) {
			if int64(end) >= d.size {
				return Stream{}, fmt.Errorf("pdf: stream Length %d exceeds file", length)
			}
			chunk *= 2
			continue
		}
		if err != nil {
			return Stream{}, err
		}
		st := obj.(Stream)
		st.Dict = nd
		if len(st.Data) != length {
			return Stream{}, fmt.Errorf("pdf: stream data length %d != /Length %d", len(st.Data), length)
		}
		return st, nil
	}
}

func findStreamKeyword(data []byte) int {
	// Match "stream" as a keyword (not inside names/strings best-effort via dict parse path).
	// Objects look like: N G obj << ... >> stream
	idx := 0
	for {
		i := bytes.Index(data[idx:], []byte("stream"))
		if i < 0 {
			return -1
		}
		i += idx
		// Previous non-space should be '>' from '>>' or whitespace after dict.
		j := i - 1
		for j >= 0 && (data[j] == ' ' || data[j] == '\t' || data[j] == '\r' || data[j] == '\n') {
			j--
		}
		if j >= 0 && data[j] == '>' {
			// Ensure not "endstream"
			if i >= 3 && bytes.Equal(data[i-3:i], []byte("end")) {
				idx = i + 6
				continue
			}
			return i
		}
		idx = i + 6
	}
}

// Get loads object id on demand (cached).
func (d *DocumentModel) Get(id int) (Object, error) {
	if id <= 0 {
		return nil, nil
	}
	if o, ok := d.cache[id]; ok {
		return o, nil
	}
	ent, ok := d.xref[id]
	if !ok {
		return nil, fmt.Errorf("pdf: missing object %d", id)
	}
	switch ent.typ {
	case 0:
		return nil, nil
	case 1:
		end := d.objectEnd(ent.offset)
		length := end - ent.offset
		if length < 256 {
			length = 256
		}
		if int64(ent.offset+length) > d.size {
			length = int(d.size) - ent.offset
		}
		data, err := d.readRange(ent.offset, length)
		if err != nil && len(data) == 0 {
			return nil, err
		}
		obj, _, err := parseIndirectAt(data, 0)
		if err != nil {
			obj, _, err = d.parseIndirectAtOffset(ent.offset)
			if err != nil {
				return nil, fmt.Errorf("pdf: object %d at %d: %w", id, ent.offset, err)
			}
		}
		if st, ok := obj.(Stream); ok {
			st, err = d.ensureStreamComplete(ent.offset, st)
			if err != nil {
				return nil, fmt.Errorf("pdf: object %d stream: %w", id, err)
			}
			obj = st
		}
		d.cache[id] = obj
		return obj, nil
	case 2:
		objs, err := d.loadObjStream(ent.offset)
		if err != nil {
			return nil, err
		}
		if ent.genOrIndex < 0 || ent.genOrIndex >= len(objs) {
			return nil, fmt.Errorf("pdf: object stream index %d out of range", ent.genOrIndex)
		}
		obj := objs[ent.genOrIndex]
		d.cache[id] = obj
		return obj, nil
	default:
		return nil, fmt.Errorf("pdf: bad xref type %d for object %d", ent.typ, id)
	}
}

func (d *DocumentModel) ensureSortedOffsets() {
	if d.sortedOff != nil {
		return
	}
	offs := make([]int, 0, len(d.xref))
	for _, e := range d.xref {
		if e.typ == 1 {
			offs = append(offs, e.offset)
		}
	}
	for i := 1; i < len(offs); i++ {
		v := offs[i]
		j := i
		for j > 0 && offs[j-1] > v {
			offs[j] = offs[j-1]
			j--
		}
		offs[j] = v
	}
	d.sortedOff = offs
}

func (d *DocumentModel) objectEnd(start int) int {
	d.ensureSortedOffsets()
	lo, hi := 0, len(d.sortedOff)
	for lo < hi {
		mid := (lo + hi) / 2
		if d.sortedOff[mid] <= start {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(d.sortedOff) {
		return d.sortedOff[lo]
	}
	return int(d.size)
}

func (d *DocumentModel) loadObjStream(streamID int) ([]Object, error) {
	if d.objStmCache == nil {
		d.objStmCache = map[int][]Object{}
	}
	if objs, ok := d.objStmCache[streamID]; ok {
		return objs, nil
	}
	stObj, err := d.Get(streamID)
	if err != nil {
		return nil, err
	}
	st, ok := stObj.(Stream)
	if !ok {
		return nil, fmt.Errorf("pdf: object %d not a stream", streamID)
	}
	decoded, err := decodeStream(st)
	if err != nil {
		return nil, err
	}
	nNum, _ := st.Dict["N"].(Number)
	firstNum, _ := st.Dict["First"].(Number)
	n := int(nNum)
	if n < 0 {
		return nil, errors.New("pdf: negative object stream N")
	}
	// Cap absurd allocations from malicious /N.
	const maxObjStreamN = 1_000_000
	if n > maxObjStreamN {
		return nil, fmt.Errorf("pdf: object stream N too large: %d", n)
	}
	ids := make([]int, n)
	p := 0
	for i := 0; i < n; i++ {
		skipWSBuf(decoded, &p)
		ids[i] = readInt(decoded, &p)
		skipWSBuf(decoded, &p)
		_ = readInt(decoded, &p)
	}
	p = int(firstNum)
	objs := make([]Object, n)
	for i := 0; i < n; i++ {
		skipWSBuf(decoded, &p)
		o, err := parseObject(decoded, &p)
		if err != nil {
			return nil, err
		}
		objs[i] = o
		// Also cache by declared id when not yet present.
		if _, ok := d.cache[ids[i]]; !ok {
			d.cache[ids[i]] = o
		}
	}
	d.objStmCache[streamID] = objs
	return objs, nil
}

func decodeStream(st Stream) ([]byte, error) {
	data := st.Data
	filters, parms := normalizeFilters(st.Dict)
	for i, f := range filters {
		switch f {
		case "FlateDecode", "Fl":
			zr, err := zlib.NewReader(bytes.NewReader(data))
			if err != nil {
				return nil, err
			}
			inflated, err := io.ReadAll(zr)
			zr.Close()
			if err != nil {
				return nil, err
			}
			data, err = applyPredictor(inflated, parms[i])
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("pdf: unsupported filter %s", f)
		}
	}
	return data, nil
}

func normalizeFilters(d Dict) ([]Name, []Dict) {
	var filters []Name
	switch f := d["Filter"].(type) {
	case Name:
		filters = []Name{f}
	case Array:
		for _, v := range f {
			if n, ok := v.(Name); ok {
				filters = append(filters, n)
			}
		}
	}
	parms := make([]Dict, len(filters))
	switch p := d["DecodeParms"].(type) {
	case Dict:
		if len(filters) == 1 {
			parms[0] = p
		}
	case Array:
		for i := 0; i < len(filters) && i < len(p); i++ {
			if dp, ok := p[i].(Dict); ok {
				parms[i] = dp
			}
		}
	}
	return filters, parms
}

// applyPredictor undoes TIFF/PNG predictors used with FlateDecode (common on xref streams).
func applyPredictor(data []byte, parms Dict) ([]byte, error) {
	if parms == nil {
		return data, nil
	}
	predictor := 1
	if n, ok := parms["Predictor"].(Number); ok {
		predictor = int(n)
	}
	if predictor <= 1 {
		return data, nil
	}
	columns := 1
	if n, ok := parms["Columns"].(Number); ok && int(n) > 0 {
		columns = int(n)
	}
	colors := 1
	if n, ok := parms["Colors"].(Number); ok && int(n) > 0 {
		colors = int(n)
	}
	bpc := 8
	if n, ok := parms["BitsPerComponent"].(Number); ok && int(n) > 0 {
		bpc = int(n)
	}
	// bytes per pixel / sample group
	bpp := (colors*bpc + 7) / 8
	rowLen := (columns*colors*bpc + 7) / 8
	if rowLen <= 0 {
		return nil, errors.New("pdf: bad predictor row length")
	}

	if predictor == 2 {
		// TIFF predictor: horizontal differencing, no per-row tag byte
		if len(data)%rowLen != 0 {
			return nil, errors.New("pdf: truncated TIFF predictor data")
		}
		out := make([]byte, len(data))
		for i := 0; i < len(data); i += rowLen {
			copy(out[i:i+rowLen], data[i:i+rowLen])
			for j := bpp; j < rowLen; j++ {
				out[i+j] = (out[i+j] + out[i+j-bpp]) & 0xff
			}
		}
		return out, nil
	}
	if predictor < 10 || predictor > 15 {
		return nil, fmt.Errorf("pdf: unsupported predictor %d", predictor)
	}

	// PNG predictors 10–15: each row is filter-byte + rowLen samples
	stride := rowLen + 1
	if len(data)%stride != 0 {
		return nil, errors.New("pdf: truncated PNG predictor data")
	}
	out := make([]byte, 0, len(data)/stride*rowLen)
	prev := make([]byte, rowLen)
	cur := make([]byte, rowLen)
	for i := 0; i < len(data); i += stride {
		ft := data[i]
		copy(cur, data[i+1:i+stride])
		switch ft {
		case 0: // None
		case 1: // Sub
			for j := bpp; j < rowLen; j++ {
				cur[j] = (cur[j] + cur[j-bpp]) & 0xff
			}
		case 2: // Up
			for j := 0; j < rowLen; j++ {
				cur[j] = (cur[j] + prev[j]) & 0xff
			}
		case 3: // Average
			for j := 0; j < rowLen; j++ {
				left := byte(0)
				if j >= bpp {
					left = cur[j-bpp]
				}
				cur[j] = (cur[j] + byte((int(left)+int(prev[j]))/2)) & 0xff
			}
		case 4: // Paeth
			for j := 0; j < rowLen; j++ {
				left := byte(0)
				upLeft := byte(0)
				if j >= bpp {
					left = cur[j-bpp]
					upLeft = prev[j-bpp]
				}
				cur[j] = (cur[j] + paethPredictor(left, prev[j], upLeft)) & 0xff
			}
		default:
			return nil, fmt.Errorf("pdf: bad PNG filter type %d", ft)
		}
		out = append(out, cur...)
		copy(prev, cur)
	}
	return out, nil
}

func paethPredictor(a, b, c byte) byte {
	// a = left, b = above, c = upper left
	p := int(a) + int(b) - int(c)
	pa := absInt(p - int(a))
	pb := absInt(p - int(b))
	pc := absInt(p - int(c))
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func parseIndirectAt(data []byte, offset int) (Object, int, error) {
	if offset < 0 || offset >= len(data) {
		return nil, 0, fmt.Errorf("pdf: object offset %d out of range (len %d)", offset, len(data))
	}
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

var errTruncatedStream = errors.New("pdf: truncated stream data")

func parseStreamAfterDict(data []byte, p *int, d Dict) (Object, error) {
	*p += len("stream")
	if *p < len(data) && data[*p] == '\r' {
		*p++
	}
	if *p < len(data) && data[*p] == '\n' {
		*p++
	}
	length := -1
	hasLength := false
	if n, ok := d["Length"].(Number); ok {
		length = int(n)
		hasLength = true
	} else if _, ok := d["Length"].(Ref); ok {
		// Indirect Length cannot be resolved from this buffer alone.
		// Prefer expanding the read (errTruncatedStream) over scanning for
		// "endstream", which can false-match inside binary FontFile data.
		hasLength = false
		length = -1
	}
	start := *p
	var raw []byte
	if hasLength {
		if length < 0 {
			return nil, errors.New("pdf: negative stream Length")
		}
		if start+length > len(data) {
			return nil, errTruncatedStream
		}
		raw = data[start : start+length]
		*p = start + length
		skipWSBuf(data, p)
		if bytes.HasPrefix(data[*p:], []byte("endstream")) {
			*p += len("endstream")
		}
	} else {
		// Unknown length (missing or indirect): scan for endstream only as last resort.
		idx := bytes.Index(data[start:], []byte("endstream"))
		if idx < 0 {
			// Likely truncated buffer while Length is still a Ref — ask caller to expand.
			return nil, errTruncatedStream
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
	if *p < 0 {
		*p = 0
	}
	if *p > len(data) {
		*p = len(data)
	}
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

// Resolve follows refs in the document model (lazy-loading as needed).
func (d *DocumentModel) Resolve(obj Object) Object {
	for i := 0; i < 32; i++ {
		ref, ok := obj.(Ref)
		if !ok {
			return obj
		}
		next, err := d.Get(ref.ID)
		if err != nil || next == nil {
			return nil
		}
		obj = next
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
