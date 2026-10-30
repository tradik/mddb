package main

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Presentations (GO-046): one section per slide, in the deck's order, with
// the speaker notes — which is often where the actual argument of a deck is
// written down.

// slide is one slide's title, body paragraphs and notes.
type slide struct {
	title string
	body  []string
	notes []string
}

// slidesToMarkdown renders slides as sections.
func slidesToMarkdown(slides []slide) (string, conversionSignals) {
	sig := conversionSignals{Container: true}
	var b strings.Builder
	empty := 0
	for i, s := range slides {
		b.WriteString("## Slide " + strconv.Itoa(i+1))
		if s.title != "" {
			b.WriteString(": " + s.title)
		}
		b.WriteString("\n\n")
		for _, p := range s.body {
			b.WriteString(p + "\n\n")
		}
		if len(s.notes) > 0 {
			b.WriteString("**Notes:** " + strings.Join(s.notes, "\n\n") + "\n\n")
		}
		if s.title == "" && len(s.body) == 0 && len(s.notes) == 0 {
			empty++
		}
	}
	if empty > 0 {
		sig.Warnings = append(sig.Warnings, fmt.Sprintf("%d of %d slides have no text (pictures only?)", empty, len(slides)))
	}
	return strings.TrimSpace(b.String()), sig
}

// pptxToMarkdown converts a PPTX deck: slides in presentation order, each
// with its notes slide.
func pptxToMarkdown(data []byte) (string, conversionSignals, error) {
	z, err := openZipDoc(data, "pptx")
	if err != nil {
		return "", conversionSignals{}, err
	}
	var pres struct {
		// r:id, in the relationships namespace: sldId also has a plain id.
		Slides []struct {
			RID string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sldIdLst>sldId"`
	}
	if err := z.readXML("ppt/presentation.xml", &pres); err != nil {
		return "", conversionSignals{}, err
	}
	targets, _ := z.relTargets("ppt/presentation.xml")
	var slides []slide
	for _, ref := range pres.Slides {
		part := targets[ref.RID]
		raw, err := z.read(part)
		if err != nil {
			return "", conversionSignals{}, err
		}
		s := slide{}
		paras, err := pptxParagraphs(raw, map[string]bool{"sldNum": true, "dt": true, "ftr": true})
		if err != nil {
			return "", conversionSignals{}, err
		}
		for _, p := range paras {
			if p.title && s.title == "" {
				s.title = p.text
			} else {
				s.body = append(s.body, p.text)
			}
		}
		if _, byType := z.relTargets(part); byType["notesSlide"] != "" {
			if notes, err := z.read(byType["notesSlide"]); err == nil {
				paras, _ := pptxParagraphs(notes, map[string]bool{"sldNum": true, "sldImg": true, "hdr": true, "ftr": true, "dt": true})
				for _, p := range paras {
					s.notes = append(s.notes, p.text)
				}
			}
		}
		slides = append(slides, s)
	}
	md, sig := slidesToMarkdown(slides)
	return md, sig, nil
}

// slideParagraph is a paragraph of text and whether it is the slide title.
type slideParagraph struct {
	text  string
	title bool
}

// pptxParagraphs reads the a:p paragraphs of a slide or notes part, leaving
// out the placeholders named in skip (slide numbers, dates, footers).
func pptxParagraphs(raw []byte, skip map[string]bool) ([]slideParagraph, error) {
	dec := xml.NewDecoder(strings.NewReader(string(raw)))
	var out []slideParagraph
	var ph string // the placeholder type of the shape being read
	var para *strings.Builder
	inText := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, errors.New("invalid pptx: " + err.Error())
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "sp", "graphicFrame":
				ph = ""
			case "ph":
				ph = odfAttr(t, "type")
				if ph == "" {
					ph = "body"
				}
			case "p":
				para = &strings.Builder{}
			case "t":
				inText = true
			case "br":
				if para != nil {
					para.WriteByte(' ')
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				if para == nil {
					continue
				}
				if text := strings.Join(strings.Fields(para.String()), " "); text != "" && !skip[ph] {
					out = append(out, slideParagraph{text: text, title: ph == "title" || ph == "ctrTitle"})
				}
				para = nil
			}
		case xml.CharData:
			if inText && para != nil {
				para.Write(t)
			}
		}
	}
}

// odpToMarkdown converts an ODP presentation: draw:page elements in order,
// the title frame as the section title, presentation:notes as notes.
func odpToMarkdown(data []byte) (string, conversionSignals, error) {
	z, err := openZipDoc(data, "odp")
	if err != nil {
		return "", conversionSignals{}, err
	}
	content, err := z.read("content.xml")
	if err != nil {
		return "", conversionSignals{}, err
	}
	slides, err := odpSlides(content)
	if err != nil {
		return "", conversionSignals{}, err
	}
	md, sig := slidesToMarkdown(slides)
	return md, sig, nil
}

// odpSlides streams content.xml into slides.
func odpSlides(content []byte) ([]slide, error) {
	dec := xml.NewDecoder(strings.NewReader(string(content)))
	r := &odpReader{}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return r.slides, nil
		}
		if err != nil {
			return nil, errors.New("invalid odp: content.xml: " + err.Error())
		}
		r.token(tok)
	}
}

// odpReader is odpSlides' state between tokens: the slides so far, whether
// the reader is in the notes or a title frame, and the paragraph being read.
type odpReader struct {
	slides  []slide
	inNotes bool
	isTitle bool
	para    *strings.Builder
}

// token feeds one token of content.xml to the reader.
func (r *odpReader) token(tok xml.Token) {
	switch t := tok.(type) {
	case xml.StartElement:
		r.start(t)
	case xml.EndElement:
		r.end(t)
	case xml.CharData:
		if r.para != nil {
			r.para.Write(t)
		}
	}
}

// start opens a slide, the notes, a frame or a paragraph, or adds the space
// a tab or line break stands for.
func (r *odpReader) start(t xml.StartElement) {
	switch t.Name.Local {
	case "page":
		r.slides = append(r.slides, slide{})
	case "notes":
		r.inNotes = true
	case "frame":
		r.isTitle = odfAttr(t, "class") == "title"
	case "p", "h":
		r.para = &strings.Builder{}
	case "s", "tab", "line-break":
		if r.para != nil {
			r.para.WriteByte(' ')
		}
	}
}

// end closes the notes, a frame or a paragraph.
func (r *odpReader) end(t xml.EndElement) {
	switch t.Name.Local {
	case "notes":
		r.inNotes = false
	case "frame":
		r.isTitle = false
	case "p", "h":
		r.endParagraph()
	}
}

// endParagraph files the finished paragraph on the current slide: as a note,
// as the slide's title (the first paragraph of a title frame), or as body.
func (r *odpReader) endParagraph() {
	if r.para == nil {
		return
	}
	text := strings.Join(strings.Fields(r.para.String()), " ")
	r.para = nil
	if text == "" || len(r.slides) == 0 {
		return
	}
	s := &r.slides[len(r.slides)-1]
	switch {
	case r.inNotes:
		s.notes = append(s.notes, text)
	case r.isTitle && s.title == "":
		s.title = text
	default:
		s.body = append(s.body, text)
	}
}
