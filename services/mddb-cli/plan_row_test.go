package main

import (
	"reflect"
	"strings"
	"testing"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"#31: webhook with HMAC and a replay window":        "31-webhook-with-hmac-and-a",
		"Zażółć gęślą jaźń":                                 "za-g-l-ja",
		"!!!":                                               "row",
		strings.Repeat("abcdefghij", 6):                     strings.Repeat("abcdefghij", 5)[:48],
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa b": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestColumnKey(t *testing.T) {
	for in, want := range map[string]string{
		"Model": "model", "Due date": "dueDate", "Status": "columnStatus", "": "column", "!!": "column",
	} {
		if got := columnKey(in); got != want {
			t.Errorf("columnKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRowIssuesAndMeta(t *testing.T) {
	issues := rowIssues("#31 and #7, again #31", []int{7, 2})
	if !reflect.DeepEqual(issues, []string{"7", "2", "31"}) {
		t.Fatalf("%v", issues)
	}
	r := planRow{order: 3, size: "M", issues: issues, columns: []string{"Prompt"}, extra: map[string]string{"due": "x", "empty": ""}}
	m := r.meta()
	want := map[string][]string{"kind": {"backlog"}, "status": {"todo"}, "order": {"3"}, "size": {"M"},
		"issues": issues, "columns": {"Prompt"}, "due": {"x"}}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("%v", m)
	}
}

func TestSortRows(t *testing.T) {
	doc := func(key, order string) mddb.Document {
		return mddb.Document{Key: key, Meta: map[string][]string{"order": {order}}}
	}
	docs := []mddb.Document{doc("backlog/c", "x"), doc("backlog/b", "10"), doc("backlog/a", "2"), doc("backlog/a2", "2"), {Key: "backlog/z"}}
	sortRows(docs)
	var keys []string
	for _, d := range docs {
		keys = append(keys, rowSlug(d))
	}
	if !reflect.DeepEqual(keys, []string{"a", "a2", "b", "c", "z"}) {
		t.Fatalf("%v", keys)
	}
}
