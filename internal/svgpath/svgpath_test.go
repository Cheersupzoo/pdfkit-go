package svgpath_test

import (
	"testing"

	"github.com/Cheersupzoo/pdfkit-go/internal/svgpath"
)

func TestParseRelativeAndSmooth(t *testing.T) {
	cmds, err := svgpath.Parse("m10,10 l20 0 c10 10 20 10 30 0 s20 -10 30 0 z")
	if err != nil {
		t.Fatal(err)
	}
	if cmds[0].Op != 'M' {
		t.Fatalf("got %c", cmds[0].Op)
	}
}
