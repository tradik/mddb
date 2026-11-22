package main

import (
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The document of an issue or pull request (INT-023): Markdown with the
// title as its heading, the body, and one section per comment, so chunking
// and passages point at the comment that matched; and flat meta to filter
// and aggregate on, among it the items and files the text names.

// maxIssuePaths bounds the file paths kept in an item's meta.
const maxIssuePaths = 50

// renderIssue is an item's document: its body and its meta.
func renderIssue(it issueItem, comments []issueComment, repo gitRepo) (string, map[string][]string) {
	var b strings.Builder
	b.WriteString("# " + it.title + "\n")
	if body := strings.TrimSpace(it.body); body != "" {
		b.WriteString("\n" + body + "\n")
	}
	texts := []string{it.title, it.body}
	for _, c := range comments {
		b.WriteString("\n## Comment by " + c.author + " on " + dateOf(c.createdAt) + "\n")
		if body := strings.TrimSpace(c.body); body != "" {
			b.WriteString("\n" + body + "\n")
		}
		texts = append(texts, c.body)
	}
	text := strings.Join(texts, "\n")
	issues, pulls := issueLinks(text, repo, it)
	meta := map[string][]string{
		"title": {it.title}, "number": {strconv.Itoa(it.number)}, metaKind: {it.kind}, "state": {it.state},
		"author": {it.author}, "createdAt": {it.createdAt}, "updatedAt": {it.updatedAt},
		"comments": {strconv.Itoa(it.comments)}, "url": {it.url},
	}
	for k, v := range map[string]string{"stateReason": it.stateReason, "milestone": it.milestone, "closedAt": it.closedAt} {
		if v != "" {
			meta[k] = []string{v}
		}
	}
	for k, v := range map[string][]string{"labels": it.labels, "assignees": it.assignees,
		"linkedIssues": issues, "linkedPulls": pulls, "paths": mentionedPaths(text)} {
		if len(v) > 0 {
			meta[k] = v
		}
	}
	return b.String(), meta
}

// dateOf is the day of an RFC 3339 time; the text itself when it is none.
func dateOf(ts string) string {
	if day, _, ok := strings.Cut(ts, "T"); ok {
		return day
	}
	return ts
}

// Item references in text: #123 (an issue on GitLab; on GitHub and Gitea,
// which number both alike, either), GitLab's !123 (a merge request), and
// links to the repository's own items.
var (
	issueHashRef = regexp.MustCompile(`(?:^|[^\w&/#])#(\d+)\b`)
	mergeBangRef = regexp.MustCompile(`(?:^|[^\w!])!(\d+)\b`)
	issueURLRef  = regexp.MustCompile(`^/(-/)?(issues|pull|pulls|merge_requests)/(\d+)\b`)
	urlInText    = regexp.MustCompile(`https?://[^\s)>\]"'` + "`" + `]+`)
	pathInText   = regexp.MustCompile(`(?:\.{0,2}/)?[A-Za-z0-9_@+-][A-Za-z0-9_.@+-]*(?:/[A-Za-z0-9_.@+-]+)*\.[A-Za-z][A-Za-z0-9]{0,7}\b`)
)

// issueLinks are the numbers of the issues and pull requests the text names,
// the item's own number left out.
func issueLinks(text string, repo gitRepo, self issueItem) (issues, pulls []string) {
	add := func(list *[]string, kind, n string) {
		if (kind != self.kind || n != strconv.Itoa(self.number)) && !slices.Contains(*list, n) {
			*list = append(*list, n)
		}
	}
	for _, u := range urlInText.FindAllString(text, -1) {
		rest, ok := strings.CutPrefix(u, repo.web)
		if !ok {
			continue
		}
		if m := issueURLRef.FindStringSubmatch(rest); m != nil {
			if m[2] == "issues" {
				add(&issues, issueKindIssue, m[3])
			} else {
				add(&pulls, issueKindPull, m[3])
			}
		}
	}
	plain := urlInText.ReplaceAllString(text, " ")
	for _, m := range issueHashRef.FindAllStringSubmatch(plain, -1) {
		add(&issues, issueKindIssue, m[1])
	}
	if repo.forge == forgeGitLab {
		for _, m := range mergeBangRef.FindAllStringSubmatch(plain, -1) {
			add(&pulls, issueKindPull, m[1])
		}
	}
	sortNumeric(issues)
	sortNumeric(pulls)
	return issues, pulls
}

// sortNumeric sorts numbers given as text.
func sortNumeric(list []string) {
	slices.SortFunc(list, func(a, b string) int {
		x, _ := strconv.Atoi(a)
		y, _ := strconv.Atoi(b)
		return x - y
	})
}

// mentionedPaths are the file paths the text names: a path with a directory
// and an extension (internal/cart/discount.go, ./main.go:42), or a bare
// file name whose extension is one the code index knows (README.md). URLs
// are not paths.
func mentionedPaths(text string) []string {
	var out []string
	for _, p := range pathInText.FindAllString(urlInText.ReplaceAllString(text, " "), -1) {
		p = strings.TrimPrefix(strings.TrimPrefix(p, "./"), "../")
		ext := strings.ToLower(path.Ext(p))
		known := codeExtensions[ext] != "" || docExtensions[ext] || gitMarkupFormats[ext] != ""
		if (!strings.Contains(p, "/") && !known) || slices.Contains(out, p) {
			continue
		}
		out = append(out, p)
		if len(out) == maxIssuePaths {
			break
		}
	}
	slices.Sort(out)
	return out
}
