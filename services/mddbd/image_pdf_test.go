package main

import (
	"bytes"
	"fmt"
	"image/color"
	"strings"
	"testing"
)

// PDF image extraction edge cases (RAG-010): what is kept, what is skipped
// and why, and how pages are found.

// imagePDF is a one-page PDF drawing one image XObject with dict and data.
func imagePDF(dict string, data []byte) []byte {
	var p pdfWriter
	p.obj(1, "/Type /Catalog /Pages 2 0 R")
	p.obj(2, "/Type /Pages /Kids [3 0 R] /Count 1")
	p.obj(3, "/Type /Page /Parent 2 0 R /Contents 4 0 R /Resources << /XObject << /Im1 5 0 R >> >>")
	p.stream(4, "", pdfText([]string{"Figure 1: only"}, "Im1"))
	p.stream(5, "/Type /XObject /Subtype /Image "+dict, data)
	return p.bytes()
}

func TestPDFImagesSkipWhatTheyCannotRead(t *testing.T) {
	rgb := rgbSamples(20, 20, fixtureRed, false)
	for reason, c := range map[string]struct {
		dict string
		data []byte
	}{
		"filter JPXDecode is not read":        {"/Width 20 /Height 20 /Filter /JPXDecode", []byte("jpx")},
		"filter /FlateDecode /DCTDecode":      {"/Width 20 /Height 20 /Filter [/FlateDecode /DCTDecode]", []byte("x")},
		"colour space is not RGB or gray":     {"/Width 20 /Height 20 /ColorSpace /DeviceCMYK /BitsPerComponent 8 /Filter /FlateDecode", zlibBytes(rgb)},
		"bit depth other than 8":              {"/Width 20 /Height 20 /ColorSpace /DeviceGray /BitsPerComponent 1 /Filter /FlateDecode", zlibBytes(rgb)},
		"image dimensions out of range":       {"/Width 0 /Height 20 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode", zlibBytes(rgb)},
		"undecodable Flate stream":            {"/Width 20 /Height 20 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode", []byte("not zlib")},
		"fewer samples than the image's size": {"/Width 40 /Height 40 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode", zlibBytes(rgb)},
		"corrupt PNG predictor rows": {"/Width 20 /Height 20 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /DecodeParms << /Predictor 15 >>",
			zlibBytes(rgb)},
		"not a JPEG, PNG, GIF or WebP image": {"/Width 20 /Height 20 /Filter /DCTDecode", []byte("noise, not a JPEG")},
		"too small to carry content":         {"/Width 4 /Height 4 /Filter /DCTDecode", solidJPEG(4, 4, fixtureRed)},
	} {
		images, warnings := pdfImages(imagePDF(c.dict, c.data))
		if len(images) != 0 || len(warnings) != 1 || !strings.Contains(warnings[0], reason) {
			t.Errorf("%s: %d images, %v", reason, len(images), warnings)
		}
	}
	// A stencil mask is a shape, not a picture.
	if images, warnings := pdfImages(imagePDF("/Width 20 /Height 20 /ImageMask true /Filter /DCTDecode", solidJPEG(20, 20, fixtureRed))); len(images)+len(warnings) != 0 {
		t.Errorf("stencil: %v %v", images, warnings)
	}
}

func TestPDFImageColourSpacesAndPredictors(t *testing.T) {
	gray := rgbSamples(18, 18, color.NRGBA{R: 90}, true)
	for name, dict := range map[string]string{
		"CalGray":        "/ColorSpace /CalGray",
		"DeviceGray":     "/ColorSpace /DeviceGray",
		"ICCBased gray":  "/ColorSpace [/ICCBased 7 0 R]",
		"indirect space": "/ColorSpace 6 0 R",
	} {
		var p pdfWriter
		p.obj(1, "/Type /Catalog /Pages 2 0 R")
		p.obj(2, "/Type /Pages /Kids [3 0 R] /Count 1")
		p.obj(3, "/Type /Page /Parent 2 0 R /Resources << /XObject << /Im1 5 0 R >> >>")
		p.stream(5, "/Type /XObject /Subtype /Image /Width 18 /Height 18 /BitsPerComponent 8 /Filter /FlateDecode "+dict, zlibBytes(gray))
		p.raw(6, "[/ICCBased 7 0 R]")
		p.stream(7, "/N 1", []byte("icc"))
		images, warnings := pdfImages(p.bytes())
		if len(images) != 1 || images[0].MimeType != "image/png" || images[0].Page != 1 || len(warnings) != 0 {
			t.Errorf("%s: %+v %v", name, images, warnings)
		}
	}
	for name, dict := range map[string]string{
		"ICCBased CMYK":   "/ColorSpace [/ICCBased 7 0 R]",
		"dangling ref":    "/ColorSpace 9 0 R",
		"indirect other":  "/ColorSpace 8 0 R",
		"no colour space": "",
	} {
		var p pdfWriter
		p.stream(5, "/Type /XObject /Subtype /Image /Width 18 /Height 18 /BitsPerComponent 8 /Filter /FlateDecode "+dict, zlibBytes(gray))
		p.stream(7, "/N 4", []byte("icc"))
		p.raw(8, "[/Indexed /DeviceRGB 1 <000000ffffff>]")
		if images, _ := pdfImages(p.bytes()); len(images) != 0 {
			t.Errorf("%s: kept", name)
		}
	}
	// CalRGB with every PNG filter type (pngRows cycles through them).
	rgb := rgbSamples(20, 20, fixtureBlue, false)
	images, warnings := pdfImages(imagePDF("/Width 20 /Height 20 /ColorSpace /CalRGB /BitsPerComponent 8 /Filter /FlateDecode /DecodeParms << /Predictor 12 >>",
		zlibBytes(pngRows(rgb, 60, 3))))
	if len(images) != 1 || len(warnings) != 0 {
		t.Fatalf("predictors: %+v %v", images, warnings)
	}
	if out, ok := undoPNGPredictor([]byte{9, 1, 2, 3}, 3, 3); ok || out != nil {
		t.Fatal("an unknown filter type")
	}
	if _, ok := undoPNGPredictor([]byte{0, 1}, 0, 1); ok {
		t.Fatal("rows of no length")
	}
	if paeth(10, 20, 30) != 10 || paeth(30, 20, 30) != 20 || paeth(10, 30, 20) != 20 {
		t.Fatal("paeth")
	}
}

func TestPDFObjectsAndPages(t *testing.T) {
	jpg := solidJPEG(20, 20, fixtureGreen)
	// No page tree to follow: pages in file order; an indirect /Length and
	// CRLF line ends; an image no page names gets no page; a file cut off
	// in its last stream.
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	fmt.Fprintf(&b, "3 0 obj\r\n<< /Type /Page /Contents [4 0 R 9 0 R] /Resources << /XObject << /Im1 5 0 R >> >> >>\r\nendobj\r\n")
	content := pdfText([]string{"Figure 1: green"}, "Im1")
	fmt.Fprintf(&b, "4 0 obj\r\n<< /Length 8 0 R >>\r\nstream\r\n%s\r\nendstream\r\nendobj\r\n", content)
	fmt.Fprintf(&b, "5 0 obj\n<< /Type /XObject /Subtype /Image /Width 20 /Height 20 /Filter /DCTDecode /Length 99999 >>\nstream\n%s\nendstream\nendobj\n", jpg)
	fmt.Fprintf(&b, "6 0 obj\n<< /Type /XObject /Subtype /Image /Width 20 /Height 20 /Filter /DCTDecode /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(jpg), jpg)
	fmt.Fprintf(&b, "9 0 obj\n<< /Length 3 >>\nstream\nabc\nendstream\nendobj\n")
	fmt.Fprintf(&b, "10 0 obj\n<< /Length 5 >>\nstream\ncut")
	images, _ := pdfImages(b.Bytes())
	if len(images) != 2 || images[0].Page != 1 || images[0].Caption != "Figure 1: green" || images[0].Name != "page-1-image-1.jpg" && images[0].Name != "page-1-image-1" ||
		images[1].Page != 0 || images[1].Name != "image-1" {
		t.Fatalf("images: %+v", images)
	}
	if objs := pdfObjects([]byte("1 0 obj\n<< /A 1 >>")); len(objs) != 1 || !strings.Contains(objs[0].dict, "/A 1") {
		t.Fatalf("an object without endobj: %+v", objs)
	}

	// A nested page tree, a page with no /Contents, and a page whose
	// caption list runs out before its images.
	var p pdfWriter
	p.obj(1, "/Type /Catalog /Pages 2 0 R")
	p.obj(2, "/Type /Pages /Kids [3 0 R 4 0 R] /Count 2")
	p.obj(3, "/Type /Pages /Parent 2 0 R /Kids [5 0 R] /Count 1")
	p.obj(4, "/Type /Page /Parent 2 0 R /Contents 7 0 R /Resources << /XObject << /A 8 0 R /B 9 0 R >> >>")
	p.obj(5, "/Type /Page /Parent 3 0 R /Resources << /XObject << /A 10 0 R >> >>")
	p.stream(7, "", pdfText([]string{"Figure 2: only one caption"}, "A", "B"))
	for _, n := range []int{8, 9, 10} {
		p.stream(n, "/Type /XObject /Subtype /Image /Width 20 /Height 20 /Filter /DCTDecode", jpg)
	}
	images, _ = pdfImages(p.bytes())
	var got []string
	for _, im := range images {
		got = append(got, fmt.Sprintf("%d:%s:%s", im.Page, im.Name, im.Caption))
	}
	if strings.Join(got, "|") != "1:page-1-image-1:|2:page-2-image-1:Figure 2: only one caption|2:page-2-image-2:" {
		t.Fatalf("pages: %v", got)
	}
	if pdfPageCaptions(&pdfObject{dict: "/Type /Page"}, nil) != nil {
		t.Fatal("a page without contents has no captions")
	}
	if pageOrder(0) <= pageOrder(1<<20) {
		t.Fatal("images without a page sort last")
	}
}
