package main

import (
	"errors"
	"reflect"
	"testing"
)

// backlogFile is a BACKLOG.md in the shape plan export writes, with text
// around the table and an escaped pipe inside a cell.
const backlogFile = `# Backlog

One line is one session.

| Prompt | Size | Model |
|---|---|---|
| #31: webhook with HMAC and a replay window | M | Opus |
| Docs: one form of ` + "`a \\| b`" + ` in the README | S | Sonnet |
| Panel: a screenshot script |  | Sonnet |

Trailing text.
`

const backlogTable = `| Prompt | Size | Model |
|---|---|---|
| #31: webhook with HMAC and a replay window | M | Opus |
| Docs: one form of ` + "`a \\| b`" + ` in the README | S | Sonnet |
| Panel: a screenshot script |  | Sonnet |
`

func TestParseTable_ReadsTheFirstTable(t *testing.T) {
	tbl, err := parseTable(backlogFile)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tbl.columns, []string{"Prompt", "Size", "Model"}) || len(tbl.rows) != 3 {
		t.Fatalf("%+v", tbl)
	}
	if got := tbl.rows[1][0]; got != "Docs: one form of `a | b` in the README" {
		t.Fatalf("escaped pipe: %q", got)
	}
	if tbl.rows[2][1] != "" {
		t.Fatalf("empty cell: %q", tbl.rows[2][1])
	}
	if got := renderTable(tbl); got != backlogTable {
		t.Fatalf("render:\n%s\nwant:\n%s", got, backlogTable)
	}
}

func TestParseTable_ShapesAndErrors(t *testing.T) {
	// CRLF, alignment colons, a short row padded and a long row cut.
	tbl, err := parseTable("| A | B |\r\n|:--|--:|\r\n| 1 |\r\n| 1 | 2 | 3 |\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tbl.rows, [][]string{{"1", ""}, {"1", "2"}}) {
		t.Fatalf("%q", tbl.rows)
	}
	for _, text := range []string{"", "no table here", "| A |\n| not a separator |", "| A |\n|  |", "| A |\nplain"} {
		if _, err := parseTable(text); !errors.Is(err, errNoTable) {
			t.Errorf("%q: %v", text, err)
		}
	}
	// A cell that ends the line with an escaped pipe keeps it.
	if got := splitRow(`| a \|`); !reflect.DeepEqual(got, []string{`a |`}) {
		t.Fatalf("%q", got)
	}
}
