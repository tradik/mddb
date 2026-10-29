package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Images of a Markdown document (RAG-010): its image links, and the bytes
// each one names — a data URI, a file sent with the document by a relative
// path, or, only when asked, an http(s) URL fetched through the SSRF guard.
// A link the document's own server cannot read (an absolute path, a path
// leaving the document's directory, another scheme) is reported, not read.

// mdImageRe matches a Markdown image: ![alt](target "title"), the target
// in angle brackets when it has spaces.
var mdImageRe = regexp.MustCompile(`!\[((?:[^\]\\]|\\.)*)\]\(\s*(?:<([^>]+)>|([^\s)]+))(?:\s+"((?:[^"\\]|\\.)*)")?\s*\)`)

// mdImageRef is one image link of a Markdown document.
type mdImageRef struct {
	Alt, Target, Caption string
}

// markdownImageRefs lists a Markdown document's image links outside code
// fences, with each one's caption: its title, else an emphasised line or a
// line starting "Figure" or "Fig." right after the image.
func markdownImageRefs(md string) []mdImageRef {
	var refs []mdImageRef
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	fenced := false
	for i, line := range lines {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		for _, m := range mdImageRe.FindAllStringSubmatch(line, -1) {
			ref := mdImageRef{Alt: mdUnescape(m[1]), Target: m[2] + m[3], Caption: mdUnescape(m[4])}
			if ref.Caption == "" {
				ref.Caption = followingCaption(lines, i)
			}
			refs = append(refs, ref)
		}
	}
	return refs
}

// mdUnescape removes Markdown's backslash escapes.
func mdUnescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return strings.TrimSpace(b.String())
}

// followingCaption is the caption under the image on line i: the next
// non-blank line, when it is wholly emphasised or names a figure.
func followingCaption(lines []string, i int) string {
	for _, line := range lines[i+1:] {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if len(t) > 2 && (t[0] == '*' || t[0] == '_') && t[len(t)-1] == t[0] {
			return strings.TrimSpace(strings.Trim(t, "*_"))
		}
		if isFigureLine(t) {
			return t
		}
		return ""
	}
	return ""
}

// isFigureLine reports whether a line names a figure: a caption.
func isFigureLine(line string) bool {
	return strings.HasPrefix(line, "Figure ") || strings.HasPrefix(line, "Fig. ")
}

// markdownImages resolves a Markdown document's image links into images:
// data URIs decoded, relative paths found through opts.Local, http(s) URLs
// fetched when opts.FetchRemote allows. What cannot be kept is reported.
func markdownImages(ctx context.Context, md string, opts imageOptions) ([]extractedImage, []string) {
	var out []extractedImage
	var warnings []string
	for _, ref := range markdownImageRefs(md) {
		data, err := resolveImageTarget(ctx, ref.Target, opts)
		if err == nil {
			var im extractedImage
			if im, err = newExtractedImage(data, ref.Alt, ref.Caption, sourceOf(ref.Target)); err == nil {
				if !strings.HasPrefix(ref.Target, "data:") {
					im.Name = path.Base(strings.SplitN(ref.Target, "?", 2)[0])
				}
				out = append(out, im)
				continue
			}
		}
		warnings = append(warnings, fmt.Sprintf("image %s skipped: %s", sourceOf(ref.Target), err))
	}
	return out, warnings
}

// sourceOf is a link target as meta and warnings record it: a data URI by
// its type alone, never its bytes.
func sourceOf(target string) string {
	if strings.HasPrefix(target, "data:") {
		if i := strings.IndexAny(target, ";,"); i > 0 {
			return target[:i]
		}
		return "data:"
	}
	return target
}

// errRemoteImagesOff explains a remote image link left alone.
var errRemoteImagesOff = errors.New("remote image not fetched (fetchRemoteImages is off)")

// resolveImageTarget returns the bytes a link target names.
func resolveImageTarget(ctx context.Context, target string, opts imageOptions) ([]byte, error) {
	lower := strings.ToLower(target)
	switch {
	case strings.HasPrefix(lower, "data:"):
		return decodeDataURI(target)
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		if !opts.FetchRemote {
			return nil, errRemoteImagesOff
		}
		fetch := opts.Fetch
		if fetch == nil {
			fetch = fetchImageURL
		}
		return fetch(ctx, target)
	case strings.Contains(target, "://"), strings.HasPrefix(target, "/"):
		return nil, errors.New("only relative paths, data URIs and http(s) URLs are read")
	}
	ref := path.Clean(strings.SplitN(target, "?", 2)[0])
	if ref == ".." || strings.HasPrefix(ref, "../") {
		return nil, errors.New("a path outside the document's directory is not read")
	}
	if opts.Local != nil {
		if data, ok := opts.Local(ref); ok {
			return data, nil
		}
	}
	return nil, errors.New("file not sent with the document")
}

// decodeDataURI decodes a base64 data URI's bytes.
func decodeDataURI(uri string) ([]byte, error) {
	meta, payload, ok := strings.Cut(uri, ",")
	if !ok || !strings.HasSuffix(meta, ";base64") {
		return nil, errors.New("data URI is not base64")
	}
	if base64.StdEncoding.DecodedLen(len(payload)) > maxImageBytes {
		return nil, fmt.Errorf("image larger than %d bytes", maxImageBytes)
	}
	return decodeBase64Any(payload)
}

// decodeBase64Any decodes standard, URL-safe or unpadded base64, the forms
// clients send.
func decodeBase64Any(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		if data, err := enc.DecodeString(s); err == nil {
			return data, nil
		}
	}
	return nil, errors.New("invalid base64")
}

// fetchImageURL downloads an image through the SSRF guard, at most
// maxImageBytes of it.
func fetchImageURL(ctx context.Context, url string) ([]byte, error) {
	data, err := fetchURLBytes(ctx, url, maxImageBytes+1)
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageBytes {
		return nil, fmt.Errorf("image larger than %d bytes", maxImageBytes)
	}
	return data, nil
}
