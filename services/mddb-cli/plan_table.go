package main

import (
	"errors"
	"strings"
)

// The Markdown table that plan import reads and plan export writes (AGT-007):
// a header line, a separator line, then one line per row, in the shape of a
// BACKLOG.md file. Export writes "| a | b |" lines and a "|---|---|"
// separator, so a file in that shape comes back byte for byte.

// mdTable is a parsed table: its column names and its rows' cells, with
// "\|" already turned back into "|".
type mdTable struct {
	columns []string
	rows    [][]string
}

// errNoTable is a file without a header and separator line.
var errNoTable = errors.New("no Markdown table found (a header line, then a |---| separator line)")

// parseTable reads the first table in text. Lines before and after it are
// ignored; a row with fewer cells than the header is padded, one with more
// is cut, so every row has a cell per column.
func parseTable(text string) (mdTable, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := 0; i+1 < len(lines); i++ {
		if !isTableLine(lines[i]) || !isSeparator(lines[i+1]) {
			continue
		}
		t := mdTable{columns: splitRow(lines[i])}
		for _, line := range lines[i+2:] {
			if !isTableLine(line) {
				break
			}
			cells := splitRow(line)
			row := make([]string, len(t.columns))
			copy(row, cells)
			t.rows = append(t.rows, row)
		}
		return t, nil
	}
	return mdTable{}, errNoTable
}

// isTableLine reports whether a line belongs to a table.
func isTableLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "|")
}

// isSeparator reports whether a line is a header separator: every cell is
// dashes, with an optional colon at either end for alignment.
func isSeparator(line string) bool {
	if !isTableLine(line) {
		return false
	}
	for _, cell := range splitRow(line) {
		c := strings.TrimSuffix(strings.TrimPrefix(cell, ":"), ":")
		if c == "" || strings.Trim(c, "-") != "" {
			return false
		}
	}
	return true
}

// splitRow returns a table line's cells, trimmed and unescaped. A pipe
// written as "\|" is part of a cell, not a border.
func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	if strings.HasSuffix(line, "|") && !strings.HasSuffix(line, `\|`) {
		line = line[:len(line)-1]
	}
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line) && line[i+1] == '|':
			cur.WriteByte('|')
			i++
		case line[i] == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(line[i])
		}
	}
	return append(cells, strings.TrimSpace(cur.String()))
}

// renderTable writes a table in the shape parseTable reads back unchanged.
func renderTable(t mdTable) string {
	var b strings.Builder
	writeRow(&b, t.columns)
	b.WriteString("|" + strings.Repeat("---|", len(t.columns)) + "\n")
	for _, row := range t.rows {
		writeRow(&b, row)
	}
	return b.String()
}

// writeRow writes one line, escaping the pipes inside cells.
func writeRow(b *strings.Builder, cells []string) {
	escaped := make([]string, len(cells))
	for i, c := range cells {
		escaped[i] = strings.ReplaceAll(c, "|", `\|`)
	}
	b.WriteString("| " + strings.Join(escaped, " | ") + " |\n")
}
