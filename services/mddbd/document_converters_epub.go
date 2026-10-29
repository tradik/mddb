package main

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

// EPUB (GO-046): the chapters in spine order — the reading order the book
// declares, not the order its files happen to sit in the zip — each through
// the HTML converter.

// epubToMarkdown converts an EPUB book.
func epubToMarkdown(data []byte, textOnly bool) (string, conversionSignals, error) {
	sig := conversionSignals{Container: true}
	z, err := openZipDoc(data, "epub")
	if err != nil {
		return "", sig, err
	}
	var container struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := z.readXML("META-INF/container.xml", &container); err != nil {
		return "", sig, err
	}
	if len(container.Rootfiles) == 0 {
		return "", sig, errors.New("invalid epub: META-INF/container.xml names no package")
	}
	opfPath := container.Rootfiles[0].FullPath
	var opf struct {
		Title    string `xml:"metadata>title"`
		Manifest []struct {
			ID   string `xml:"id,attr"`
			Href string `xml:"href,attr"`
		} `xml:"manifest>item"`
		Spine []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"spine>itemref"`
	}
	if err := z.readXML(opfPath, &opf); err != nil {
		return "", sig, err
	}
	hrefs := make(map[string]string, len(opf.Manifest))
	for _, item := range opf.Manifest {
		hrefs[item.ID] = item.Href
	}

	var b strings.Builder
	if title := strings.TrimSpace(opf.Title); title != "" {
		b.WriteString("# " + title + "\n\n")
	}
	missing := 0
	for _, ref := range opf.Spine {
		href, err := url.PathUnescape(hrefs[ref.IDRef])
		if err != nil || href == "" {
			missing++
			continue
		}
		chapter, err := z.read(path.Join(path.Dir(opfPath), href))
		if err != nil {
			missing++
			continue
		}
		// The head holds the chapter's <title>, a repeat of its heading.
		chapter = []byte(stripTagBlock(string(chapter), "head"))
		text := htmlToMarkdown(chapter)
		if textOnly {
			text = htmlToText(chapter)
		}
		if text != "" {
			b.WriteString(text + "\n\n")
		}
	}
	if missing > 0 {
		sig.Warnings = append(sig.Warnings, fmt.Sprintf("%d of %d spine items could not be read", missing, len(opf.Spine)))
	}
	return strings.TrimSpace(b.String()), sig, nil
}
