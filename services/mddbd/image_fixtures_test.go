package main

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Image fixtures (RAG-010). Every file in testdata/images is written by the
// generators below — our own pixels, never a downloaded picture — and the
// extraction of each document fixture has a golden file beside it.
// Regenerate both with
//
//	go test -tags noasm -run TestImageFixturesGolden -update-image-fixtures
//
// and review the diff of the .images files: that diff is what the change did
// to image extraction.

var updateImageFixtures = flag.Bool("update-image-fixtures", false, "rewrite testdata/images and its golden files")

const imageFixtureDir = "testdata/images"

// Colours the fixtures are painted in; the fake multimodal provider reads
// them back as the words "red", "green" and "blue".
var (
	fixtureRed   = color.NRGBA{R: 220, G: 30, B: 30, A: 255}
	fixtureGreen = color.NRGBA{R: 30, G: 200, B: 40, A: 255}
	fixtureBlue  = color.NRGBA{R: 20, G: 40, B: 210, A: 255}
)

// solid is a w×h image of one colour.
func solid(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		img.SetNRGBA(i%w, i/w, c)
	}
	return img
}

func solidPNG(w, h int, c color.NRGBA) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, solid(w, h, c))
	return b.Bytes()
}

func solidJPEG(w, h int, c color.NRGBA) []byte {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, solid(w, h, c), &jpeg.Options{Quality: 90})
	return b.Bytes()
}

func solidGIF(w, h int, c color.NRGBA) []byte {
	var b bytes.Buffer
	pal := image.NewPaletted(image.Rect(0, 0, w, h), color.Palette{c})
	_ = gif.Encode(&b, pal, nil)
	return b.Bytes()
}

// webpHeader is the start of a WebP file whose first chunk is kind, sized
// w×h: enough for the size reader, not a decodable picture.
func webpHeader(kind string, w, h int) []byte {
	chunk := make([]byte, 10)
	// The sizes are a test's own small numbers, which fit every field.
	switch kind {
	case "VP8 ":
		chunk[3], chunk[4], chunk[5] = 0x9D, 0x01, 0x2A
		binary.LittleEndian.PutUint16(chunk[6:], uint16(w)) // #nosec G115 -- a test fixture's small size
		binary.LittleEndian.PutUint16(chunk[8:], uint16(h)) // #nosec G115 -- a test fixture's small size
	case "VP8L":
		chunk[0] = 0x2F
		binary.LittleEndian.PutUint32(chunk[1:], uint32(w-1)|uint32(h-1)<<14) // #nosec G115 -- a test fixture's small size
	case "VP8X":
		w, h = w-1, h-1
		chunk[4], chunk[5], chunk[6] = byte(w), byte(w>>8), byte(w>>16) // #nosec G115 -- a test fixture's small size
		chunk[7], chunk[8], chunk[9] = byte(h), byte(h>>8), byte(h>>16) // #nosec G115 -- a test fixture's small size
	}
	out := []byte("RIFF\x00\x00\x00\x00WEBP" + kind + "\x0a\x00\x00\x00")
	return append(out, chunk...)
}

// zlibBytes deflates data as a PDF FlateDecode stream holds it.
func zlibBytes(data []byte) []byte {
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	_, _ = zw.Write(data)
	_ = zw.Close()
	return z.Bytes()
}

// pngRows prefixes each row of raw samples with a PNG filter byte: the
// filters cycle through all five so each is undone once.
func pngRows(raw []byte, rowLen, bpp int) []byte {
	var out []byte
	prev := make([]byte, rowLen)
	for r := 0; r*rowLen < len(raw); r++ {
		row := raw[r*rowLen : (r+1)*rowLen]
		ft := byte(r % 5)
		enc := make([]byte, rowLen)
		for i := range row {
			var left, upLeft byte
			if i >= bpp {
				left, upLeft = row[i-bpp], prev[i-bpp]
			}
			up := prev[i]
			switch ft {
			case 0:
				enc[i] = row[i]
			case 1:
				enc[i] = row[i] - left
			case 2:
				enc[i] = row[i] - up
			case 3:
				enc[i] = row[i] - (left/2 + up/2 + left&up&1)
			case 4:
				enc[i] = row[i] - paeth(left, up, upLeft)
			}
		}
		out = append(out, ft)
		out = append(out, enc...)
		prev = row
	}
	return out
}

// rgbSamples are the raw samples of an image in RGB or, with gray, one
// channel.
func rgbSamples(w, h int, c color.NRGBA, gray bool) []byte {
	var out []byte
	for i := 0; i < w*h; i++ {
		if gray {
			out = append(out, c.R)
			continue
		}
		out = append(out, c.R, c.G, c.B)
	}
	return out
}

// pdfWriter writes numbered objects and a trailer.
type pdfWriter struct{ b bytes.Buffer }

func (p *pdfWriter) obj(n int, dict string) {
	fmt.Fprintf(&p.b, "%d 0 obj\n<< %s >>\nendobj\n", n, dict)
}

func (p *pdfWriter) raw(n int, body string) {
	fmt.Fprintf(&p.b, "%d 0 obj\n%s\nendobj\n", n, body)
}

func (p *pdfWriter) stream(n int, dict string, data []byte) {
	fmt.Fprintf(&p.b, "%d 0 obj\n<< %s /Length %d >>\nstream\n", n, dict, len(data))
	p.b.Write(data)
	p.b.WriteString("\nendstream\nendobj\n")
}

func (p *pdfWriter) bytes() []byte {
	return append([]byte("%PDF-1.5\n%\xe2\xe3\xcf\xd3\n"), append(p.b.Bytes(), "trailer\n<< /Root 1 0 R >>\n%%EOF\n"...)...)
}

// pdfText is a content stream drawing lines of text and the named images.
func pdfText(lines []string, images ...string) []byte {
	var ops []string
	for _, im := range images {
		ops = append(ops, "q 100 0 0 100 72 400 cm /"+im+" Do Q")
	}
	ops = append(ops, "BT /F1 12 Tf")
	for i, l := range lines {
		ops = append(ops, fmt.Sprintf("72 %d Td (%s) Tj", 720-14*i, l))
	}
	ops = append(ops, "ET")
	return []byte(strings.Join(ops, "\n"))
}

// fixtureImagePDF is a two-page manual: page 1 draws a red JPEG, page 2 a
// blue RGB picture behind a Flate stream with PNG predictors (and a soft
// mask, which is not a picture), and a green one in an ICCBased gray-free
// space, its resources and XObject dictionary indirect.
func fixtureImagePDF() []byte {
	var p pdfWriter
	p.obj(1, "/Type /Catalog /Pages 2 0 R")
	p.obj(2, "/Type /Pages /Kids [3 0 R 6 0 R] /Count 2")
	p.obj(3, "/Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /XObject << /Im1 5 0 R >> >>")
	p.stream(4, "", pdfText([]string{"Pump manual", "Figure 1: Exploded view of the pump"}, "Im1"))
	p.stream(5, "/Type /XObject /Subtype /Image /Width 32 /Height 24 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode",
		solidJPEG(32, 24, fixtureRed))
	p.obj(6, "/Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents [7 0 R] /Resources 8 0 R")
	p.stream(7, "/Filter /FlateDecode", zlibBytes(pdfText([]string{"Wiring", "Figure 2: Wiring diagram", "Figure 3: Status lamp"}, "Im2", "Im3")))
	p.obj(8, "/XObject 9 0 R")
	p.obj(9, "/Im2 10 0 R /Im3 12 0 R")
	p.stream(10, "/Type /XObject /Subtype /Image /Width 20 /Height 18 /ColorSpace /DeviceRGB /BitsPerComponent 8 /SMask 11 0 R "+
		"/Filter /FlateDecode /DecodeParms << /Predictor 15 /Colors 3 /Columns 20 >>",
		zlibBytes(pngRows(rgbSamples(20, 18, fixtureBlue, false), 60, 3)))
	p.stream(11, "/Type /XObject /Subtype /Image /Width 20 /Height 18 /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode",
		zlibBytes(rgbSamples(20, 18, color.NRGBA{R: 255}, true)))
	p.stream(12, "/Type /XObject /Subtype /Image /Width 16 /Height 16 /ColorSpace [/ICCBased 13 0 R] /BitsPerComponent 8 /Filter /FlateDecode",
		zlibBytes(rgbSamples(16, 16, fixtureGreen, false)))
	p.stream(13, "/N 3", []byte("icc profile bytes"))
	return p.bytes()
}

// fixtureImageMarkdown is a manual whose links name a PNG and a JPEG sent
// beside it, a GIF as a data URI, a remote picture and one that is missing.
func fixtureImageMarkdown() string {
	gifURI := "data:image/gif;base64," + base64.StdEncoding.EncodeToString(solidGIF(16, 16, fixtureGreen))
	return strings.Join([]string{
		"# Pump manual",
		"",
		"The pump seal keeps water out of the motor.",
		"",
		`![Exploded view of the pump seal](img/seal.png "Figure 1: the seal")`,
		"",
		"![](img/wiring.jpg)",
		"*Figure 2: wiring of the motor*",
		"",
		"Status lamp: ![Green status lamp](" + gifURI + ")",
		"",
		"![Vendor logo](https://example.com/logo.png)",
		"",
		"![Missing chart](img/missing.png)",
		"",
		"```",
		"![not an image](inside/a/fence.png)",
		"```",
	}, "\n")
}

// imageFixtureFiles are the files beside the Markdown fixture.
func imageFixtureFiles() map[string][]byte {
	return map[string][]byte{
		"img/seal.png":   solidPNG(40, 30, fixtureRed),
		"img/wiring.jpg": solidJPEG(48, 32, fixtureBlue),
	}
}

// imageFixtures are every fixture file by its path under testdata/images.
func imageFixtures() map[string][]byte {
	out := map[string][]byte{
		"manual.md":  []byte(fixtureImageMarkdown()),
		"manual.pdf": fixtureImagePDF(),
	}
	for name, data := range imageFixtureFiles() {
		out[name] = data
	}
	return out
}

// renderImages is the golden form of an extraction: one line per image and
// one per warning.
func renderImages(images []extractedImage, warnings []string) string {
	var b strings.Builder
	for _, im := range images {
		fmt.Fprintf(&b, "%d %s %s %dx%d page=%d alt=%q caption=%q source=%q\n",
			im.Position, im.Name, im.MimeType, im.Width, im.Height, im.Page, im.AltText, im.Caption, im.Source)
	}
	sorted := append([]string(nil), warnings...)
	sort.Strings(sorted)
	for _, w := range sorted {
		b.WriteString("warning: " + w + "\n")
	}
	return b.String()
}

// readImageFixture reads a fixture file.
func readImageFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(imageFixtureDir, filepath.FromSlash(name))) // #nosec G304 -- a fixture of this package
	if err != nil {
		t.Fatalf("%v (regenerate with -update-image-fixtures)", err)
	}
	return data
}

func TestImageFixturesGolden(t *testing.T) {
	fixtures := imageFixtures()
	if *updateImageFixtures {
		for name, data := range fixtures {
			path := filepath.Join(imageFixtureDir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The generators are deterministic: the files on disk are what they
	// write, so a fixture cannot drift from the code that documents it.
	for name, data := range fixtures {
		if !bytes.Equal(readImageFixture(t, name), data) {
			t.Errorf("%s differs from its generator", name)
		}
	}
	files := map[string][]byte{}
	for name := range imageFixtureFiles() {
		files[name] = readImageFixture(t, name)
	}
	opts := imageOptions{Extract: true, Local: localFiles(files)}
	for _, name := range []string{"manual.md", "manual.pdf"} {
		t.Run(name, func(t *testing.T) {
			data := readImageFixture(t, name)
			c, err := convertToMarkdown(formatFromFilename(name), data, false)
			if err != nil {
				t.Fatal(err)
			}
			got := renderImages(extractImages(context.Background(), c.Format, data, c.Markdown, opts))
			golden := filepath.Join(imageFixtureDir, name+".images")
			if *updateImageFixtures {
				if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden) // #nosec G304 -- a golden file of this package
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("%s extracts differently from %s:\n--- got\n%s\n--- want\n%s", name, golden, got, want)
			}
		})
	}
}
