package main

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Reading XLSX (OOXML) and ODS (OpenDocument) workbooks with archive/zip and
// encoding/xml: values only — no formulas evaluated, no styles beyond what
// says a number is a date.

// xlsxWorkbook is xl/workbook.xml: the sheets in tab order, and the epoch.
type xlsxWorkbook struct {
	Pr struct {
		Date1904 string `xml:"date1904,attr"`
	} `xml:"workbookPr"`
	Sheets []struct {
		Name string `xml:"name,attr"`
		RID  string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
	} `xml:"sheets>sheet"`
}

// xlsxRun is the text of a string item or rich-text run.
type xlsxRun struct {
	T string `xml:"t"`
}

// xlsxString is a shared or inline string: plain, or rich-text runs.
type xlsxString struct {
	T    string    `xml:"t"`
	Runs []xlsxRun `xml:"r"`
}

// text joins a string item's runs.
func (s xlsxString) text() string {
	if len(s.Runs) == 0 {
		return s.T
	}
	var b strings.Builder
	for _, r := range s.Runs {
		b.WriteString(r.T)
	}
	return b.String()
}

// xlsxStyles is the part of xl/styles.xml that decides which cells are dates.
type xlsxStyles struct {
	NumFmts []struct {
		ID   int    `xml:"numFmtId,attr"`
		Code string `xml:"formatCode,attr"`
	} `xml:"numFmts>numFmt"`
	Xfs []struct {
		NumFmtID int `xml:"numFmtId,attr"`
	} `xml:"cellXfs>xf"`
}

// xlsxSheet is a worksheet's cells.
type xlsxSheet struct {
	Rows []struct {
		Cells []struct {
			Ref    string     `xml:"r,attr"`
			Style  int        `xml:"s,attr"`
			Type   string     `xml:"t,attr"`
			Value  string     `xml:"v"`
			Inline xlsxString `xml:"is"`
		} `xml:"c"`
	} `xml:"sheetData>row"`
}

// xlsxToMarkdown converts every worksheet of an XLSX workbook.
func xlsxToMarkdown(data []byte) (string, conversionSignals, error) {
	z, err := openZipDoc(data, "xlsx")
	if err != nil {
		return "", conversionSignals{}, err
	}
	var wb xlsxWorkbook
	if err := z.readXML("xl/workbook.xml", &wb); err != nil {
		return "", conversionSignals{}, err
	}
	var sst struct {
		Items []xlsxString `xml:"si"`
	}
	_ = z.readXML("xl/sharedStrings.xml", &sst) // a workbook of numbers has none
	var styles xlsxStyles
	_ = z.readXML("xl/styles.xml", &styles)
	dateStyle := xlsxDateStyles(styles)
	date1904 := wb.Pr.Date1904 == "1" || wb.Pr.Date1904 == "true"
	targets, _ := z.relTargets("xl/workbook.xml")

	var sheets []sheet
	for _, s := range wb.Sheets {
		var ws xlsxSheet
		if err := z.readXML(targets[s.RID], &ws); err != nil {
			return "", conversionSignals{}, err
		}
		sh := sheet{name: s.Name}
		for _, row := range ws.Rows {
			var cells []string
			for i, c := range row.Cells {
				col := cellColumn(c.Ref, i)
				if col >= maxSheetColumns {
					continue
				}
				for len(cells) <= col {
					cells = append(cells, "")
				}
				switch c.Type {
				case "s":
					if n, err := strconv.Atoi(c.Value); err == nil && n >= 0 && n < len(sst.Items) {
						cells[col] = sst.Items[n].text()
					}
				case "inlineStr":
					cells[col] = c.Inline.text()
				case "b":
					cells[col] = map[string]string{"1": "TRUE", "0": "FALSE"}[c.Value]
				case "str", "e":
					cells[col] = c.Value
				default:
					cells[col] = xlsxNumber(c.Value, dateStyle[c.Style], date1904)
				}
			}
			sh.rows = append(sh.rows, cells)
		}
		sheets = append(sheets, sh)
	}
	md, sig := sheetsToMarkdown(sheets)
	sig.Container = true
	return md, sig, nil
}

// xlsxDateStyles marks the cell styles whose number format shows a date.
func xlsxDateStyles(st xlsxStyles) map[int]bool {
	custom := map[int]string{}
	for _, f := range st.NumFmts {
		custom[f.ID] = f.Code
	}
	out := map[int]bool{}
	for i, xf := range st.Xfs {
		if code, ok := custom[xf.NumFmtID]; ok {
			out[i] = isDateFormat(code)
		} else {
			out[i] = builtinDateFormat(xf.NumFmtID)
		}
	}
	return out
}

// xlsxNumber prints a numeric cell: as an ISO date when its style is a date
// format, otherwise as the number.
func xlsxNumber(v string, isDate, date1904 bool) string {
	if isDate {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			return excelSerialToISO(f, date1904)
		}
	}
	return formatNumber(v)
}

// cellColumn is the zero-based column of a cell reference ("C7" is 2); a
// cell without a reference follows the previous one.
func cellColumn(ref string, fallback int) int {
	col := 0
	letters := 0
	for _, r := range ref {
		if r < 'A' || r > 'Z' {
			break
		}
		col = col*26 + int(r-'A'+1)
		letters++
	}
	if letters == 0 || letters > 3 {
		return fallback
	}
	return col - 1
}

// odsToMarkdown converts every table of an ODS spreadsheet.
func odsToMarkdown(data []byte) (string, conversionSignals, error) {
	z, err := openZipDoc(data, "ods")
	if err != nil {
		return "", conversionSignals{}, err
	}
	content, err := z.read("content.xml")
	if err != nil {
		return "", conversionSignals{}, err
	}
	sheets, err := odsSheets(content)
	if err != nil {
		return "", conversionSignals{}, err
	}
	md, sig := sheetsToMarkdown(sheets)
	sig.Container = true
	return md, sig, nil
}

// odsCell is the cell being read.
type odsCell struct {
	repeat int
	value  string // a date or time value, which wins over the display text
	text   strings.Builder
}

// odsSheets streams content.xml into sheets. Streaming, because a table's
// repeated rows and columns must be expanded with care rather than all at
// once.
func odsSheets(content []byte) ([]sheet, error) {
	dec := xml.NewDecoder(strings.NewReader(string(content)))
	r := &odsReader{rowRepeat: 1}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return r.sheets, nil
		}
		if err != nil {
			return nil, errors.New("invalid ods: content.xml: " + err.Error())
		}
		r.token(tok)
	}
}

// odsReader is odsSheets' state between tokens: the sheets so far, the row
// and cell being read, and the depth inside an annotation, whose text is not
// the cell's.
type odsReader struct {
	sheets    []sheet
	row       []string
	rowRepeat int
	cell      *odsCell
	skip      int
}

// token feeds one token of content.xml to the reader.
func (r *odsReader) token(tok xml.Token) {
	switch t := tok.(type) {
	case xml.StartElement:
		r.start(t)
	case xml.EndElement:
		r.end(t)
	case xml.CharData:
		if r.cell != nil && r.skip == 0 {
			r.cell.text.Write(t)
		}
	}
}

// start opens a table, row or cell, or adds the space a paragraph break,
// tab or line break stands for.
func (r *odsReader) start(t xml.StartElement) {
	if r.skip > 0 || t.Name.Local == "annotation" {
		r.skip++
		return
	}
	switch t.Name.Local {
	case "table":
		r.sheets = append(r.sheets, sheet{name: odfAttr(t, "name")})
	case "table-row":
		r.row, r.rowRepeat = nil, odfRepeat(t, "number-rows-repeated")
	case "table-cell", "covered-table-cell":
		r.cell = newODSCell(t)
	case "p", "h":
		if r.cell != nil && r.cell.text.Len() > 0 {
			r.cell.text.WriteByte(' ')
		}
	case "s", "tab", "line-break":
		if r.cell != nil {
			r.cell.text.WriteByte(' ')
		}
	}
}

// newODSCell is the cell t opens, with its date or time value if it has one.
func newODSCell(t xml.StartElement) *odsCell {
	cell := &odsCell{repeat: odfRepeat(t, "number-columns-repeated")}
	switch odfAttr(t, "value-type") {
	case "date":
		cell.value = strings.TrimSuffix(odfAttr(t, "date-value"), "T00:00:00")
	case "time":
		cell.value = odfDuration(odfAttr(t, "time-value"))
	}
	return cell
}

// end closes a cell into the row, or a row into the current sheet.
func (r *odsReader) end(t xml.EndElement) {
	if r.skip > 0 {
		r.skip--
		return
	}
	switch t.Name.Local {
	case "table-cell", "covered-table-cell":
		r.row = appendRepeated(r.row, odsCellText(r.cell), r.cell.repeat)
		r.cell = nil
	case "table-row":
		r.endRow()
	}
}

// endRow adds the finished row to the current sheet, repeated as the row
// asks — but only once when it is empty, and at most maxSheetRepeat times.
func (r *odsReader) endRow() {
	if len(r.sheets) == 0 {
		return
	}
	sh := &r.sheets[len(r.sheets)-1]
	for i := 0; i < min(r.rowRepeat, maxSheetRepeat) && (i == 0 || rowHasText(r.row)); i++ {
		sh.rows = append(sh.rows, r.row)
	}
}

// odsCellText is a cell's value: its date or time, or the text it shows.
func odsCellText(c *odsCell) string {
	if c.value != "" {
		return c.value
	}
	return strings.TrimSpace(c.text.String())
}

// appendRepeated appends value n times; an empty value only moves the column
// on, and a filled one is expanded at most maxSheetRepeat times.
func appendRepeated(row []string, value string, n int) []string {
	if value == "" && n > maxSheetRepeat {
		n = 1 // trailing filler: one empty column is the same as many
	}
	for i := 0; i < min(n, maxSheetRepeat); i++ {
		row = append(row, value)
	}
	return row
}

// odfAttr returns an attribute by local name, whatever its namespace.
func odfAttr(t xml.StartElement, local string) string {
	for _, a := range t.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// odfRepeat reads a repeat count; absent or invalid is 1.
func odfRepeat(t xml.StartElement, local string) int {
	n, err := strconv.Atoi(odfAttr(t, local))
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// rowHasText reports whether any cell of row has text.
func rowHasText(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(c) != "" {
			return true
		}
	}
	return false
}

// odfDuration turns an ODF time value (PT13H45M00S) into 13:45:00.
func odfDuration(v string) string {
	v = strings.TrimPrefix(v, "PT")
	var h, m, s int
	for _, part := range []struct {
		unit string
		dst  *int
	}{{"H", &h}, {"M", &m}, {"S", &s}} {
		if i := strings.Index(v, part.unit); i >= 0 {
			f, _ := strconv.ParseFloat(v[:i], 64)
			*part.dst = int(f)
			v = v[i+1:]
		}
	}
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}
