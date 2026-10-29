package main

import (
	"bytes"
	"compress/zlib"
	"strings"
	"testing"
)

// Edge cases of the GO-046 converters that the golden fixtures do not reach:
// malformed containers, odd PDFs, and the corners of spreadsheet dates.

func TestPDFStreams(t *testing.T) {
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	_, _ = zw.Write([]byte("<< /Type /Page >> << /Type /Page >>"))
	_ = zw.Close()
	pdf := "%PDF-1.5\n" +
		"1 0 obj\n<< /Type /ObjStm /Filter /FlateDecode >>\nstream\n" + z.String() + "\nendstream\nendobj\n" +
		"2 0 obj\n<< /Length 3 >>\nstream\r\nBT (Caf\xe9 au lait) Tj ET\r\nendstream\nendobj\n" +
		"3 0 obj\n<< /Filter /FlateDecode >>\nstream\nnot zlib\nendstream\nendobj\n" +
		"4 0 obj\n<< /Filter /ASCII85Decode >>\nstream\n<~87cURD~>\nendstream\nendobj\n" +
		"5 0 obj\n<< /Length1 9 >>\nstream\n(font program) Tj\nendstream\nendobj\n" +
		"6 0 obj\n<< >>\nstream\n(no text operators here)\nendstream\nendobj\n" +
		"7 0 obj\n<< >>\nstream\nBT (cut short"
	md, sig, err := pdfToMarkdown([]byte(pdf))
	if err != nil {
		t.Fatal(err)
	}
	if md != "Café au lait" {
		t.Errorf("text: %q", md)
	}
	if sig.Pages != 2 || sig.PagesWithoutText != 1 || len(sig.Warnings) != 1 || !strings.Contains(sig.Warnings[0], "2 streams use a filter") {
		t.Errorf("signals: %+v", sig)
	}

	// A truncated Flate stream gives what inflated before the cut.
	z.Reset()
	zw = zlib.NewWriter(&z)
	_, _ = zw.Write([]byte("BT (Partial words) Tj ET"))
	_ = zw.Flush()
	if got, ok := pdfInflate(z.String()); !ok || !strings.Contains(got, "Partial words") {
		t.Errorf("truncated stream: %q %v", got, ok)
	}
	old := maxPDFInflate
	maxPDFInflate = 4
	defer func() { maxPDFInflate = old }()
	if got, _ := pdfInflate(z.String()); got != "BT (" {
		t.Errorf("inflate bound: %q", got)
	}
	if got := pdfStreamDict("no keyword"); got != "no keyword" {
		t.Errorf("dict: %q", got)
	}
}

func TestSpreadsheetValues(t *testing.T) {
	for serial, want := range map[float64]string{45658: "2025-01-01", 0.5: "12:00:00", 45659.75: "2025-01-02T18:00:00"} {
		if got := excelSerialToISO(serial, false); got != want {
			t.Errorf("serial %v = %q, want %q", serial, got, want)
		}
	}
	if got := excelSerialToISO(0, true); got != "1904-01-01" {
		t.Errorf("1904 epoch: %q", got)
	}
	for code, want := range map[string]bool{`yyyy-mm-dd`: true, `[h]:mm`: true, `"day "0`: false, `0\d`: false, `[Red]0.00`: false, `General`: false, `#,##0 "items"`: false} {
		if got := isDateFormat(code); got != want {
			t.Errorf("isDateFormat(%q) = %v", code, got)
		}
	}
	if formatNumber("abc") != "abc" || formatNumber("1.50") != "1.5" || xlsxNumber("-3", true, false) != "-3" {
		t.Error("numbers")
	}
	for ref, want := range map[string]int{"A1": 0, "AA10": 26, "": 7, "ABCD1": 7, "1": 7} {
		if got := cellColumn(ref, 7); got != want {
			t.Errorf("cellColumn(%q) = %d, want %d", ref, got, want)
		}
	}
	if markdownTable([][]string{{"", " "}}) != "" || tableCell("a|b\nc") != `a\|b c` {
		t.Error("table")
	}
	if detectSeparator([]byte("a,b\nc,d,e\n")) != ',' || detectSeparator([]byte("a\tb\tc\n1\t2\t3\n")) != '\t' {
		t.Error("separator")
	}
	if md, sig := csvToMarkdown([]byte("\n\n"), "csv"); md != "" || sig.EmptySheets != 1 {
		t.Errorf("empty csv: %q %+v", md, sig)
	}
}

func TestXLSXErrors(t *testing.T) {
	wb := `<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><workbookPr date1904="1"/><sheets><sheet name="S" r:id="rId1"/></sheets></workbook>`
	rels := `<Relationships><Relationship Id="rId1" Type="x/worksheet" Target="worksheets/s.xml"/></Relationships>`
	cases := map[string][]byte{
		"invalid xlsx file":         []byte("PK"),
		"xl/workbook.xml not found": fixtureZip("a", "b"),
		"not found":                 fixtureZip("xl/workbook.xml", wb),
		"invalid xlsx: xl/worksheets/s.xml": fixtureZip("xl/workbook.xml", wb, "xl/_rels/workbook.xml.rels", rels,
			"xl/worksheets/s.xml", "<worksheet>"),
	}
	for want, data := range cases {
		if _, _, err := xlsxToMarkdown(data); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q, got %v", want, err)
		}
	}
	// 1904 dates, a shared string index out of range, a column past the
	// widest sheet, and cells without references.
	sheet := `<worksheet><sheetData><row><c t="s"><v>9</v></c><c s="0"><v>0</v></c><c r="ZZZZ1"><v>1</v></c><c t="e"><v>#DIV/0!</v></c></row></sheetData></worksheet>`
	md, _, err := xlsxToMarkdown(fixtureZip("xl/workbook.xml", wb, "xl/_rels/workbook.xml.rels", rels, "xl/worksheets/s.xml", sheet,
		"xl/styles.xml", `<styleSheet><cellXfs><xf numFmtId="14"/></cellXfs></styleSheet>`))
	if err != nil || !strings.Contains(md, "|  | 1904-01-01 | 1904-01-02 | #DIV/0! |") {
		t.Errorf("odd cells: %q %v", md, err)
	}
}

func TestODSErrors(t *testing.T) {
	for want, data := range map[string][]byte{
		"invalid ods file":          []byte("PK"),
		"content.xml not found":     fixtureZip("mimetype", "application/vnd.oasis.opendocument.spreadsheet"),
		"invalid ods: content.xml:": fixtureZip("content.xml", "<office:document-content><table:table>"),
	} {
		if _, _, err := odsToMarkdown(data); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q, got %v", want, err)
		}
	}
	content := `<r xmlns:table="t" xmlns:text="x" xmlns:office="o"><table:table-row><table:table-cell><text:p>before any table</text:p></table:table-cell></table:table-row>` +
		`<table:table table:name="T"><table:table-row><table:covered-table-cell/><table:table-cell table:number-columns-repeated="x"><text:h>Head</text:h><text:p>line<text:line-break/>two</text:p></table:table-cell>` +
		`<table:table-cell office:value-type="time" office:time-value="PT1H2M3.5S"/><table:table-cell table:number-columns-repeated="5000"><text:p>big</text:p></table:table-cell></table:table-row></table:table></r>`
	md, _, err := odsToMarkdown(fixtureZip("content.xml", content))
	if err != nil || !strings.Contains(md, "|  | Head line two | 01:02:03 | big |") || strings.Count(md, "big") != maxSheetRepeat || strings.Contains(md, "before") {
		t.Errorf("odd cells: %d %v", len(md), err)
	}
}

func TestSlideErrors(t *testing.T) {
	pres := `<p:presentation xmlns:p="p" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><p:sldIdLst><p:sldId r:id="rId1"/></p:sldIdLst></p:presentation>`
	rels := `<Relationships><Relationship Id="rId1" Type="x/slide" Target="slides/s.xml"/></Relationships>`
	for want, data := range map[string][]byte{
		"invalid pptx file":              []byte("PK"),
		"ppt/presentation.xml not found": fixtureZip("a", "b"),
		"not found":                      fixtureZip("ppt/presentation.xml", pres),
		"invalid pptx: XML syntax":       fixtureZip("ppt/presentation.xml", pres, "ppt/_rels/presentation.xml.rels", rels, "ppt/slides/s.xml", "<p:sld><a:p>"),
	} {
		if _, _, err := pptxToMarkdown(data); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("pptx want %q, got %v", want, err)
		}
	}
	// A picture-only slide, a placeholder without a type, a table frame and
	// a line break outside a paragraph; notes that cannot be read are skipped.
	slide := `<p:sld xmlns:p="p" xmlns:a="a"><a:br/><p:sp><p:nvSpPr><p:nvPr><p:ph/></p:nvPr></p:nvSpPr><p:txBody><a:p><a:r><a:t>Body</a:t></a:r></a:p></p:txBody></p:sp><p:graphicFrame><a:tbl><a:p><a:r><a:t>Cell</a:t></a:r></a:p></a:tbl></p:graphicFrame></p:sld>`
	pres2 := strings.Replace(pres, `<p:sldId r:id="rId1"/>`, `<p:sldId r:id="rId1"/><p:sldId r:id="rId2"/>`, 1)
	rels2 := strings.Replace(rels, `</Relationships>`, `<Relationship Id="rId2" Type="x/slide" Target="slides/empty.xml"/></Relationships>`, 1)
	md, sig, err := pptxToMarkdown(fixtureZip("ppt/presentation.xml", pres2, "ppt/_rels/presentation.xml.rels", rels2, "ppt/slides/s.xml", slide,
		"ppt/slides/_rels/s.xml.rels", `<Relationships><Relationship Id="n" Type="x/notesSlide" Target="../notes/missing.xml"/></Relationships>`,
		"ppt/slides/empty.xml", `<p:sld/>`))
	if err != nil || md != "## Slide 1\n\nBody\n\nCell\n\n## Slide 2" || len(sig.Warnings) != 1 || !strings.Contains(sig.Warnings[0], "1 of 2 slides have no text") {
		t.Errorf("odd slides: %q %+v %v", md, sig, err)
	}

	for want, data := range map[string][]byte{
		"invalid odp file":          []byte("PK"),
		"content.xml not found":     fixtureZip("a", "b"),
		"invalid odp: content.xml:": fixtureZip("content.xml", "<draw:page><text:p>"),
	} {
		if _, _, err := odpToMarkdown(data); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("odp want %q, got %v", want, err)
		}
	}
	odp := `<r xmlns:draw="d" xmlns:text="t"><text:p>before any page</text:p><draw:page><draw:frame><draw:text-box><text:p> </text:p><text:p>a<text:tab/>b</text:p></draw:text-box></draw:frame></draw:page><text:s/></r>`
	if md, _, err := odpToMarkdown(fixtureZip("content.xml", odp)); err != nil || md != "## Slide 1\n\na b" {
		t.Errorf("odd odp: %q %v", md, err)
	}
}

func TestEPUBErrors(t *testing.T) {
	container := `<container><rootfiles><rootfile full-path="book.opf"/></rootfiles></container>`
	for want, data := range map[string][]byte{
		"invalid epub file":                  []byte("PK"),
		"META-INF/container.xml not found":   fixtureZip("mimetype", "application/epub+zip"),
		"names no package":                   fixtureZip("META-INF/container.xml", "<container/>"),
		"book.opf not found":                 fixtureZip("META-INF/container.xml", container),
		"invalid epub: META-INF/container.x": fixtureZip("META-INF/container.xml", "<container>"),
	} {
		if _, _, err := epubToMarkdown(data, false); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q, got %v", want, err)
		}
	}
	opf := `<package><manifest><item id="a" href="a.html"/><item id="bad" href="%zz"/><item id="gone" href="gone.html"/></manifest>` +
		`<spine><itemref idref="a"/><itemref idref="bad"/><itemref idref="gone"/><itemref idref="unlisted"/></spine></package>`
	md, sig, err := epubToMarkdown(fixtureZip("META-INF/container.xml", container, "book.opf", opf, "a.html", "<h1>Only</h1><p>chapter</p>"), true)
	if err != nil || md != "Only\n\nchapter" || len(sig.Warnings) != 1 || !strings.Contains(sig.Warnings[0], "3 of 4 spine items") {
		t.Errorf("epub: %q %+v %v", md, sig, err)
	}
}
