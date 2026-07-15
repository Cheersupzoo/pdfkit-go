package pdf

import (
	"fmt"
	"io"
	"os"
)

// sizedReaderAt is a ReaderAt that knows its total size.
type sizedReaderAt interface {
	io.ReaderAt
	Size() int64
}

// readerAtOf returns random-access bytes for r.
// Non-seekable readers are spooled to a temp file (closer removes it).
// *os.File is used in place without taking ownership (closer is nil)
// unless ownFile is true.
func readerAtOf(r io.Reader) (ra io.ReaderAt, size int64, closer io.Closer, err error) {
	switch v := r.(type) {
	case *os.File:
		st, err := v.Stat()
		if err != nil {
			return nil, 0, nil, err
		}
		return v, st.Size(), nil, nil
	case sizedReaderAt:
		return v, v.Size(), nil, nil
	case io.ReaderAt:
		// ReaderAt without Size: spool so we know the length.
	case nil:
		return nil, 0, nil, fmt.Errorf("pdf: nil reader")
	}

	f, err := os.CreateTemp("", "pdfkit-*.pdf")
	if err != nil {
		return nil, 0, nil, err
	}
	n, err := io.Copy(f, r)
	if err != nil {
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
		return nil, 0, nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
		return nil, 0, nil, err
	}
	return f, n, &tempFile{File: f}, nil
}

// tempFile closes and removes the backing file.
type tempFile struct {
	*os.File
}

func (t *tempFile) Close() error {
	name := t.File.Name()
	err := t.File.Close()
	if remErr := os.Remove(name); err == nil {
		err = remErr
	}
	return err
}

// OpenReaderAt opens a PDF from a random-access source.
// If closer is non-nil, DocumentModel.Close closes it (e.g. owned *os.File or temp spool).
func OpenReaderAt(ra io.ReaderAt, size int64, closer io.Closer) (*DocumentModel, error) {
	if ra == nil {
		return nil, fmt.Errorf("pdf: nil ReaderAt")
	}
	if size < 5 {
		if closer != nil {
			_ = closer.Close()
		}
		return nil, fmt.Errorf("pdf: file too small")
	}
	d := &DocumentModel{
		ra:     ra,
		size:   size,
		closer: closer,
		xref:   map[int]xrefEntry{},
		cache:  map[int]Object{},
	}
	if err := d.initFromReaderAt(); err != nil {
		_ = d.Close()
		return nil, err
	}
	return d, nil
}
