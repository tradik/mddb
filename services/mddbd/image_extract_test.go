package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Unit tests of image extraction (RAG-010): what is an image, what a
// Markdown link names, how names and metadata are made.

func TestValidateImage(t *testing.T) {
	for name, c := range map[string]struct {
		data []byte
		mime string
		w, h int
	}{
		"png":  {solidPNG(30, 20, fixtureRed), "image/png", 30, 20},
		"jpeg": {solidJPEG(17, 19, fixtureBlue), "image/jpeg", 17, 19},
		"gif":  {solidGIF(16, 18, fixtureGreen), "image/gif", 16, 18},
		"vp8":  {webpHeader("VP8 ", 300, 200), "image/webp", 300, 200},
		"vp8l": {webpHeader("VP8L", 64, 48), "image/webp", 64, 48},
		"vp8x": {webpHeader("VP8X", 4000, 3000), "image/webp", 4000, 3000},
	} {
		mime, w, h, err := validateImage(c.data)
		if err != nil || mime != c.mime || w != c.w || h != c.h {
			t.Errorf("%s: %s %dx%d %v", name, mime, w, h, err)
		}
	}
	badVP8 := webpHeader("VP8 ", 30, 30)
	badVP8[23] = 0
	badVP8L := webpHeader("VP8L", 30, 30)
	badVP8L[20] = 0
	for name, data := range map[string][]byte{
		"too large":       append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, maxImageBytes)...),
		"not an image":    []byte("plain text"),
		"corrupt png":     []byte("\x89PNG\r\n\x1a\nnot really"),
		"truncated webp":  []byte("RIFF\x00\x00\x00\x00WEBPVP8 "),
		"vp8 no start":    badVP8,
		"vp8l no sig":     badVP8L,
		"webp other kind": webpHeader("ALPH", 30, 30),
	} {
		if _, _, _, err := validateImage(data); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, _, _, err := validateImage([]byte("plain")); !errors.Is(err, errImageType) {
		t.Errorf("not an image: %v", err)
	}
	if _, err := newExtractedImage(solidPNG(8, 40, fixtureRed), "", "", ""); err == nil {
		t.Error("a sliver is too small to carry content")
	}
	if _, err := newExtractedImage([]byte("x"), "", "", ""); err == nil {
		t.Error("bytes that are no image")
	}
}

func TestMarkdownImageRefs(t *testing.T) {
	md := strings.Join([]string{
		`![a \] b](x.png "T \"q\"") and ![](<y y.png>)`,
		"",
		"_Emphasised caption_",
		"![](z.png)",
		"Fig. 4 the thing",
		"![](w.png)",
		"an ordinary paragraph",
		"~~~",
		"![](fenced.png)",
		"~~~",
		"![](last.png)",
	}, "\r\n")
	refs := markdownImageRefs(md)
	got := make([]string, len(refs))
	for i, r := range refs {
		got[i] = r.Alt + "|" + r.Target + "|" + r.Caption
	}
	want := []string{`a ] b|x.png|T "q"`, "|y y.png|Emphasised caption", "|z.png|Fig. 4 the thing", "|w.png|", "|last.png|"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("refs:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if c := followingCaption([]string{"![](a.png)", "", "*Figure 9*"}, 0); c != "Figure 9" {
		t.Fatalf("caption after a blank line: %q", c)
	}
	if mdUnescape(`trailing\`) != `trailing\` {
		t.Fatal("a trailing backslash is kept")
	}
}

func TestResolveImageTarget(t *testing.T) {
	ctx := context.Background()
	png := solidPNG(20, 20, fixtureRed)
	local := localFiles(map[string][]byte{"./img/a.png": png})
	opts := imageOptions{Local: local}
	for target, ok := range map[string]bool{
		"img/a.png":           true,
		"img/a.png?v=2":       true,
		"other/dir/a.png":     true, // by base name, as a multipart upload sends it
		"img/missing.png":     false,
		"/etc/passwd":         false,
		"../secret.png":       false,
		"ftp://host/a.png":    false,
		"data:image/png,abc":  false,
		"https://example.com": false,
	} {
		data, err := resolveImageTarget(ctx, target, opts)
		if (err == nil) != ok || (ok && !bytes.Equal(data, png)) {
			t.Errorf("%s: %v", target, err)
		}
	}
	if _, err := resolveImageTarget(ctx, "img/a.png", imageOptions{}); err == nil {
		t.Error("no files sent, nothing found")
	}
	if _, err := resolveImageTarget(ctx, "https://example.com/a.png", opts); !errors.Is(err, errRemoteImagesOff) {
		t.Errorf("remote off: %v", err)
	}
	opts.FetchRemote = true
	opts.Fetch = func(_ context.Context, url string) ([]byte, error) { return []byte(url), nil }
	if data, err := resolveImageTarget(ctx, "HTTPS://example.com/a.png", opts); err != nil || string(data) != "HTTPS://example.com/a.png" {
		t.Errorf("remote fetch: %s %v", data, err)
	}
	big := "data:image/png;base64," + strings.Repeat("A", maxImageBytes*2)
	if _, err := decodeDataURI(big); err == nil {
		t.Error("a data URI larger than the limit")
	}
	for _, enc := range []string{base64.StdEncoding.EncodeToString(png), base64.URLEncoding.EncodeToString(png),
		base64.RawStdEncoding.EncodeToString(png), base64.RawURLEncoding.EncodeToString(png)} {
		if data, err := decodeBase64Any(" " + enc + "\n"); err != nil || !bytes.Equal(data, png) {
			t.Errorf("base64 form %q: %v", enc[:8], err)
		}
	}
	if sourceOf("data:") != "data:" || sourceOf("data:image/png;base64,xx") != "data:image/png" {
		t.Error("data URI source")
	}
	if localFiles(nil) != nil {
		t.Error("no files, no finder")
	}
}

func TestFetchImageURL(t *testing.T) {
	png := solidPNG(20, 20, fixtureRed)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big.png":
			_, _ = w.Write(make([]byte, maxImageBytes+10))
		case "/missing.png":
			http.NotFound(w, r)
		default:
			_, _ = w.Write(png)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "false")
	t.Setenv("MDDB_OUTBOUND_ALLOWLIST", "")
	if _, err := fetchImageURL(ctx, srv.URL+"/a.png"); err == nil {
		t.Fatal("the SSRF guard let a loopback address through")
	}
	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "true")
	if data, err := fetchImageURL(ctx, srv.URL+"/a.png"); err != nil || !bytes.Equal(data, png) {
		t.Fatalf("fetch: %v", err)
	}
	if _, err := fetchImageURL(ctx, srv.URL+"/big.png"); err == nil {
		t.Fatal("an image larger than the limit")
	}
	if _, err := fetchImageURL(ctx, srv.URL+"/missing.png"); err == nil {
		t.Fatal("a 404")
	}
	// A Markdown link to it, with fetchRemoteImages on, through the default
	// fetcher.
	images, warnings := markdownImages(ctx, "![remote]("+srv.URL+"/pic.png)", imageOptions{FetchRemote: true})
	if len(images) != 1 || images[0].Name != "pic.png" || len(warnings) != 0 {
		t.Fatalf("remote image: %+v %v", images, warnings)
	}
	if _, err := queryImageBytes(ctx, " "+srv.URL+"/a.png "); err != nil {
		t.Fatalf("query image URL: %v", err)
	}
}

func TestExtractImagesNamesAndLimits(t *testing.T) {
	ctx := context.Background()
	if images, warnings := extractImages(ctx, "docx", nil, "", imageOptions{}); images != nil || warnings != nil {
		t.Fatal("a format without images")
	}
	var md strings.Builder
	files := map[string][]byte{}
	for i := 0; i < maxImagesPerDocument+2; i++ {
		md.WriteString("![](dup.png)\n")
	}
	files["dup.png"] = solidPNG(20, 20, fixtureRed)
	images, warnings := extractImages(ctx, "md", nil, md.String(), imageOptions{Local: localFiles(files)})
	if len(images) != maxImagesPerDocument || len(warnings) != 1 || images[1].Name != "dup-2.png" || images[63].Position != 64 {
		t.Fatalf("%d images, %v, %s", len(images), warnings, images[1].Name)
	}
	named := []extractedImage{{Name: ".png", MimeType: "image/png"}, {Name: "", MimeType: "image/gif"}, {Name: "Chart.PNG", MimeType: "image/png"}, {Name: "chart.png", MimeType: "image/png"}}
	uniqueImageNames(named)
	if named[0].Name != "image-1.png" || named[1].Name != "image-2.gif" || named[2].Name != "Chart.png" || named[3].Name != "chart-2.png" {
		t.Fatalf("names: %+v", named)
	}
}

func TestImageDocMetaAndContent(t *testing.T) {
	im := extractedImage{Name: "a.png", Data: []byte("1234"), MimeType: "image/png", Width: 20, Height: 30,
		AltText: `x [y] \z`, Caption: "Figure 1", Page: 2, Source: "img/a.png", Position: 3}
	if c := imageDocContent(im); c != "![x \\[y\\] \\\\z](a.png)\n\nFigure 1" {
		t.Fatalf("content: %q", c)
	}
	base := map[string][]string{"path": {"docs/m.md"}, "extension": {".md"}, "language": {"markdown"}, "parent": {"other"},
		"page": {"9"}, "altText": {"old"}, "upload_format": {"md"}, "conversionConfidence": {"1.00"}}
	meta := imageDocMeta(base, im, "m")
	for k, want := range map[string]string{"path": "docs/m.md", "kind": "image", "parent": "m", "page": "2", "position": "3",
		"altText": `x [y] \z`, "caption": "Figure 1", "imageSource": "img/a.png", "size": "4", "width": "20", "height": "30", "mimeType": "image/png"} {
		if first(meta[k]) != want {
			t.Errorf("meta.%s = %v, want %s", k, meta[k], want)
		}
	}
	for _, gone := range []string{"extension", "language", "upload_format", "conversionConfidence"} {
		if _, ok := meta[gone]; ok {
			t.Errorf("meta.%s describes the parent file", gone)
		}
	}
	bare := imageDocMeta(base, extractedImage{MimeType: "image/png"}, "")
	for _, gone := range []string{"page", "altText", "position"} {
		if _, ok := bare[gone]; ok {
			t.Errorf("meta.%s on an image without one", gone)
		}
	}
	if first(bare["parent"]) != "other" {
		t.Error("an image stored on its own keeps the parent its writer gave it")
	}
}
