package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"path"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/htmlindex"
)

// Email (GO-046): EML is one message, MBOX many. The headers people search
// by become meta (from, to, date, subject, messageId); the body becomes the
// document, HTML through the HTML converter, which drops scripts and styles
// and executes nothing; attachments the server can convert become documents
// of their own, with meta.parent naming the message.

const (
	// maxMailDepth bounds MIME nesting: a message inside a message inside a
	// multipart is ordinary, forty levels of it is not.
	maxMailDepth = 8
	// maxMailPart bounds one decoded part.
	maxMailPart = 32 << 20
)

// mailPart is what walking a MIME tree collects.
type mailPart struct {
	plain, html []string
	attachments []mailAttachment
}

// mailAttachment is an attached file.
type mailAttachment struct {
	name string
	data []byte
}

// emlConvert converts one message into c: its Markdown and header meta, and
// its convertible attachments as parts.
func emlConvert(c *conversion, data []byte, textOnly bool, depth int) error {
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("invalid email: %w", err)
	}
	md, meta, parts, warnings := readMail(msg, textOnly, depth)
	c.Markdown, c.Meta, c.Parts = md, meta, parts
	c.Warnings = append(c.Warnings, warnings...)
	return nil
}

// mboxConvert converts a mailbox: the document is the list of its messages,
// and each message is a part of its own, with its attachments under it.
func mboxConvert(c *conversion, data []byte, textOnly bool, depth int) error {
	messages := splitMbox(data)
	if len(messages) == 0 {
		return fmt.Errorf("invalid mbox: no message starts with a \"From \" line")
	}
	var b strings.Builder
	b.WriteString("# Mailbox\n\n")
	for i, raw := range messages {
		suffix := "/message-" + strconv.Itoa(i+1)
		msg := conversion{Format: "eml", Converted: true}
		if err := emlConvert(&msg, raw, textOnly, depth+1); err != nil {
			c.Warnings = append(c.Warnings, fmt.Sprintf("message %d: %s", i+1, err))
			continue
		}
		msg.Confidence, msg.Warnings = assessConversion(raw, msg.Markdown, conversionSignals{Warnings: msg.Warnings})
		for _, p := range msg.Parts {
			p.Suffix = suffix + p.Suffix
			p.ParentSuffix = suffix + p.ParentSuffix
			c.Parts = append(c.Parts, p)
		}
		msg.Parts = nil
		c.Parts = append(c.Parts, conversionPart{Suffix: suffix, conversion: msg})
		fmt.Fprintf(&b, "%d. %s — %s — %s\n", i+1, first(msg.Meta["date"]), first(msg.Meta["from"]), first(msg.Meta["subject"]))
	}
	c.Markdown = strings.TrimSpace(b.String())
	return nil
}

// first is a meta key's first value, or "".
func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// splitMbox cuts a mailbox at its "From " separator lines and undoes the
// mboxrd escaping (">From " in a body).
func splitMbox(data []byte) [][]byte {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	var out [][]byte
	// The first chunk is what precedes the first separator: nothing in a
	// mailbox, and not a message in anything else.
	for _, chunk := range strings.Split("\n"+text, "\nFrom ")[1:] {
		nl := strings.IndexByte(chunk, '\n')
		if nl < 0 || strings.TrimSpace(chunk[nl:]) == "" {
			continue // a separator with no message after it
		}
		var msg strings.Builder
		for _, line := range strings.Split(chunk[nl+1:], "\n") {
			if strings.HasPrefix(strings.TrimLeft(line, ">"), "From ") && strings.HasPrefix(line, ">") {
				line = line[1:]
			}
			msg.WriteString(line + "\n")
		}
		out = append(out, []byte(msg.String()))
	}
	return out
}

// readMail turns a parsed message into Markdown, meta and attachment parts.
func readMail(msg *mail.Message, textOnly bool, depth int) (string, map[string][]string, []conversionPart, []string) {
	dec := mime.WordDecoder{CharsetReader: mailCharsetReader}
	header := func(name string) string {
		v := msg.Header.Get(name)
		if d, err := dec.DecodeHeader(v); err == nil {
			v = d
		}
		return strings.TrimSpace(v)
	}
	meta := map[string][]string{}
	for key, name := range map[string]string{"from": "From", "to": "To", "subject": "Subject"} {
		if v := header(name); v != "" {
			meta[key] = []string{v}
		}
	}
	if v := header("Message-Id"); v != "" {
		meta["messageId"] = []string{strings.Trim(v, "<>")}
	}
	if v := header("Date"); v != "" {
		if t, err := mail.ParseDate(v); err == nil {
			v = t.UTC().Format(time.RFC3339)
		}
		meta["date"] = []string{v}
	}

	var collected mailPart
	var warnings []string
	walkMIME(msg.Header, msg.Body, depth, &collected, &warnings)

	var b strings.Builder
	if s := first(meta["subject"]); s != "" {
		b.WriteString("# " + s + "\n\n")
	}
	for _, h := range []struct{ label, key string }{{"From", "from"}, {"To", "to"}, {"Date", "date"}} {
		if v := first(meta[h.key]); v != "" {
			b.WriteString("- **" + h.label + ":** " + v + "\n")
		}
	}
	b.WriteString("\n")
	switch {
	case len(collected.plain) > 0:
		b.WriteString(strings.Join(collected.plain, "\n\n"))
	case len(collected.html) > 0:
		for _, h := range collected.html {
			if textOnly {
				b.WriteString(htmlToText([]byte(h)) + "\n\n")
			} else {
				b.WriteString(htmlToMarkdown([]byte(h)) + "\n\n")
			}
		}
	}

	parts, names, attWarnings := mailAttachmentParts(collected.attachments, textOnly, depth)
	warnings = append(warnings, attWarnings...)
	if len(names) > 0 {
		b.WriteString("\n\n**Attachments:** " + strings.Join(names, ", "))
		meta["attachments"] = names
	}
	return strings.TrimSpace(b.String()), meta, parts, warnings
}

// mailAttachmentParts converts the attachments the server has a converter
// for into parts, and lists the names of all of them.
func mailAttachmentParts(atts []mailAttachment, textOnly bool, depth int) ([]conversionPart, []string, []string) {
	var parts []conversionPart
	var names, warnings []string
	used := map[string]int{}
	for _, a := range atts {
		names = append(names, a.name)
		conv, err := convertDepth(formatFromFilename(a.name), a.data, textOnly, depth+1)
		if errors.Is(err, errUnsupportedFormat) {
			continue // a picture, an archive: listed, not converted
		}
		if err != nil {
			warnings = append(warnings, "attachment "+a.name+": "+err.Error())
			continue
		}
		slug := attachmentSlug(a.name)
		used[slug]++
		if used[slug] > 1 {
			slug += "-" + strconv.Itoa(used[slug])
		}
		suffix := "/attachments/" + slug
		if conv.Meta == nil {
			conv.Meta = map[string][]string{}
		}
		conv.Meta["filename"] = []string{a.name}
		for _, p := range conv.Parts {
			p.Suffix = suffix + p.Suffix
			p.ParentSuffix = suffix + p.ParentSuffix
			parts = append(parts, p)
		}
		conv.Parts = nil
		parts = append(parts, conversionPart{Suffix: suffix, conversion: conv})
	}
	return parts, names, warnings
}

// attachmentSlug makes an attachment's file name usable in a document key.
func attachmentSlug(name string) string {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.Join(strings.Fields(strings.ToLower(name)), "-")
	if name == "" || name == "." || name == ".." || name == "/" {
		return "attachment"
	}
	return name
}

// walkMIME collects the text bodies and attachments of one MIME entity.
func walkMIME(h map[string][]string, body io.Reader, depth int, out *mailPart, warnings *[]string) {
	get := func(k string) string {
		if v := h[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	mediaType, params, err := mime.ParseMediaType(get("Content-Type"))
	if err != nil {
		mediaType, params = "text/plain", map[string]string{}
	}
	data, err := io.ReadAll(io.LimitReader(transferDecoder(get("Content-Transfer-Encoding"), body), maxMailPart))
	if err != nil {
		*warnings = append(*warnings, "a part could not be decoded: "+err.Error())
	}
	disposition, dparams, _ := mime.ParseMediaType(get("Content-Disposition"))
	name := dparams["filename"]
	if name == "" {
		name = params["name"]
	}

	switch {
	case strings.HasPrefix(mediaType, "multipart/"):
		if depth >= maxMailDepth {
			*warnings = append(*warnings, "MIME nesting deeper than "+strconv.Itoa(maxMailDepth)+" levels was not read")
			return
		}
		walkMultipart(mediaType, params["boundary"], data, depth, out, warnings)
	case mediaType == "message/rfc822":
		if name == "" {
			name = "message.eml"
		}
		out.attachments = append(out.attachments, mailAttachment{name: name, data: data})
	case disposition == "attachment" || (name != "" && !strings.HasPrefix(mediaType, "text/")):
		if name == "" {
			name = "attachment"
		}
		out.attachments = append(out.attachments, mailAttachment{name: name, data: data})
	case len(bytes.TrimSpace(data)) == 0:
		// An empty body, or one that did not decode, is no alternative to
		// the bodies that have text.
	case mediaType == "text/html":
		out.html = append(out.html, mailText(data, params["charset"]))
	case strings.HasPrefix(mediaType, "text/"):
		out.plain = append(out.plain, strings.TrimSpace(mailText(data, params["charset"])))
	}
}

// walkMultipart reads the parts of a multipart body. In
// multipart/alternative the plain text wins over the HTML when both exist —
// they say the same thing, and plain text needs no converting.
func walkMultipart(mediaType, boundary string, data []byte, depth int, out *mailPart, warnings *[]string) {
	mr := multipart.NewReader(bytes.NewReader(data), boundary)
	var alt mailPart
	target := out
	if mediaType == "multipart/alternative" {
		target = &alt
	}
	for {
		p, err := mr.NextRawPart()
		if err != nil {
			break
		}
		walkMIME(p.Header, p, depth+1, target, warnings)
	}
	if target == &alt {
		if len(alt.plain) > 0 {
			out.plain = append(out.plain, alt.plain...)
		} else {
			out.html = append(out.html, alt.html...)
		}
		out.attachments = append(out.attachments, alt.attachments...)
	}
}

// transferDecoder undoes a Content-Transfer-Encoding.
func transferDecoder(encoding string, r io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, r)
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	}
	return r
}

// mailText is a text part as UTF-8 with Unix line ends: mail travels with
// CRLF, a stored document should not.
func mailText(data []byte, charset string) string {
	return strings.ReplaceAll(decodeCharset(data, charset), "\r\n", "\n")
}

// decodeCharset decodes text in a declared charset to UTF-8; unknown
// charsets are left as they are, for the ingest path to sanitise.
func decodeCharset(data []byte, charset string) string {
	if r, err := mailCharsetReader(charset, bytes.NewReader(data)); err == nil {
		if out, err := io.ReadAll(r); err == nil {
			return string(out)
		}
	}
	return string(data)
}

// mailCharsetReader reads input in the named charset as UTF-8.
func mailCharsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return input, nil
	}
	enc, err := htmlindex.Get(charset)
	if err != nil {
		return nil, err
	}
	return enc.NewDecoder().Reader(input), nil
}
