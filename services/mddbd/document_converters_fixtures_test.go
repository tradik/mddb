package main

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// Conversion fixtures (GO-046). Every file in testdata/convert is written by
// the generator below — none is downloaded or copied from elsewhere — and
// each has a golden Markdown file beside it. Regenerate both with
//
//	go test -tags noasm -run TestConvertFixturesGolden -update-convert-fixtures
//
// and review the diff of the .md files: that diff is what the change did to
// conversion.

var updateConvertFixtures = flag.Bool("update-convert-fixtures", false, "rewrite testdata/convert and its golden files")

const convertFixtureDir = "testdata/convert"

// fixtureTime is the modification time written into every zip, so a
// regenerated fixture differs only where its content does.
var fixtureTime = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

// fixtureZip builds a zip from name/content pairs in order. The first entry
// is stored uncompressed when it is an OpenDocument or EPUB mimetype, as
// those formats require.
func fixtureZip(entries ...string) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for i := 0; i+1 < len(entries); i += 2 {
		method := zip.Deflate
		if entries[i] == "mimetype" {
			method = zip.Store
		}
		f, err := w.CreateHeader(&zip.FileHeader{Name: entries[i], Method: method, Modified: fixtureTime})
		if err != nil {
			panic(err)
		}
		if _, err := f.Write([]byte(entries[i+1])); err != nil {
			panic(err)
		}
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// fixturePDF writes a PDF whose pages each have one content stream; a page
// given as "" draws only an image, the way a scanner's output does.
func fixturePDF(pages []string, compress bool) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	kids := make([]string, len(pages))
	for i := range pages {
		kids[i] = fmt.Sprintf("%d 0 R", 3+2*i)
	}
	fmt.Fprintf(&b, "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	fmt.Fprintf(&b, "2 0 obj\n<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n", strings.Join(kids, " "), len(pages))
	image := 3 + 2*len(pages)
	for i, text := range pages {
		page, content := 3+2*i, 4+2*i
		fmt.Fprintf(&b, "%d 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents %d 0 R /Resources << /XObject << /Im1 %d 0 R >> >> >>\nendobj\n", page, content, image)
		stream := "q 612 0 0 792 0 0 cm /Im1 Do Q"
		if text != "" {
			var ops []string
			for j, line := range strings.Split(text, "\n") {
				ops = append(ops, fmt.Sprintf("72 %d Td (%s) Tj", 720-14*j, line))
			}
			stream = "BT /F1 12 Tf " + strings.Join(ops, " ") + " ET"
		}
		dict := fmt.Sprintf("/Length %d", len(stream))
		if compress {
			var z bytes.Buffer
			zw := zlib.NewWriter(&z)
			_, _ = zw.Write([]byte(stream))
			_ = zw.Close()
			stream = z.String()
			dict = fmt.Sprintf("/Length %d /Filter /FlateDecode", len(stream))
		}
		fmt.Fprintf(&b, "%d 0 obj\n<< %s >>\nstream\n%s\nendstream\nendobj\n", content, dict, stream)
	}
	// A greyscale "scan": noise standing in for JPEG data.
	noise := make([]byte, 4096)
	_, _ = rand.New(rand.NewSource(46)).Read(noise) // #nosec G404 -- deterministic fixture bytes, not a secret
	fmt.Fprintf(&b, "%d 0 obj\n<< /Type /XObject /Subtype /Image /Width 64 /Height 64 /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n%s\nendstream\nendobj\n", image, len(noise), noise)
	b.WriteString("trailer\n<< /Root 1 0 R >>\n%%EOF\n")
	return b.Bytes()
}

const (
	ooxmlRelNS = `xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	odfNS      = `xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0" xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0" xmlns:presentation="urn:oasis:names:tc:opendocument:xmlns:presentation:1.0"`
	pmlNS      = `xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" ` + ooxmlRelNS
)

// fixtureXLSX is a workbook with text, numbers, a boolean, dates in a
// built-in and a custom format, and an empty second sheet.
func fixtureXLSX() []byte {
	return fixtureZip(
		"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/></Types>`,
		"xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" `+ooxmlRelNS+`><workbookPr/><sheets><sheet name="Orders" sheetId="1" r:id="rId1"/><sheet name="Notes" sheetId="2" r:id="rId2"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="/xl/worksheets/sheet2.xml"/></Relationships>`,
		"xl/sharedStrings.xml", `<?xml version="1.0" encoding="UTF-8"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><t>Customer</t></si><si><t>Ordered</t></si><si><t>Amount</t></si><si><t>Paid</t></si><si><r><t>Acme </t></r><r><t>Ltd | North</t></r></si><si><t>Shipped</t></si></sst>`,
		"xl/styles.xml", `<?xml version="1.0" encoding="UTF-8"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><numFmts><numFmt numFmtId="164" formatCode="yyyy\-mm\-dd\ hh:mm"/><numFmt numFmtId="165" formatCode="&quot;EUR&quot;\ #,##0.00"/></numFmts><cellXfs><xf numFmtId="0"/><xf numFmtId="14"/><xf numFmtId="164"/><xf numFmtId="165"/></cellXfs></styleSheet>`,
		"xl/worksheets/sheet1.xml", `<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`+
			`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="s"><v>2</v></c><c r="D1" t="s"><v>3</v></c><c r="E1" t="s"><v>5</v></c></row>`+
			`<row r="2"><c r="A2" t="s"><v>4</v></c><c r="B2" s="1"><v>45658</v></c><c r="C2" s="3"><v>1250.5</v></c><c r="D2" t="b"><v>1</v></c><c r="E2" s="2"><v>45659.75</v></c></row>`+
			`<row r="3"><c r="A3" t="inlineStr"><is><t>Brightwater</t></is></c><c r="B3" s="1"><v>45700</v></c><c r="C3"><v>99</v></c><c r="D3" t="b"><v>0</v></c></row>`+
			`</sheetData></worksheet>`,
		"xl/worksheets/sheet2.xml", `<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData/></worksheet>`,
	)
}

// fixtureODS is a spreadsheet with a date, a time, a repeated cell and a
// comment that is not part of the cell's value.
func fixtureODS() []byte {
	return fixtureZip(
		"mimetype", "application/vnd.oasis.opendocument.spreadsheet",
		"content.xml", `<?xml version="1.0" encoding="UTF-8"?><office:document-content `+odfNS+`><office:body><office:spreadsheet>`+
			`<table:table table:name="Rota"><table:table-row><table:table-cell office:value-type="string"><text:p>Day</text:p></table:table-cell><table:table-cell office:value-type="string"><text:p>Starts</text:p></table:table-cell><table:table-cell office:value-type="string"><text:p>Team</text:p></table:table-cell><table:table-cell table:number-columns-repeated="1020"/></table:table-row>`+
			`<table:table-row><table:table-cell office:value-type="date" office:date-value="2026-11-02"><text:p>02/11/26</text:p></table:table-cell><table:table-cell office:value-type="time" office:time-value="PT08H30M00S"><text:p>08:30 AM</text:p></table:table-cell><table:table-cell office:value-type="string"><office:annotation><text:p>check with Ana</text:p></office:annotation><text:p>Platform<text:s/>team</text:p></table:table-cell></table:table-row>`+
			`<table:table-row table:number-rows-repeated="2"><table:table-cell office:value-type="date" office:date-value="2026-11-03T14:00:00"><text:p>03/11/26 14:00</text:p></table:table-cell><table:table-cell table:number-columns-repeated="2" office:value-type="string"><text:p>tbd</text:p></table:table-cell></table:table-row>`+
			`<table:table-row table:number-rows-repeated="1048570"><table:table-cell table:number-columns-repeated="1024"/></table:table-row>`+
			`</table:table></office:spreadsheet></office:body></office:document-content>`,
	)
}

// fixturePPTX is a two-slide deck with titles, body text, a slide number and
// speaker notes.
func fixturePPTX() []byte {
	slide := func(title, body string) string {
		return `<?xml version="1.0" encoding="UTF-8"?><p:sld ` + pmlNS + `><p:cSld><p:spTree>` +
			`<p:sp><p:nvSpPr><p:nvPr><p:ph type="title"/></p:nvPr></p:nvSpPr><p:txBody><a:p><a:r><a:t>` + title + `</a:t></a:r></a:p></p:txBody></p:sp>` +
			`<p:sp><p:nvSpPr><p:nvPr><p:ph idx="1"/></p:nvPr></p:nvSpPr><p:txBody>` + body + `</p:txBody></p:sp>` +
			`<p:sp><p:nvSpPr><p:nvPr><p:ph type="sldNum"/></p:nvPr></p:nvSpPr><p:txBody><a:p><a:r><a:t>7</a:t></a:r></a:p></p:txBody></p:sp>` +
			`</p:spTree></p:cSld></p:sld>`
	}
	notes := func(text string) string {
		return `<?xml version="1.0" encoding="UTF-8"?><p:notes ` + pmlNS + `><p:cSld><p:spTree>` +
			`<p:sp><p:nvSpPr><p:nvPr><p:ph type="sldImg"/></p:nvPr></p:nvSpPr></p:sp>` +
			`<p:sp><p:nvSpPr><p:nvPr><p:ph type="body" idx="1"/></p:nvPr></p:nvSpPr><p:txBody><a:p><a:r><a:t>` + text + `</a:t></a:r></a:p></p:txBody></p:sp>` +
			`</p:spTree></p:cSld></p:notes>`
	}
	rels := func(target string) string {
		return `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesSlide" Target="` + target + `"/></Relationships>`
	}
	return fixtureZip(
		"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
		"ppt/presentation.xml", `<?xml version="1.0" encoding="UTF-8"?><p:presentation `+pmlNS+`><p:sldIdLst><p:sldId id="256" r:id="rId7"/><p:sldId id="257" r:id="rId6"/></p:sldIdLst></p:presentation>`,
		"ppt/_rels/presentation.xml.rels", `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId6" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide2.xml"/><Relationship Id="rId7" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide1.xml"/></Relationships>`,
		"ppt/slides/slide1.xml", slide("Quarterly review", `<a:p><a:r><a:t>Revenue up 12%</a:t></a:r></a:p><a:p><a:r><a:t>Churn </a:t></a:r><a:br/><a:r><a:t>down to 3%</a:t></a:r></a:p>`),
		"ppt/slides/_rels/slide1.xml.rels", rels("../notesSlides/notesSlide1.xml"),
		"ppt/notesSlides/notesSlide1.xml", notes("Mention the Lisbon office first."),
		"ppt/slides/slide2.xml", slide("Next steps", `<a:p><a:r><a:t>Hire two engineers</a:t></a:r></a:p>`),
	)
}

// fixtureODP is a two-page presentation with notes on the first page.
func fixtureODP() []byte {
	page := func(name, title, body, notes string) string {
		s := `<draw:page draw:name="` + name + `"><draw:frame presentation:class="title"><draw:text-box><text:p>` + title + `</text:p></draw:text-box></draw:frame>` +
			`<draw:frame presentation:class="outline"><draw:text-box><text:list><text:list-item><text:p>` + body + `</text:p></text:list-item></text:list></draw:text-box></draw:frame>`
		if notes != "" {
			s += `<presentation:notes><draw:page-thumbnail/><draw:frame presentation:class="notes"><draw:text-box><text:p>` + notes + `</text:p></draw:text-box></draw:frame></presentation:notes>`
		}
		return s + `</draw:page>`
	}
	return fixtureZip(
		"mimetype", "application/vnd.oasis.opendocument.presentation",
		"content.xml", `<?xml version="1.0" encoding="UTF-8"?><office:document-content `+odfNS+`><office:body><office:presentation>`+
			page("p1", "Field trial", "Twelve sites,<text:s/>three regions", "Say the pilot ended in March.")+
			page("p2", "Findings", "Battery life doubled", "")+
			`</office:presentation></office:body></office:document-content>`,
	)
}

// fixtureEPUB is a book whose spine order differs from its manifest order.
func fixtureEPUB() []byte {
	chapter := func(title, body string) string {
		return `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>` + title + `</title><style>p{margin:0}</style></head><body><h1>` + title + `</h1><p>` + body + `</p></body></html>`
	}
	return fixtureZip(
		"mimetype", "application/epub+zip",
		"META-INF/container.xml", `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>The Lighthouse Keeper</dc:title></metadata>`+
			`<manifest><item id="c2" href="text/chapter%202.xhtml" media-type="application/xhtml+xml"/><item id="c1" href="text/chapter1.xhtml" media-type="application/xhtml+xml"/></manifest>`+
			`<spine><itemref idref="c1"/><itemref idref="c2"/></spine></package>`,
		"OEBPS/text/chapter 2.xhtml", chapter("Storm", "The lamp failed at <em>midnight</em>."),
		"OEBPS/text/chapter1.xhtml", chapter("Arrival", "Mara rowed out to the rock in April."),
	)
}

// fixtureEML is a multipart message: plain and HTML alternatives, a CSV
// attachment the server converts and a picture it does not.
const fixtureEML = "From: =?UTF-8?Q?Zo=C3=AB_Kowalska?= <zoe@example.org>\r\n" +
	"To: ops@example.org\r\n" +
	"Subject: =?UTF-8?B?UXVhcnRlcmx5IGZpZ3VyZXMg4oCUIFEz?=\r\n" +
	"Date: Tue, 27 Oct 2026 09:15:00 +0100\r\n" +
	"Message-ID: <q3-figures@example.org>\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"outer\"\r\n" +
	"\r\n" +
	"--outer\r\n" +
	"Content-Type: multipart/alternative; boundary=\"inner\"\r\n" +
	"\r\n" +
	"--inner\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n" +
	"\r\n" +
	"Hi all,\r\n\r\nthe Q3 figures are attached. Revenue grew 12 =E2=80=94 see the sheet.\r\n" +
	"--inner\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n" +
	"\r\n" +
	"<p>Hi all,</p><script>steal()</script><p>the Q3 figures are attached.</p>\r\n" +
	"--inner--\r\n" +
	"--outer\r\n" +
	"Content-Type: text/csv; name=\"q3.csv\"\r\n" +
	"Content-Disposition: attachment; filename=\"q3.csv\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"cmVnaW9uO3JldmVudWUKTm9ydGg7MTIwMApTb3V0aDs5ODAK\r\n" +
	"--outer\r\n" +
	"Content-Type: image/png; name=\"chart.png\"\r\n" +
	"Content-Disposition: attachment; filename=\"chart.png\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAwS2OUAAAAABJRU5ErkJggg==\r\n" +
	"--outer--\r\n"

// fixtureMbox holds two messages: one in Latin-1 with an mboxrd-escaped
// "From " line in its body, one HTML-only with a script that must not survive.
const fixtureMbox = "From alice@example.org Mon Oct 26 08:00:00 2026\n" +
	"From: Alice <alice@example.org>\n" +
	"To: team@example.org\n" +
	"Subject: Standup moved\n" +
	"Date: Mon, 26 Oct 2026 08:00:00 +0000\n" +
	"Message-ID: <standup@example.org>\n" +
	"Content-Type: text/plain; charset=iso-8859-1\n" +
	"Content-Transfer-Encoding: quoted-printable\n" +
	"\n" +
	"Standup moves to 10:00 in the caf=E9.\n" +
	">From now on, bring coffee.\n" +
	"\n" +
	"From bob@example.org Mon Oct 26 09:30:00 2026\n" +
	"From: Bob <bob@example.org>\n" +
	"To: team@example.org\n" +
	"Subject: Release notes\n" +
	"Date: Mon, 26 Oct 2026 09:30:00 +0000\n" +
	"Content-Type: text/html\n" +
	"\n" +
	"<h2>Release 4.2</h2><script>alert('x')</script><ul><li>Faster sync</li><li>Dark mode</li></ul>\n"

// convertFixtures are the fixture files, generated.
func convertFixtures() map[string][]byte {
	noise := make([]byte, 2048)
	_, _ = rand.New(rand.NewSource(46_046)).Read(noise) // #nosec G404 -- deterministic fixture bytes, not a secret
	return map[string][]byte{
		"sample.pdf":  fixturePDF([]string{"Field report: the north well\nWater level 4.2 m, rising.", "Second page: pump replaced on 3 March."}, true),
		"scanned.pdf": fixturePDF([]string{"", "", ""}, false),
		"sample.xlsx": fixtureXLSX(),
		"sample.ods":  fixtureODS(),
		"sample.csv":  []byte("\xef\xbb\xbfregion;\"revenue; EUR\";note\nNorth;1200;\"steady, \"\"good\"\"\"\nSouth;980;\n"),
		"sample.tsv":  []byte("name\trole\nAna\tlead\nIvo\tops\n"),
		"sample.pptx": fixturePPTX(),
		"sample.odp":  fixtureODP(),
		"sample.epub": fixtureEPUB(),
		"sample.eml":  []byte(fixtureEML),
		"sample.mbox": []byte(fixtureMbox),
		"random.bin":  noise,
		"sample.docx": fixtureZip(
			"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
			"word/document.xml", `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`+
				`<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Site survey</w:t></w:r></w:p>`+
				`<w:p><w:r><w:t xml:space="preserve">The roof is </w:t></w:r><w:r><w:rPr><w:b/></w:rPr><w:t>sound</w:t></w:r><w:r><w:t>.</w:t></w:r></w:p>`+
				`</w:body></w:document>`),
		"sample.odt": fixtureZip(
			"mimetype", "application/vnd.oasis.opendocument.text",
			"content.xml", `<?xml version="1.0" encoding="UTF-8"?><office:document-content `+odfNS+`><office:body><office:text>`+
				`<text:h text:outline-level="1">Minutes</text:h><text:p>Budget approved for the <text:span>new depot</text:span>.</text:p>`+
				`</office:text></office:body></office:document-content>`),
		"sample.rtf":  []byte(`{\rtf1\ansi{\fonttbl{\f0 Helvetica;}}\f0\b Delivery note\b0\par Three crates, signed by the driver.\par}`),
		"sample.html": []byte(`<!doctype html><html><head><title>Opening hours</title><style>p{color:red}</style></head><body><h1>Opening hours</h1><p>Mon&ndash;Fri <strong>9&ndash;17</strong>, see <a href="https://example.org/map">the map</a>.</p><script>track()</script></body></html>`),
		"sample.tex":  []byte("\\documentclass{article}\n\\begin{document}\n\\section{Method}\nWe sampled \\textbf{forty} wells.\n\\end{document}\n"),
	}
}

// renderConversion is the golden form of a conversion: the Markdown of the
// file and of each part, with the meta a reader would check.
func renderConversion(c conversion) string {
	var b strings.Builder
	b.WriteString(c.Markdown + "\n")
	b.WriteString(renderMeta(c))
	for _, p := range c.Parts {
		fmt.Fprintf(&b, "\n=== part %s (parent %q)\n%s\n%s", p.Suffix, p.ParentSuffix, p.Markdown, renderMeta(p.conversion))
	}
	return b.String()
}

// renderMeta lists a conversion's format, score, warnings and meta, sorted.
func renderMeta(c conversion) string {
	lines := []string{fmt.Sprintf("--- format=%s converted=%v confidence=%.2f", c.Format, c.Converted, c.Confidence)}
	for _, w := range c.Warnings {
		lines = append(lines, "--- warning: "+w)
	}
	keys := make([]string, 0, len(c.Meta))
	for k := range c.Meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("--- meta %s=%q", k, c.Meta[k]))
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestConvertFixturesGolden(t *testing.T) {
	fixtures := convertFixtures()
	if *updateConvertFixtures {
		if err := os.MkdirAll(convertFixtureDir, 0o750); err != nil {
			t.Fatal(err)
		}
		for name, data := range fixtures {
			if err := os.WriteFile(filepath.Join(convertFixtureDir, name), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name := range fixtures {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(convertFixtureDir, name)) // #nosec G304 -- a fixture of this package
			if err != nil {
				t.Fatal(err)
			}
			c, err := convertToMarkdown(formatFromFilename(name), data, false)
			if name == "random.bin" {
				if err == nil {
					t.Fatalf("random bytes converted: %+v", c)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join(convertFixtureDir, name+".md")
			got := renderConversion(c)
			if *updateConvertFixtures {
				if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden) // #nosec G304 -- a golden file of this package
			if err != nil {
				t.Fatal(err)
			}
			if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
				t.Errorf("%s converts differently from %s:\n--- got\n%s\n--- want\n%s", name, golden, got, want)
			}
		})
	}
}

// The acceptance thresholds: a scan scores below 0.2 and says why; a PDF
// with a text layer scores above 0.9; so do the other fixtures.
func TestConvertFixturesConfidence(t *testing.T) {
	for name, data := range convertFixtures() {
		if name == "random.bin" {
			continue
		}
		c, err := convertToMarkdown(formatFromFilename(name), data, false)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		switch name {
		case "scanned.pdf":
			if c.Confidence >= 0.2 || len(c.Warnings) == 0 || !strings.Contains(c.Warnings[0], "3 of 3 pages have no extractable text") {
				t.Errorf("scanned PDF: %.2f %v", c.Confidence, c.Warnings)
			}
		case "sample.xlsx":
			// One of its two sheets is empty.
			if c.Confidence != 0.75 || len(c.Warnings) != 1 {
				t.Errorf("%s: %.2f %v", name, c.Confidence, c.Warnings)
			}
		default:
			if c.Confidence <= 0.9 {
				t.Errorf("%s: %.2f %v", name, c.Confidence, c.Warnings)
			}
		}
		t.Logf("%-12s confidence %.2f %v", name, c.Confidence, c.Warnings)
	}
}
