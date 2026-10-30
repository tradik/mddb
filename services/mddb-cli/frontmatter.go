package main

import "strings"

// Markdown frontmatter, read the same way by the server and the CLI (#373).
//
// services/mddbd/frontmatter.go and services/mddb-cli/frontmatter.go are the
// same file: the two are separate modules and the server's code is package
// main, so the CLI cannot import it. TestFrontmatterFileMatchesServer in the
// CLI fails when the copies differ, and both run testdata/frontmatter/cases.json.
//
// This is a reader for the frontmatter Markdown files carry, not YAML: one
// level of `key: value` lines, where a value is a scalar, a comma list
// (`tags: a, b`), a flow list (`tags: [a, b]`) or a block list (`tags:` and
// then `- a` lines). Quotes around a value or an item are dropped.

// parseFrontmatter takes the frontmatter (between --- lines) off Markdown and
// returns its keys as metadata with the rest of the content. Without a
// frontmatter block the metadata is nil and the content comes back trimmed.
func parseFrontmatter(content string) (map[string][]string, string) {
	content = strings.TrimSpace(content)
	block, body, ok := splitFrontmatter(content)
	if !ok {
		return nil, content
	}
	return parseFrontmatterBlock(block), body
}

// splitFrontmatter separates the lines between the opening and closing ---
// from the body after them; ok is false when content has no such block.
func splitFrontmatter(content string) (block, body string, ok bool) {
	if !strings.HasPrefix(content, "---") {
		return "", "", false
	}
	_, rest, found := strings.Cut(content[3:], "\n")
	if !found {
		return "", "", false
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", false
	}
	return rest[:end], strings.TrimSpace(rest[end+4:]), true
}

// parseFrontmatterBlock reads the key: value lines of a frontmatter block.
// A key with no value opens a block list that its following `- item` lines
// fill; a key that ends up with no values is left out.
func parseFrontmatterBlock(block string) map[string][]string {
	r := frontmatterReader{meta: make(map[string][]string)}
	for _, raw := range strings.Split(block, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r.read(line)
	}
	return r.meta
}

// frontmatterReader is the state of reading a block: the metadata so far
// and the key whose block list is open, if any.
type frontmatterReader struct {
	meta    map[string][]string
	listKey string
}

// read takes one non-empty, non-comment line of the block.
func (r *frontmatterReader) read(line string) {
	if item, isItem := frontmatterItem(line); isItem {
		if r.listKey != "" && item != "" {
			r.meta[r.listKey] = append(r.meta[r.listKey], item)
		}
		return
	}
	r.listKey = ""
	key, val, found := strings.Cut(line, ":")
	key, val = strings.TrimSpace(key), strings.TrimSpace(val)
	if !found || key == "" {
		return
	}
	if val == "" {
		r.listKey = key
		return
	}
	if values := frontmatterValues(val); len(values) > 0 {
		r.meta[key] = values
	}
}

// frontmatterItem reports whether line is a block-list item and returns the
// item without its quotes. An unquoted `key: value` item opens a map, which
// flat meta cannot hold, so it comes back empty and is not stored.
func frontmatterItem(line string) (string, bool) {
	if line == "-" {
		return "", true
	}
	if !strings.HasPrefix(line, "- ") {
		return "", false
	}
	item := strings.TrimSpace(line[2:])
	if strings.Contains(item, ": ") && !strings.ContainsAny(item[:1], `"'`) {
		return "", true
	}
	return frontmatterUnquote(item), true
}

// frontmatterValues turns a value into its items: a flow list's or a comma
// list's, or the scalar on its own.
func frontmatterValues(val string) []string {
	if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
		return frontmatterList(val[1 : len(val)-1])
	}
	val = frontmatterUnquote(val)
	if strings.Contains(val, ",") {
		return frontmatterList(val)
	}
	if val == "" {
		return nil
	}
	return []string{val}
}

// frontmatterList splits a comma list, dropping empty items and quotes.
func frontmatterList(s string) []string {
	var items []string
	for _, part := range strings.Split(s, ",") {
		if item := frontmatterUnquote(strings.TrimSpace(part)); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// frontmatterUnquote drops the quotes around a value.
func frontmatterUnquote(s string) string {
	return strings.Trim(s, `"'`)
}
