package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

// Email edge cases (GO-046): what is listed but not converted, what is
// converted and nested, and what a malformed message turns into.

// mailWith builds a multipart/mixed message with the given parts, each a
// header block and a body.
func mailWith(parts ...string) []byte {
	var b strings.Builder
	b.WriteString("From: a@example.org\r\nSubject: Parts\r\nDate: not a date\r\nContent-Type: multipart/mixed; boundary=\"b\"\r\n\r\n")
	for i := 0; i+1 < len(parts); i += 2 {
		b.WriteString("--b\r\n" + parts[i] + "\r\n\r\n" + parts[i+1] + "\r\n")
	}
	b.WriteString("--b--\r\n")
	return []byte(b.String())
}

func TestEmailAttachments(t *testing.T) {
	pdf := base64.StdEncoding.EncodeToString([]byte("%PDF-1.4\nstream\nBT (Invoice 7) Tj ET\nendstream\n"))
	inner := "From: b@example.org\r\nSubject: Inner\r\nContent-Type: multipart/mixed; boundary=\"i\"\r\n\r\n" +
		"--i\r\nContent-Type: text/plain\r\n\r\ninner body\r\n" +
		"--i\r\nContent-Type: text/csv\r\nContent-Disposition: attachment; filename=\"t.csv\"\r\n\r\nx,y\r\n1,2\r\n--i--\r\n"
	msg := mailWith(
		"Content-Type: text/html; charset=windows-1252", "<p>Caf\xe9 <b>menu</b></p><script>x()</script>",
		"Content-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"Invoice 7.pdf\"\r\nContent-Transfer-Encoding: base64", pdf,
		"Content-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"Invoice 7.pdf\"\r\nContent-Transfer-Encoding: base64", pdf,
		"Content-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"broken.pdf\"", "not a pdf",
		"Content-Type: application/octet-stream\r\nContent-Disposition: attachment\r\nContent-Transfer-Encoding: base64", "AAE=",
		"Content-Type: image/png; name=\"pic.png\"\r\nContent-Transfer-Encoding: base64", "iVBORw0=",
		"Content-Type: message/rfc822", inner,
		"Content-Type: text/plain; charset=x-unknown-charset\r\nContent-Disposition: attachment; filename=\"notes.txt\"", "plain notes",
		"Content-Type: text/plain\r\nContent-Transfer-Encoding: base64", "!!!not base64",
	)
	c, err := convertToMarkdown("eml", msg, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.Markdown, "Café **menu**") || strings.Contains(c.Markdown, "x()") || c.Meta["date"][0] != "not a date" {
		t.Errorf("body: %q %v", c.Markdown, c.Meta)
	}
	names := strings.Join(c.Meta["attachments"], ",")
	if names != "Invoice 7.pdf,Invoice 7.pdf,broken.pdf,attachment,pic.png,message.eml,notes.txt" {
		t.Errorf("attachments: %s", names)
	}
	var keys []string
	for _, p := range c.Parts {
		keys = append(keys, p.Suffix+"<"+p.ParentSuffix)
	}
	want := "/attachments/invoice-7.pdf<,/attachments/invoice-7.pdf-2<,/attachments/message.eml/attachments/t.csv</attachments/message.eml," +
		"/attachments/message.eml<,/attachments/notes.txt<"
	if strings.Join(keys, ",") != want {
		t.Errorf("parts:\n got %s\nwant %s", strings.Join(keys, ","), want)
	}
	warnings := strings.Join(c.Warnings, "|")
	if !strings.Contains(warnings, "attachment broken.pdf: pdf conversion") || !strings.Contains(warnings, "a part could not be decoded") {
		t.Errorf("warnings: %v", c.Warnings)
	}

	// Text-only keeps the words of an HTML body.
	if c, err := convertToMarkdown("eml", mailWith("Content-Type: text/html", "<h1>Big</h1>"), true); err != nil || !strings.Contains(c.Markdown, "Big") || strings.Contains(c.Markdown, "# Big") {
		t.Errorf("text-only: %q %v", c.Markdown, err)
	}
}

func TestEmailLimitsAndErrors(t *testing.T) {
	if _, err := convertToMarkdown("eml", []byte("no header line\x01"), false); err == nil || !strings.Contains(err.Error(), "eml conversion: invalid email") {
		t.Errorf("not a message: %v", err)
	}
	if _, err := convertToMarkdown("mbox", []byte("just text\n"), false); err == nil || !strings.Contains(err.Error(), "no message starts") {
		t.Errorf("not a mailbox: %v", err)
	}
	// A mailbox whose second message is not one.
	c, err := convertToMarkdown("mbox", []byte("From a\nSubject: ok\n\nbody\n\nFrom b\n\x01broken header\n\nx\n"), false)
	if err != nil || len(c.Parts) != 1 || !strings.Contains(strings.Join(c.Warnings, " "), "message 2: invalid email") {
		t.Errorf("broken message: %+v %v", c, err)
	}
	// MIME nested past the limit is reported, not followed.
	body := "deep"
	for i := 0; i < maxMailDepth+1; i++ {
		body = "Content-Type: multipart/mixed; boundary=\"n" + string(rune('a'+i)) + "\"\r\n\r\n--n" + string(rune('a'+i)) + "\r\n" + body + "\r\n--n" + string(rune('a'+i)) + "--"
	}
	c, err = convertToMarkdown("eml", []byte("From: x@y\r\n"+body), false)
	if err != nil || !strings.Contains(strings.Join(c.Warnings, " "), "MIME nesting deeper than") {
		t.Errorf("deep MIME: %v %v", c.Warnings, err)
	}
	// An unparseable Content-Type is read as plain text.
	c, err = convertToMarkdown("eml", []byte("From: x@y\r\nContent-Type: ;;;\r\n\r\nstill text"), false)
	if err != nil || !strings.Contains(c.Markdown, "still text") {
		t.Errorf("bad content type: %q %v", c.Markdown, err)
	}
	for in, want := range map[string]string{"": "attachment", "..": "attachment", `C:\docs\Q3 Plan.PDF`: "q3-plan.pdf", "a/b.txt": "b.txt"} {
		if got := attachmentSlug(in); got != want {
			t.Errorf("attachmentSlug(%q) = %q, want %q", in, got, want)
		}
	}
	if first(nil) != "" || decodeCharset([]byte("x"), "no-such-charset") != "x" {
		t.Error("helpers")
	}
}
