package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // registers GIF for image.DecodeConfig
	_ "image/jpeg" // registers JPEG for image.DecodeConfig
	_ "image/png"  // registers PNG for image.DecodeConfig
	"path"
	"strconv"
	"strings"
)

// Images in documents (RAG-010).
//
// A Markdown file's image links and a PDF's embedded pictures become image
// documents of their own: kind "image", meta.parent naming the document they
// came from, their alt text and caption as searchable text, their bytes kept
// apart from the document (image_store.go). Extraction is asked for — the
// extractImages option, or a collection of type "images" — so a file
// uploaded the way it always was stores what it always did.

const (
	// maxImageBytes bounds one image: a picture worth retrieving is rarely
	// larger, and a provider call carrying a larger one is slow and costly.
	maxImageBytes = 2 << 20
	// maxImagesPerDocument bounds what one document can add.
	maxImagesPerDocument = 64
	// minImageSide skips pictures smaller than this in either dimension:
	// bullets, spacers and rules, not content.
	minImageSide = 16
)

// imageFormats maps the formats an image may have to their MIME types.
var imageFormats = map[string]string{
	"jpg": "image/jpeg", "jpeg": "image/jpeg", "png": "image/png", "gif": "image/gif", "webp": "image/webp",
}

// imageExtensions maps a MIME type to the extension an image document's name
// gets.
var imageExtensions = map[string]string{
	"image/jpeg": "jpg", "image/png": "png", "image/gif": "gif", "image/webp": "webp",
}

// errImageType is wrapped by the error for bytes that are no image of a kept
// format.
var errImageType = errors.New("not a JPEG, PNG, GIF or WebP image")

// extractedImage is one image found in a document.
type extractedImage struct {
	// Name is the image document's name under KEY/images/.
	Name          string
	Data          []byte
	MimeType      string
	Width, Height int
	AltText       string
	Caption       string
	// Page is the PDF page the image is drawn on, 1-based; 0 outside a PDF
	// or when the page could not be told.
	Page int
	// Source is where the image came from: a Markdown link target, or the
	// PDF object that holds it.
	Source string
	// Position is the image's place among its document's images, 1-based.
	Position int
}

// imageOptions say whether and how images are extracted from a document.
type imageOptions struct {
	// Extract turns extraction on.
	Extract bool
	// FetchRemote lets a Markdown image link to an http(s) URL be fetched,
	// through the SSRF guard; off, such a link is skipped and reported.
	FetchRemote bool
	// Local finds a file a Markdown link names by a relative path: one
	// uploaded or sent alongside the document. nil finds nothing.
	Local func(ref string) ([]byte, bool)
	// Fetch downloads a remote image; tests replace it.
	Fetch func(ctx context.Context, url string) ([]byte, error)
}

// validateImage checks bytes are an image of a kept format within the size
// limit, and reads its type and dimensions without decoding the pixels.
func validateImage(data []byte) (mime string, width, height int, err error) {
	if len(data) > maxImageBytes {
		return "", 0, 0, fmt.Errorf("image of %d bytes is larger than %d", len(data), maxImageBytes)
	}
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		mime = "image/jpeg"
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		mime = "image/png"
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		mime = "image/gif"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		width, height, err = webpSize(data)
		return "image/webp", width, height, err
	default:
		return "", 0, 0, errImageType
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, fmt.Errorf("unreadable %s: %w", mime, err)
	}
	return mime, cfg.Width, cfg.Height, nil
}

// webpSize reads a WebP image's dimensions from its first chunk: lossy
// (VP8), lossless (VP8L) or extended (VP8X). The standard library has no
// WebP decoder; the header is all a size needs.
func webpSize(data []byte) (int, int, error) {
	if len(data) < 30 {
		return 0, 0, errors.New("truncated WebP")
	}
	b := data[20:]
	switch string(data[12:16]) {
	case "VP8 ":
		if b[3] != 0x9D || b[4] != 0x01 || b[5] != 0x2A {
			return 0, 0, errors.New("WebP VP8 frame without its start code")
		}
		return int(binary.LittleEndian.Uint16(b[6:]) & 0x3FFF), int(binary.LittleEndian.Uint16(b[8:]) & 0x3FFF), nil
	case "VP8L":
		if b[0] != 0x2F {
			return 0, 0, errors.New("WebP VP8L without its signature")
		}
		bits := binary.LittleEndian.Uint32(b[1:])
		return int(bits&0x3FFF) + 1, int((bits>>14)&0x3FFF) + 1, nil
	case "VP8X":
		w := int(b[4]) | int(b[5])<<8 | int(b[6])<<16
		h := int(b[7]) | int(b[8])<<8 | int(b[9])<<16
		return w + 1, h + 1, nil
	}
	return 0, 0, errors.New("WebP with an unknown first chunk")
}

// newExtractedImage validates data and fills in what the bytes say.
func newExtractedImage(data []byte, alt, caption, source string) (extractedImage, error) {
	mime, w, h, err := validateImage(data)
	if err != nil {
		return extractedImage{}, err
	}
	if w < minImageSide || h < minImageSide {
		return extractedImage{}, fmt.Errorf("image of %dx%d is too small to carry content", w, h)
	}
	return extractedImage{Data: data, MimeType: mime, Width: w, Height: h, AltText: alt, Caption: caption, Source: source}, nil
}

// extractImages finds a converted document's images: a PDF's pictures, a
// Markdown document's linked images. Names are made unique and the count is
// bounded; what was skipped is in the warnings.
func extractImages(ctx context.Context, format string, data []byte, markdown string, opts imageOptions) ([]extractedImage, []string) {
	var images []extractedImage
	var warnings []string
	switch format {
	case "pdf":
		images, warnings = pdfImages(data)
	case "md", "markdown", "":
		images, warnings = markdownImages(ctx, markdown, opts)
	default:
		return nil, nil
	}
	if len(images) > maxImagesPerDocument {
		warnings = append(warnings, fmt.Sprintf("%d images skipped: at most %d are kept per document", len(images)-maxImagesPerDocument, maxImagesPerDocument))
		images = images[:maxImagesPerDocument]
	}
	uniqueImageNames(images)
	for i := range images {
		images[i].Position = i + 1
	}
	return images, warnings
}

// uniqueImageNames gives every image a distinct name with its type's
// extension: "chart.png", "chart-2.png".
func uniqueImageNames(images []extractedImage) {
	seen := map[string]bool{}
	for i := range images {
		name := images[i].Name
		ext := imageExtensions[images[i].MimeType]
		base := strings.TrimSuffix(name, path.Ext(name))
		if base == "" || base == "." || base == "/" {
			base = "image-" + strconv.Itoa(i+1)
		}
		name = base + "." + ext
		for n := 2; seen[strings.ToLower(name)]; n++ {
			name = base + "-" + strconv.Itoa(n) + "." + ext
		}
		seen[strings.ToLower(name)] = true
		images[i].Name = name
	}
}

// imageDocContent is an image document's text: a Markdown image of its name
// with its alt text, and its caption. It is what full-text search indexes
// and what a reader of the document sees.
func imageDocContent(im extractedImage) string {
	alt := strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`).Replace(im.AltText)
	out := "![" + alt + "](" + im.Name + ")"
	if im.Caption != "" {
		out += "\n\n" + im.Caption
	}
	return out
}

// Image document metadata keys.
const (
	metaKind        = "kind"
	kindImage       = "image"
	metaParent      = "parent"
	metaAltText     = "altText"
	metaCaption     = "caption"
	metaPage        = "page"
	metaMimeType    = "mimeType"
	metaImageWidth  = "width"
	metaImageHeight = "height"
	metaImageSource = "imageSource"
	metaImageSize   = "size"
	metaPosition    = "position"
)

// imageDocMeta is an image document's metadata: base (the caller's and the
// file's provenance) without what describes only the parent file, and the
// image's own fields. parent is the document the image came from, "" for an
// image file stored on its own.
func imageDocMeta(base map[string][]string, im extractedImage, parent string) map[string][]string {
	meta := make(map[string][]string, len(base)+10)
	for k, v := range base {
		switch k {
		case "extension", "language", "modified", "upload_format", "upload_converted", "conversionConfidence", "conversionWarnings":
			continue
		}
		meta[k] = v
	}
	meta[metaKind] = []string{kindImage}
	meta[metaMimeType] = []string{im.MimeType}
	meta[metaImageWidth] = []string{strconv.Itoa(im.Width)}
	meta[metaImageHeight] = []string{strconv.Itoa(im.Height)}
	meta[metaImageSize] = []string{strconv.Itoa(len(im.Data))}
	// An image of a document names it; an image stored on its own keeps
	// the parent its writer gave it, if any.
	if parent != "" {
		meta[metaParent] = []string{parent}
	}
	for k, v := range map[string]string{metaAltText: im.AltText, metaCaption: im.Caption, metaImageSource: im.Source} {
		if v != "" {
			meta[k] = []string{v}
		} else {
			delete(meta, k)
		}
	}
	for k, v := range map[string]int{metaPage: im.Page, metaPosition: im.Position} {
		delete(meta, k)
		if v > 0 {
			meta[k] = []string{strconv.Itoa(v)}
		}
	}
	return meta
}
