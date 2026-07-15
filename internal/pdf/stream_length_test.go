package pdf

import (
	"bytes"
	"fmt"
	"testing"
)

// TestIndirectLengthStreamPreserved builds a PDF whose stream uses an indirect
// /Length and embeds binary data containing ASCII "endstream" early — a classic
// false-match for naive scanners. Lazy open must still return the full payload.
func TestIndirectLengthStreamPreserved(t *testing.T) {
	payload := make([]byte, 0, 4096)
	payload = append(payload, []byte("AAAA")...)
	payload = append(payload, []byte("endstream")...) // trap for naive scanners
	payload = append(payload, bytes.Repeat([]byte{0xAB}, 3000)...)
	payload = append(payload, []byte("ZZZZ")...)

	var body bytes.Buffer
	body.WriteString("%PDF-1.7\n%\xE2\xE3\xCF\xD3\n")
	off := make([]int, 6)

	write := func(id int, s string) {
		off[id] = body.Len()
		fmt.Fprintf(&body, "%d 0 obj\n%s\nendobj\n", id, s)
	}

	write(1, fmt.Sprintf("%d", len(payload)))
	off[2] = body.Len()
	fmt.Fprintf(&body, "2 0 obj\n<< /Length 1 0 R >>\nstream\n")
	body.Write(payload)
	body.WriteString("\nendstream\nendobj\n")
	write(3, "<< /Type /Pages /Kids [4 0 R] /Count 1 >>")
	write(4, "<< /Type /Page /Parent 3 0 R /MediaBox [0 0 100 100] /Contents 2 0 R >>")
	write(5, "<< /Type /Catalog /Pages 3 0 R >>")

	xrefPos := body.Len()
	fmt.Fprintf(&body, "xref\n0 6\n")
	body.WriteString("0000000000 65535 f \n")
	for id := 1; id <= 5; id++ {
		fmt.Fprintf(&body, "%010d 00000 n \n", off[id])
	}
	fmt.Fprintf(&body, "trailer\n<< /Size 6 /Root 5 0 R >>\nstartxref\n%d\n%%%%EOF\n", xrefPos)

	model, err := Open(bytes.NewReader(body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer model.Close()

	obj, err := model.Get(2)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := obj.(Stream)
	if !ok {
		t.Fatalf("got %T", obj)
	}
	if len(st.Data) != len(payload) {
		t.Fatalf("stream len=%d want %d (truncated?)", len(st.Data), len(payload))
	}
	if !bytes.Equal(st.Data, payload) {
		t.Fatal("stream payload mismatch")
	}
	if _, ok := st.Dict["Length"].(Number); !ok {
		t.Fatalf("Length should be normalized to Number, got %T", st.Dict["Length"])
	}
}
