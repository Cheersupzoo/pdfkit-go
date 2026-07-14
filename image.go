package pdfkit

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"

	"github.com/Cheersupzoo/pdfkit-go/internal/pdf"
)

type imageResource struct {
	name   string
	width  int
	height int
	dict   pdf.Dict
	data   []byte
	smask  *imageResource
}

// RegisterImage loads JPEG or PNG from bytes and returns a resource name.
func (d *Document) RegisterImage(name string, data []byte) (string, error) {
	if name == "" {
		name = fmt.Sprintf("Im%d", len(d.images)+1)
	}
	if looksLikeJPEG(data) {
		ir, err := jpegImage(name, data)
		if err != nil {
			return "", err
		}
		d.images[name] = ir
		return name, nil
	}
	ir, smask, err := pngImage(name, data)
	if err != nil {
		return "", err
	}
	d.images[name] = ir
	if smask != nil {
		d.images[smask.name] = smask
		ir.smask = smask
	}
	return name, nil
}

// RegisterImageFile loads an image from disk.
func (d *Document) RegisterImageFile(name, path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return d.RegisterImage(name, data)
}

// Image draws a registered image at x,y with width w (height scaled if h<=0).
func (d *Document) Image(name string, x, y, w, h float64) *Document {
	ir, ok := d.images[name]
	if !ok {
		d.setErr(fmt.Errorf("pdfkit: unknown image %q", name))
		return d
	}
	if w <= 0 {
		w = float64(ir.width)
	}
	if h <= 0 {
		h = w * float64(ir.height) / float64(ir.width)
	}
	p := d.Page()
	p.usedImages[name] = true
	if ir.smask != nil {
		p.usedImages[ir.smask.name] = true
	}
	p.write("q %.5f 0 0 %.5f %.5f %.5f cm /%s Do Q\n", w, h, x, y, name)
	return d
}

// ImageFile is a convenience to register and draw an image.
func (d *Document) ImageFile(path string, x, y, w, h float64) *Document {
	name, err := d.RegisterImageFile("", path)
	if err != nil {
		d.setErr(err)
		return d
	}
	return d.Image(name, x, y, w, h)
}

func (ir *imageResource) embed(cat *pdf.Catalog) (pdf.Ref, error) {
	d := pdf.Dict{}
	for k, v := range ir.dict {
		d[k] = v
	}
	if ir.smask != nil {
		smRef, err := ir.smask.embed(cat)
		if err != nil {
			return pdf.Ref{}, err
		}
		d["SMask"] = smRef
	}
	st := pdf.Stream{Dict: d, Data: ir.data}
	return cat.Add(st), nil
}

func looksLikeJPEG(data []byte) bool {
	return len(data) > 2 && data[0] == 0xff && data[1] == 0xd8
}

func jpegImage(name string, data []byte) (*imageResource, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	cs := pdf.Name("DeviceRGB")
	if cfg.ColorModel == color.GrayModel {
		cs = pdf.Name("DeviceGray")
	}
	return &imageResource{
		name:   name,
		width:  cfg.Width,
		height: cfg.Height,
		data:   append([]byte(nil), data...),
		dict: pdf.Dict{
			"Type":             pdf.Name("XObject"),
			"Subtype":          pdf.Name("Image"),
			"Width":            pdf.Number(cfg.Width),
			"Height":           pdf.Number(cfg.Height),
			"ColorSpace":       cs,
			"BitsPerComponent": pdf.Number(8),
			"Filter":           pdf.Name("DCTDecode"),
		},
	}, nil
}

func pngImage(name string, data []byte) (*imageResource, *imageResource, error) {
	img, err := decodePNG(bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	hasAlpha := false
	rgb := make([]byte, w*h*3)
	alpha := make([]byte, w*h)
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			rgb[i*3] = byte(r >> 8)
			rgb[i*3+1] = byte(g >> 8)
			rgb[i*3+2] = byte(bl >> 8)
			alpha[i] = byte(a >> 8)
			if alpha[i] != 255 {
				hasAlpha = true
			}
			i++
		}
	}
	compressed, err := pdf.Flate(rgb)
	if err != nil {
		return nil, nil, err
	}
	ir := &imageResource{
		name:   name,
		width:  w,
		height: h,
		data:   compressed,
		dict: pdf.Dict{
			"Type":             pdf.Name("XObject"),
			"Subtype":          pdf.Name("Image"),
			"Width":            pdf.Number(w),
			"Height":           pdf.Number(h),
			"ColorSpace":       pdf.Name("DeviceRGB"),
			"BitsPerComponent": pdf.Number(8),
			"Filter":           pdf.Name("FlateDecode"),
		},
	}
	var sm *imageResource
	if hasAlpha {
		ac, err := pdf.Flate(alpha)
		if err != nil {
			return nil, nil, err
		}
		sm = &imageResource{
			name:   name + "_SMask",
			width:  w,
			height: h,
			data:   ac,
			dict: pdf.Dict{
				"Type":             pdf.Name("XObject"),
				"Subtype":          pdf.Name("Image"),
				"Width":            pdf.Number(w),
				"Height":           pdf.Number(h),
				"ColorSpace":       pdf.Name("DeviceGray"),
				"BitsPerComponent": pdf.Number(8),
				"Filter":           pdf.Name("FlateDecode"),
			},
		}
	}
	return ir, sm, nil
}

func decodePNG(r io.Reader) (image.Image, error) {
	img, format, err := image.Decode(r)
	if err != nil {
		return nil, err
	}
	if format != "png" && format != "jpeg" {
		// still accept
	}
	return img, nil
}
