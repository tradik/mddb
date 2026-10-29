package main

import (
	"regexp"
	"strconv"
	"strings"
)

// Which page of a PDF draws which image, and what the page calls it
// (RAG-010). Pages are taken in the order of the page tree (/Kids from the
// root /Pages), or in file order when there is no tree to follow; a page's
// images are the image XObjects its resources name, directly or through an
// indirect /Resources or /XObject dictionary. Inherited resources and images
// drawn inside a form XObject are not followed: such an image gets no page.

var (
	pdfPagesRe    = regexp.MustCompile(`/Type\s*/Pages\b`)
	pdfParentRe   = regexp.MustCompile(`/Parent\s+\d+\s+\d+\s+R`)
	pdfKidsRe     = regexp.MustCompile(`/Kids\s*\[([^\]]*)\]`)
	pdfIndirectRe = regexp.MustCompile(`/(Resources|XObject)\s+(\d+)\s+\d+\s+R`)
	pdfContentsRe = regexp.MustCompile(`/Contents\s*(\[[^\]]*\]|\d+\s+\d+\s+R)`)
)

// pdfPage is one page in document order.
type pdfPage struct {
	index int // 1-based
	obj   *pdfObject
}

// pdfPageList lists the pages in document order.
func pdfPageList(objs []*pdfObject, byNum map[int]*pdfObject) []pdfPage {
	var order []*pdfObject
	seen := map[int]bool{}
	var walk func(o *pdfObject, depth int)
	walk = func(o *pdfObject, depth int) {
		if o == nil || seen[o.num] || depth > 32 {
			return
		}
		seen[o.num] = true
		if pdfPageRe.MatchString(o.dict) && !pdfPagesRe.MatchString(o.dict) {
			order = append(order, o)
			return
		}
		if m := pdfKidsRe.FindStringSubmatch(o.dict); m != nil {
			for _, ref := range pdfRefRe.FindAllStringSubmatch(m[1], -1) {
				n, _ := strconv.Atoi(ref[1])
				walk(byNum[n], depth+1)
			}
		}
	}
	for _, o := range objs {
		if pdfPagesRe.MatchString(o.dict) && !pdfParentRe.MatchString(o.dict) {
			walk(o, 0)
			break
		}
	}
	if len(order) == 0 {
		for _, o := range objs {
			if pdfPageRe.MatchString(o.dict) && !pdfPagesRe.MatchString(o.dict) {
				order = append(order, o)
			}
		}
	}
	pages := make([]pdfPage, len(order))
	for i, o := range order {
		pages[i] = pdfPage{index: i + 1, obj: o}
	}
	return pages
}

// pdfResourceText is a page's dictionary with its indirect /Resources and
// /XObject dictionaries appended, where its image references are.
func pdfResourceText(page *pdfObject, byNum map[int]*pdfObject) string {
	text := page.dict
	for range 2 { // Resources, then the XObject dictionary inside it
		for _, m := range pdfIndirectRe.FindAllStringSubmatch(text, -1) {
			n, _ := strconv.Atoi(m[2])
			if o := byNum[n]; o != nil && !strings.Contains(text, "\x00"+m[2]+"\x00") {
				text += "\x00" + m[2] + "\x00" + o.dict
			}
		}
	}
	return text
}

// pdfImagePages maps each image object a page names to that page's number,
// the first page that names it winning, and returns the pages.
func pdfImagePages(objs []*pdfObject, byNum map[int]*pdfObject) (map[int]int, []pdfPage) {
	pages := pdfPageList(objs, byNum)
	pageOf := map[int]int{}
	for _, p := range pages {
		for _, ref := range pdfRefRe.FindAllStringSubmatch(pdfResourceText(p.obj, byNum), -1) {
			n, _ := strconv.Atoi(ref[1])
			if _, set := pageOf[n]; !set {
				pageOf[n] = p.index
			}
		}
	}
	return pageOf, pages
}

// pdfPageCaptions are the lines of a page's text that name a figure.
func pdfPageCaptions(page *pdfObject, byNum map[int]*pdfObject) []string {
	m := pdfContentsRe.FindStringSubmatch(page.dict)
	if m == nil {
		return nil
	}
	var text strings.Builder
	for _, ref := range pdfRefRe.FindAllStringSubmatch(m[1], -1) {
		n, _ := strconv.Atoi(ref[1])
		o := byNum[n]
		if o == nil || o.stream == nil {
			continue
		}
		if content, ok := pdfStreamContent(o.dict, string(o.stream)); ok && pdfHasTextOperators(content) {
			extractPDFText(content, &text)
			text.WriteByte('\n')
		}
	}
	var captions []string
	for _, line := range strings.Split(latin1Fallback(text.String()), "\n") {
		if line = strings.TrimSpace(line); isFigureLine(line) {
			captions = append(captions, line)
		}
	}
	return captions
}

// assignPDFCaptions gives each page's images that page's figure captions, in
// order; an image past the last caption keeps none.
func assignPDFCaptions(images []extractedImage, pages []pdfPage, byNum map[int]*pdfObject) {
	next := map[int]int{}
	captions := map[int][]string{}
	for i := range images {
		p := images[i].Page
		if p <= 0 || p > len(pages) {
			continue
		}
		if _, read := captions[p]; !read {
			captions[p] = pdfPageCaptions(pages[p-1].obj, byNum)
		}
		if k := next[p]; k < len(captions[p]) {
			images[i].Caption = captions[p][k]
			next[p] = k + 1
		}
	}
}
