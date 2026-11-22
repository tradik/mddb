package main

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// A backlog row (AGT-007) is a document: key backlog/<slug>, the prompt as
// its content, and meta kind, status, size, order, issues, release and model.
// A row imported from a table also keeps its table's column names in
// "columns" and the cells of columns the plan does not know under a key
// named after the column, so export can write the same table back.

const (
	planKey      = "backlog/"
	kindBacklog  = "backlog"
	kindProgress = "progress"
	statusTodo   = "todo"
	statusDoing  = "doing"
	statusDone   = "done"
)

// defaultColumns is the table export writes when no row was imported.
var defaultColumns = []string{"Prompt", "Size", "Model"}

// reservedMeta are the keys the plan sets itself; a table column whose name
// would map onto one of them is stored as "column<Name>" instead.
var reservedMeta = map[string]bool{
	"kind": true, "status": true, "order": true, "issues": true, "release": true,
	"columns": true, "commit": true, "prompt": true,
}

// planRow is a row before it is written.
type planRow struct {
	prompt, size, model, release, slug string
	order                              int
	issues                             []string
	// columns and extra come from an imported table: its column names, and
	// the cells of the columns that are not prompt, size or model.
	columns []string
	extra   map[string]string
}

// meta is the row's metadata as the document carries it.
func (r planRow) meta() map[string][]string {
	m := map[string][]string{
		"kind": {kindBacklog}, "status": {statusTodo}, "order": {strconv.Itoa(r.order)},
	}
	set := func(k, v string) {
		if v != "" {
			m[k] = []string{v}
		}
	}
	set("size", r.size)
	set("model", r.model)
	set("release", r.release)
	if len(r.issues) > 0 {
		m["issues"] = r.issues
	}
	if len(r.columns) > 0 {
		m["columns"] = r.columns
	}
	for k, v := range r.extra {
		set(k, v)
	}
	return m
}

// issueRef is an issue number written into a prompt: "#31".
var issueRef = regexp.MustCompile(`#(\d+)\b`)

// rowIssues merges the issues given as flags with the ones the prompt
// names, in that order and without repeats.
func rowIssues(prompt string, flags []int) []string {
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, n := range flags {
		add(strconv.Itoa(n))
	}
	for _, m := range issueRef.FindAllStringSubmatch(prompt, -1) {
		add(m[1])
	}
	return out
}

// slugWords and slugMax keep a derived slug short enough to type.
const (
	slugWords = 6
	slugMax   = 48
)

// slugify derives a row's slug from its prompt: its first words, in lower
// case, joined by dashes.
func slugify(prompt string) string {
	words := strings.FieldsFunc(strings.ToLower(prompt), func(r rune) bool {
		return r > unicode.MaxASCII || (!unicode.IsLetter(r) && !unicode.IsDigit(r))
	})
	if len(words) > slugWords {
		words = words[:slugWords]
	}
	s := strings.Join(words, "-")
	if len(s) > slugMax {
		s = strings.TrimRight(s[:slugMax], "-")
	}
	if s == "" {
		return "row"
	}
	return s
}

// columnKey is the meta key a table column is kept under: its name in
// camelCase ("Due date" is dueDate), prefixed when the plan uses that key.
func columnKey(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var b strings.Builder
	for i, w := range words {
		w = strings.ToLower(w)
		if i > 0 {
			w = strings.ToUpper(w[:1]) + w[1:]
		}
		b.WriteString(w)
	}
	key := b.String()
	if key == "" || reservedMeta[key] {
		return "column" + strings.ToUpper(key[:min(1, len(key))]) + key[min(1, len(key)):]
	}
	return key
}

// rowSlug is a row document's slug: its key without the backlog/ prefix.
func rowSlug(d mddb.Document) string {
	return strings.TrimPrefix(d.Key, planKey)
}

// metaFirst is a document's first value for a meta key, "" when it has none.
func metaFirst(d mddb.Document, key string) string {
	if v := d.Meta[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// orderOf is a row's order as a number; a row without one sorts last.
func orderOf(value string) int {
	n, err := strconv.Atoi(value)
	if err != nil {
		return math.MaxInt
	}
	return n
}

// sortRows puts rows in the order a session takes them: by order, then key.
func sortRows(docs []mddb.Document) {
	sort.SliceStable(docs, func(i, j int) bool {
		oi, oj := orderOf(metaFirst(docs[i], "order")), orderOf(metaFirst(docs[j], "order"))
		if oi != oj {
			return oi < oj
		}
		return docs[i].Key < docs[j].Key
	})
}
