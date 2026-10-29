package main

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// File discovery and classification for autoindex (AGT-001).
//
// The CLI walks the directory, never the server: a server that accepts a path
// to its own filesystem over HTTP is a path-traversal endpoint waiting to be
// found. What reaches the server is documents, as any client sends them.

// defaultExcludedDirs are skipped outside a git repository, where there is no
// .gitignore to say so: build output, dependencies and caches.
var defaultExcludedDirs = map[string]bool{
	".git": true, "node_modules": true, "target": true, "dist": true, "build": true,
	"vendor": true, ".venv": true, "venv": true, "__pycache__": true, ".idea": true, ".vscode": true,
}

// docExtensions are prose: stored as kind "doc".
var docExtensions = map[string]bool{".md": true, ".markdown": true, ".mdx": true}

// sniffBytes is how much of a file is read to decide what it is.
const sniffBytes = 8 << 10

// autoFile is one file to index.
type autoFile struct {
	Rel      string `json:"path"`
	abs      string
	Size     int64 `json:"size"`
	modified string
	Kind     string `json:"kind,omitempty"`
	Language string `json:"language,omitempty"`
	// Format is the format the server converts the file from (pdf, docx,
	// html, ...); empty for a file stored as the text it is.
	Format string `json:"format,omitempty"`
}

// autoSkip is a file left out, and why.
type autoSkip struct {
	Rel    string `json:"path"`
	Reason string `json:"reason"`
}

// classifier decides what each file is, from the server's extension table.
type classifier struct {
	extensions map[string]string // nil when the server could not be asked
	include    []string
	exclude    []string
	maxSize    int64
}

// matchAny reports whether rel or its base name matches one of the globs.
func matchAny(globs []string, rel string) bool {
	for _, g := range globs {
		if ok, _ := path.Match(g, rel); ok {
			return true
		}
		if ok, _ := path.Match(g, path.Base(rel)); ok {
			return true
		}
		if strings.HasSuffix(g, "/**") && strings.HasPrefix(rel, strings.TrimSuffix(g, "**")) {
			return true
		}
	}
	return false
}

// classify returns the file to index, or why it is skipped.
func (c classifier) classify(root, rel string) (*autoFile, *autoSkip) {
	if len(c.include) > 0 && !matchAny(c.include, rel) {
		return nil, &autoSkip{rel, "not included"}
	}
	if matchAny(c.exclude, rel) {
		return nil, &autoSkip{rel, "excluded"}
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(abs)
	switch {
	case err != nil:
		return nil, &autoSkip{rel, "unreadable"}
	case !info.Mode().IsRegular():
		return nil, &autoSkip{rel, "not a regular file"}
	case info.Size() > c.maxSize:
		return nil, &autoSkip{rel, "larger than --max-file-size"}
	}
	format, binary := sniffFile(abs)
	if binary {
		return nil, &autoSkip{rel, "binary"}
	}
	f := &autoFile{Rel: rel, abs: abs, Size: info.Size(), modified: info.ModTime().UTC().Format(time.RFC3339), Format: format}
	ext := strings.ToLower(path.Ext(rel))
	switch {
	case format != "", docExtensions[ext]:
		// A converted file is prose once the server has made Markdown of
		// it, whatever the code table says about its extension.
		f.Kind = "doc"
	case c.extensions == nil:
		// Without the server's table the server classifies on read, from
		// meta.path; an explicit kind here would override that.
	case c.extensions[ext] != "":
		f.Kind, f.Language = "code", c.extensions[ext]
	default:
		f.Kind = "text"
	}
	return f, nil
}

// walkFiles lists the files under root outside a git repository, skipping
// the default excluded directories. Paths are slash-separated and relative.
func walkFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable subtree is skipped, not fatal
		}
		if d.IsDir() {
			if p != root && defaultExcludedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// includeDirs finds C/C++ include roots: directories named "include" that
// hold headers, as collection-relative paths for code.includePaths.
func includeDirs(files []autoFile) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		if f.Language != "c" && f.Language != "cpp" {
			continue
		}
		for dir := path.Dir(f.Rel); dir != "." && dir != "/"; dir = path.Dir(dir) {
			if path.Base(dir) == "include" && !seen[dir] {
				seen[dir] = true
				out = append(out, dir)
			}
		}
	}
	sort.Strings(out)
	return out
}

// sanitizeCollection turns a directory or repository name into a collection
// name: lower case, letters, digits, "-" and "_", at most 64 characters.
func sanitizeCollection(name string) string {
	name = strings.TrimSuffix(strings.ToLower(name), ".git")
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 64 {
		out = out[:64]
	}
	if out == "" {
		out = "default"
	}
	return out
}
