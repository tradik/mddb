package main

import (
	"bytes"
	"encoding/csv"
	"math"
	"strconv"
	"strings"
	"time"
)

// Spreadsheets (GO-046): every sheet a Markdown section holding a table, and
// dates as dates. A spreadsheet stores a date as a number of days with a
// display format; reading the number alone indexes "45292" where the sheet
// shows 2024-01-01, which no query will ever match.

// maxSheetRepeat bounds how far a repeated cell or row is expanded: ODS
// writes "the next 16 384 columns are empty" as one cell, and a filled cell
// repeated a million times is a file built to exhaust memory.
const maxSheetRepeat = 1000

// maxSheetColumns is the widest sheet Excel writes (column XFD).
const maxSheetColumns = 16384

// sheet is one worksheet's name and rows of cell text.
type sheet struct {
	name string
	rows [][]string
}

// sheetsToMarkdown renders sheets as sections and reports how many were empty.
func sheetsToMarkdown(sheets []sheet) (string, conversionSignals) {
	sig := conversionSignals{Sheets: len(sheets)}
	var b strings.Builder
	for _, sh := range sheets {
		b.WriteString("## " + sh.name + "\n\n")
		table := markdownTable(sh.rows)
		if table == "" {
			sig.EmptySheets++
			b.WriteString("_Empty sheet._\n\n")
			continue
		}
		b.WriteString(table + "\n")
	}
	return strings.TrimSpace(b.String()), sig
}

// markdownTable renders rows as a table with the first row as its header,
// dropping empty trailing rows and columns. Empty when no cell has text.
func markdownTable(rows [][]string) string {
	width, height := 0, 0
	for i, row := range rows {
		for j, cell := range row {
			if strings.TrimSpace(cell) != "" {
				width = max(width, j+1)
				height = i + 1
			}
		}
	}
	if width == 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < height; i++ {
		b.WriteString("|")
		for j := 0; j < width; j++ {
			cell := ""
			if j < len(rows[i]) {
				cell = rows[i][j]
			}
			b.WriteString(" " + tableCell(cell) + " |")
		}
		b.WriteString("\n")
		if i == 0 {
			b.WriteString("|" + strings.Repeat(" --- |", width) + "\n")
		}
	}
	return b.String()
}

// tableCell makes cell text safe inside a Markdown table row.
func tableCell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.ReplaceAll(s, "|", `\|`)
}

// csvToMarkdown reads delimited text as one table. TSV is tab-separated;
// for CSV the separator is detected from the first lines (a European export
// uses ";", because "," is its decimal mark).
func csvToMarkdown(data []byte, format string) (string, conversionSignals) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = detectSeparator(data)
	if format == "tsv" {
		r.Comma = '\t'
	}
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	var rows [][]string
	for {
		// With LazyQuotes and any field count, reading bytes in memory has
		// no error but the end.
		rec, err := r.Read()
		if err != nil {
			break
		}
		rows = append(rows, rec)
	}
	sig := conversionSignals{Sheets: 1}
	table := markdownTable(rows)
	if table == "" {
		sig.EmptySheets = 1
	}
	return strings.TrimSpace(table), sig
}

// detectSeparator picks the candidate that splits the first lines into the
// same, largest number of fields; "," when none does.
func detectSeparator(data []byte) rune {
	lines := strings.Split(string(data), "\n")
	if len(lines) > 10 {
		lines = lines[:10]
	}
	best, bestCount := ',', 0
	for _, sep := range []rune{',', ';', '\t', '|'} {
		count := -1
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			n := countOutsideQuotes(line, sep)
			if count == -1 {
				count = n
			} else if n != count {
				count = 0
				break
			}
		}
		if count > bestCount {
			best, bestCount = sep, count
		}
	}
	return best
}

// countOutsideQuotes counts sep in line outside double-quoted fields.
func countOutsideQuotes(line string, sep rune) int {
	n, quoted := 0, false
	for _, r := range line {
		switch {
		case r == '"':
			quoted = !quoted
		case r == sep && !quoted:
			n++
		}
	}
	return n
}

// excelSerialToISO turns a spreadsheet date serial into ISO 8601: a date, a
// time of day, or both. date1904 selects the Mac epoch.
func excelSerialToISO(serial float64, date1904 bool) string {
	epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	if date1904 {
		epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	days := math.Floor(serial)
	secs := math.Round((serial - days) * 86400)
	t := epoch.AddDate(0, 0, int(days)).Add(time.Duration(secs) * time.Second)
	switch {
	case secs == 0:
		return t.Format("2006-01-02")
	case days == 0:
		return t.Format("15:04:05")
	}
	return t.Format("2006-01-02T15:04:05")
}

// isDateFormat reports whether a number format code displays a date or time:
// it has y, m, d, h or s outside quoted text, escapes and [colour] brackets.
func isDateFormat(code string) bool {
	var b strings.Builder
	quoted, bracket := false, false
	for i := 0; i < len(code); i++ {
		c := code[i]
		switch {
		case c == '"':
			quoted = !quoted
		case quoted:
		case c == '\\':
			i++
		case c == '[':
			bracket = true
		case c == ']':
			bracket = false
		case !bracket:
			b.WriteByte(c)
		}
	}
	return strings.ContainsAny(strings.ToLower(b.String()), "ymdhs")
}

// builtinDateFormat reports whether a built-in number format ID is a date or
// time format (ECMA-376 part 1, 18.8.30).
func builtinDateFormat(id int) bool {
	return (id >= 14 && id <= 22) || (id >= 45 && id <= 47)
}

// formatNumber prints a cell number the way a reader expects: integers
// without a decimal point, other values as short as they round-trip.
func formatNumber(v string) string {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return v
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}
