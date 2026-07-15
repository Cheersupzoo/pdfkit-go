package pdf

import (
	"bytes"
	"compress/zlib"
	"testing"
)

func TestApplyPredictorPNGUp(t *testing.T) {
	// Two rows, Columns=5, predictor tag 2 (Up).
	// Row0 raw: 01 00 00 10 00
	// Row1 delta from row0: 00 00 05 00 00 -> absolute 01 00 05 10 00
	raw := []byte{
		2, 0x01, 0x00, 0x00, 0x10, 0x00,
		2, 0x00, 0x00, 0x05, 0x00, 0x00,
	}
	out, err := applyPredictor(raw, Dict{
		"Predictor": Number(12),
		"Columns":   Number(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0x01, 0x00, 0x00, 0x10, 0x00,
		0x01, 0x00, 0x05, 0x10, 0x00,
	}
	if !bytes.Equal(out, want) {
		t.Fatalf("got %x want %x", out, want)
	}
}

func TestDecodeStreamFlateWithPredictor(t *testing.T) {
	row := []byte{1, 0, 0, 2, 0} // type=1 offset=2 gen=0
	// PNG Up: first row predictor 2 with same bytes (prev is zeros)
	filtered := append([]byte{2}, row...)
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(filtered); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	st := Stream{
		Dict: Dict{
			"Filter": Name("FlateDecode"),
			"DecodeParms": Dict{
				"Predictor": Number(12),
				"Columns":   Number(5),
			},
		},
		Data: buf.Bytes(),
	}
	got, err := decodeStream(st)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, row) {
		t.Fatalf("got %x want %x", got, row)
	}
}

func TestParseIndirectAtOutOfRange(t *testing.T) {
	_, _, err := parseIndirectAt([]byte("%PDF"), 14483458)
	if err == nil {
		t.Fatal("expected error")
	}
}
