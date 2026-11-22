package main

import (
	"errors"
	"strings"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// Translation between a backlog table and its rows (AGT-007). Prompt, Size
// and Model map onto the row's own fields; any other column is kept as
// meta under columnKey, and the column names themselves in "columns", so
// export can write back the table that was imported.

// errNoPromptColumn is a table the plan cannot read rows from.
var errNoPromptColumn = errors.New("the table has no Prompt column")

// promptColumn is the index of a table's Prompt column, -1 when it has none.
func promptColumn(columns []string) int {
	for i, c := range columns {
		if strings.EqualFold(c, "prompt") {
			return i
		}
	}
	return -1
}

// importRows turns a table's rows into backlog rows numbered from order on.
// Rows with an empty prompt are skipped.
func importRows(t mdTable, order int) ([]planRow, error) {
	p := promptColumn(t.columns)
	if p < 0 {
		return nil, errNoPromptColumn
	}
	var rows []planRow
	for _, cells := range t.rows {
		if cells[p] == "" {
			continue
		}
		r := planRow{prompt: cells[p], order: order, columns: t.columns, extra: map[string]string{}}
		for i, c := range t.columns {
			if i == p {
				continue
			}
			switch k := columnKey(c); k {
			case "size":
				r.size = cells[i]
			case "model":
				r.model = cells[i]
			default:
				r.extra[k] = cells[i]
			}
		}
		r.slug, r.issues = slugify(r.prompt), rowIssues(r.prompt, nil)
		rows = append(rows, r)
		order++
	}
	return rows, nil
}

// exportTable writes rows as a table. The columns are the ones the rows
// were imported with, in the order first seen; rows added with plan add
// carry none, and alone they get defaultColumns.
func exportTable(docs []mddb.Document) mdTable {
	var columns []string
	seen := map[string]bool{}
	for _, d := range docs {
		for _, c := range d.Meta["columns"] {
			if !seen[c] {
				seen[c] = true
				columns = append(columns, c)
			}
		}
	}
	if len(columns) == 0 {
		columns = defaultColumns
	}
	p := promptColumn(columns)
	t := mdTable{columns: columns}
	for _, d := range docs {
		row := make([]string, len(columns))
		for i, c := range columns {
			if i == p {
				row[i] = strings.TrimSpace(d.ContentMD)
				continue
			}
			row[i] = strings.Join(d.Meta[columnKey(c)], ", ")
		}
		t.rows = append(t.rows, row)
	}
	return t
}
