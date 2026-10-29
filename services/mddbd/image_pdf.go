package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// PDF images (RAG-010): the image XObjects a PDF holds, as files.
//
// Kept:
//   - DCTDecode streams, which are JPEG files as they are;
//   - FlateDecode streams of 8-bit DeviceRGB, DeviceGray or a one- or
//     three-component ICCBased colour space, PNG predictors undone, written
//     as PNG.
//
// Skipped, and counted in the warnings: other filters (JPX, CCITT, JBIG2,
// chains of filters), other colour spaces (CMYK in Flate, Indexed, Lab,
// Separation), bit depths other than 8, stencil masks, soft masks (the
// alpha of another image, never a picture of its own), inline images
// (BI ... EI in a content stream) and images drawn only inside a form
// XObject, which are found but get no page. A page's caption is a line of
// its text starting "Figure" or "Fig.", given to its images in order.

// pdfObjRe finds an indirect object's header: "12 0 obj".
var pdfObjRe = regexp.MustCompile(`(\d+)\s+\d+\s+obj\b`)

var (
	// pdfRefRe finds an indirect reference: "12 0 R".
	pdfRefRe = regexp.MustCompile(`(\d+)\s+\d+\s+R\b`)
	// pdfMaskRe finds the soft mask or mask another image names.
	pdfMaskRe      = regexp.MustCompile(`/S?Mask\s+(\d+)\s+\d+\s+R`)
	pdfImageRe     = regexp.MustCompile(`/Subtype\s*/Image\b`)
	pdfImageMaskRe = regexp.MustCompile(`/ImageMask\s+true`)
	pdfICCRe       = regexp.MustCompile(`/ColorSpace\s*(?:\[\s*/ICCBased\s+(\d+)\s+\d+\s+R\s*\]|(\d+)\s+\d+\s+R)`)
	pdfICCArrayRe  = regexp.MustCompile(`\[\s*/ICCBased\s+(\d+)\s+\d+\s+R`)
)

// pdfObject is one indirect object: its dictionary text and, for a stream,
// its raw (still encoded) bytes.
type pdfObject struct {
	num    int
	dict   string
	stream []byte
}

// pdfObjects reads every indirect object of a PDF in file order. A stream's
// end is found by its /Length when that is a direct number that lands on
// "endstream", and by searching for "endstream" otherwise.
func pdfObjects(data []byte) []*pdfObject {
	var out []*pdfObject
	pos := 0
	for pos < len(data) {
		loc := pdfObjRe.FindSubmatchIndex(data[pos:])
		if loc == nil {
			break
		}
		num, _ := strconv.Atoi(string(data[pos+loc[2] : pos+loc[3]]))
		body := pos + loc[1]
		end := bytes.Index(data[body:], []byte("endobj"))
		if end < 0 {
			end = len(data) - body
		}
		obj := &pdfObject{num: num}
		streamAt := bytes.Index(data[body:body+end], []byte("stream"))
		if streamAt < 0 {
			obj.dict = string(data[body : body+end])
			out = append(out, obj)
			pos = body + end
			continue
		}
		obj.dict = string(data[body : body+streamAt])
		start := body + streamAt + len("stream")
		if start < len(data) && data[start] == '\r' {
			start++
		}
		if start < len(data) && data[start] == '\n' {
			start++
		}
		stop := pdfStreamEnd(data, start, obj.dict)
		obj.stream = data[start:stop]
		out = append(out, obj)
		pos = stop
	}
	return out
}

// pdfLengthRe reads a direct /Length.
var pdfLengthRe = regexp.MustCompile(`/Length\s+(\d+)(\s+\d+\s+R)?`)

// pdfStreamEnd is where a stream's bytes end.
func pdfStreamEnd(data []byte, start int, dict string) int {
	if m := pdfLengthRe.FindStringSubmatch(dict); m != nil && m[2] == "" {
		if n, err := strconv.Atoi(m[1]); err == nil && start+n <= len(data) &&
			bytes.HasPrefix(bytes.TrimLeft(data[start+n:], "\r\n \t"), []byte("endstream")) {
			return start + n
		}
	}
	if i := bytes.Index(data[start:], []byte("endstream")); i >= 0 {
		return start + i
	}
	return len(data)
}

// pdfDictInt reads a direct integer entry of a dictionary, 0 when absent.
func pdfDictInt(dict, name string) int {
	m := regexp.MustCompile(`/` + name + `\s+(\d+)`).FindStringSubmatch(dict)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// pdfDictName reads a name entry: "/Filter /FlateDecode" or a one-element
// array "/Filter [/FlateDecode]". An array of several names is returned with
// its brackets, which no caller accepts.
func pdfDictName(dict, name string) string {
	m := regexp.MustCompile(`/` + name + `\s*(\[\s*/(\w+)\s*\]|/(\w+)|\[[^\]]*\])`).FindStringSubmatch(dict)
	switch {
	case m == nil:
		return ""
	case m[2] != "":
		return m[2]
	case m[3] != "":
		return m[3]
	}
	return m[1]
}

// pdfImages extracts a PDF's pictures in page order.
func pdfImages(data []byte) ([]extractedImage, []string) {
	objs := pdfObjects(data)
	byNum := make(map[int]*pdfObject, len(objs))
	masks := map[int]bool{}
	for _, o := range objs {
		byNum[o.num] = o
		for _, m := range pdfMaskRe.FindAllStringSubmatch(o.dict, -1) {
			n, _ := strconv.Atoi(m[1])
			masks[n] = true
		}
	}
	pageOf, pages := pdfImagePages(objs, byNum)
	var images []extractedImage
	skipped := map[string]int{}
	for _, o := range objs {
		if o.stream == nil || !pdfImageRe.MatchString(o.dict) {
			continue
		}
		if masks[o.num] || pdfImageMaskRe.MatchString(o.dict) {
			continue // the alpha or stencil of another image
		}
		im, reason := pdfImage(o, byNum)
		if reason != "" {
			skipped[reason]++
			continue
		}
		im.Page = pageOf[o.num]
		im.Source = fmt.Sprintf("pdf object %d", o.num)
		images = append(images, im)
	}
	sort.SliceStable(images, func(i, j int) bool { return pageOrder(images[i].Page) < pageOrder(images[j].Page) })
	assignPDFCaptions(images, pages, byNum)
	onPage := map[int]int{}
	for i := range images {
		p := images[i].Page
		onPage[p]++
		if p > 0 {
			images[i].Name = fmt.Sprintf("page-%d-image-%d", p, onPage[p])
		} else {
			images[i].Name = fmt.Sprintf("image-%d", onPage[p])
		}
	}
	reasons := make([]string, 0, len(skipped))
	for r := range skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	warnings := make([]string, 0, len(reasons))
	for _, r := range reasons {
		warnings = append(warnings, fmt.Sprintf("%d PDF images skipped: %s", skipped[r], r))
	}
	return images, warnings
}

// pageOrder sorts images without a page after those with one.
func pageOrder(page int) int {
	if page == 0 {
		return int(^uint(0) >> 1)
	}
	return page
}

// pdfImage turns one image XObject into an image, or says why not.
func pdfImage(o *pdfObject, byNum map[int]*pdfObject) (extractedImage, string) {
	filter := pdfDictName(o.dict, "Filter")
	var data []byte
	switch filter {
	case "DCTDecode":
		data = o.stream
	case "FlateDecode":
		pixels, ok := pdfInflate(string(o.stream))
		if !ok {
			return extractedImage{}, "undecodable Flate stream"
		}
		var reason string
		if data, reason = pdfFlateImagePNG([]byte(pixels), o.dict, byNum); reason != "" {
			return extractedImage{}, reason
		}
	default:
		return extractedImage{}, "filter " + strings.Trim(filter, "[]") + " is not read"
	}
	im, err := newExtractedImage(data, "", "", "")
	if err != nil {
		return extractedImage{}, err.Error()
	}
	return im, ""
}

// pdfFlateImagePNG encodes inflated 8-bit gray or RGB samples as a PNG.
func pdfFlateImagePNG(raw []byte, dict string, byNum map[int]*pdfObject) ([]byte, string) {
	w, h, bpc := pdfDictInt(dict, "Width"), pdfDictInt(dict, "Height"), pdfDictInt(dict, "BitsPerComponent")
	if bpc != 8 {
		return nil, "bit depth other than 8"
	}
	comps := pdfColorComponents(dict, byNum)
	if comps == 0 {
		return nil, "colour space is not RGB or gray"
	}
	if w <= 0 || h <= 0 || w*h > maxImageBytes*8 {
		return nil, "image dimensions out of range"
	}
	if pdfDictInt(dict, "Predictor") >= 10 {
		var ok bool
		if raw, ok = undoPNGPredictor(raw, w*comps, comps); !ok {
			return nil, "corrupt PNG predictor rows"
		}
	}
	if len(raw) < w*h*comps {
		return nil, "fewer samples than the image's size"
	}
	var img image.Image
	if comps == 1 {
		g := image.NewGray(image.Rect(0, 0, w, h))
		copy(g.Pix, raw)
		img = g
	} else {
		rgba := image.NewNRGBA(image.Rect(0, 0, w, h))
		for i := 0; i < w*h; i++ {
			rgba.SetNRGBA(i%w, i/w, color.NRGBA{R: raw[3*i], G: raw[3*i+1], B: raw[3*i+2], A: 0xFF})
		}
		img = rgba
	}
	var buf bytes.Buffer
	// Encoding an in-memory image into a buffer fails only for a zero-size
	// image, which the bounds above exclude.
	_ = png.Encode(&buf, img)
	return buf.Bytes(), ""
}

// pdfColorComponents is the number of components of a gray or RGB colour
// space, followed into an ICCBased stream's /N; 0 for any other.
func pdfColorComponents(dict string, byNum map[int]*pdfObject) int {
	switch pdfDictName(dict, "ColorSpace") {
	case "DeviceRGB", "CalRGB":
		return 3
	case "DeviceGray", "CalGray":
		return 1
	}
	m := pdfICCRe.FindStringSubmatch(dict)
	if m == nil {
		return 0
	}
	ref := m[1]
	if ref == "" {
		// An indirect colour space: an array object naming ICCBased.
		n, _ := strconv.Atoi(m[2])
		if o := byNum[n]; o != nil {
			if mm := pdfICCArrayRe.FindStringSubmatch(o.dict); mm != nil {
				ref = mm[1]
			}
		}
	}
	n, _ := strconv.Atoi(ref)
	if o := byNum[n]; o != nil {
		if c := pdfDictInt(o.dict, "N"); c == 1 || c == 3 {
			return c
		}
	}
	return 0
}

// undoPNGPredictor reverses the PNG row filters a FlateDecode stream may use
// (/Predictor 10-15): each row starts with its filter type.
func undoPNGPredictor(data []byte, rowLen, bpp int) ([]byte, bool) {
	stride := rowLen + 1
	if rowLen <= 0 || len(data)%stride != 0 {
		return nil, false
	}
	rows := len(data) / stride
	out := make([]byte, rows*rowLen)
	prev := make([]byte, rowLen)
	for r := 0; r < rows; r++ {
		ft, in, cur := data[r*stride], data[r*stride+1:(r+1)*stride], out[r*rowLen:(r+1)*rowLen]
		for i := range in {
			var left, upLeft byte
			if i >= bpp {
				left, upLeft = cur[i-bpp], prev[i-bpp]
			}
			up := prev[i]
			switch ft {
			case 0:
				cur[i] = in[i]
			case 1:
				cur[i] = in[i] + left
			case 2:
				cur[i] = in[i] + up
			case 3:
				cur[i] = in[i] + left/2 + up/2 + left&up&1 // floor((left+up)/2) in bytes
			case 4:
				cur[i] = in[i] + paeth(left, up, upLeft)
			default:
				return nil, false
			}
		}
		prev = cur
	}
	return out, true
}

// paeth is the PNG Paeth predictor.
func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := absInt(p-int(a)), absInt(p-int(b)), absInt(p-int(c))
	switch {
	case pa <= pb && pa <= pc:
		return a
	case pb <= pc:
		return b
	}
	return c
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
