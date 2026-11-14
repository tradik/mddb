package lspindex

import (
	"bytes"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Refusing facts from guessed flags (#418). clangd without a compilation
// database answers every request, from a parse that lacks the project's
// include paths and defines; the protocol carries no sign of it, only a
// line on stderr per file. The run reads stderr line by line and fails when
// any file it indexes was compiled that way.

// maxFallbacks bounds the files kept for the error; one already fails.
const maxFallbacks = 5

// maxStderrLine bounds a line kept while it is incomplete; a longer one is
// no line about a file.
const maxStderrLine = 64 << 10

// fallbacks collects the files a server's stderr names as compiled with
// guessed flags. Lines arrive from exec's stderr copier while the run asks.
type fallbacks struct {
	read  func(line string) string
	mu    sync.Mutex
	files []string
}

// line takes one stderr line.
func (f *fallbacks) line(l string) {
	file := f.read(l)
	if file == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.files) < maxFallbacks && !slices.Contains(f.files, file) {
		f.files = append(f.files, file)
	}
}

// list returns the files seen so far.
func (f *fallbacks) list() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.files...)
}

// lineWriter splits what is written to it into lines for fn. exec writes
// stderr from one goroutine, so it needs no lock.
type lineWriter struct {
	fn  func(string)
	buf []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.fn(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > maxStderrLine {
		w.buf = nil
	}
	w.buf = append([]byte(nil), w.buf...)
	return len(p), nil
}

// fallbackError fails the run when an opened file was compiled with guessed
// flags; nil for a spec that does not look.
func (r *run) fallbackError() error {
	if r.fallbacks == nil {
		return nil
	}
	var hit []string
	for _, file := range r.fallbacks.list() {
		rel, err := filepath.Rel(r.root, filepath.Clean(file))
		if rel = filepath.ToSlash(rel); err == nil && r.opened[rel] {
			hit = append(hit, rel)
		}
	}
	if len(hit) == 0 {
		return nil
	}
	slices.Sort(hit)
	hint := r.spec.FallbackHint
	if len(r.dropped) > 0 {
		// The database exists; it never reached the server (#420).
		hint = fmt.Sprintf("the command in %s is not recognised as %s, so %s was not passed to it; add it to %s",
			r.spec.EnvVar, r.spec.Candidates[0].Name, strings.Join(r.dropped, " "), r.spec.EnvVar)
	}
	return fmt.Errorf("%w: %s; %s", ErrFallbackFlags, strings.Join(hit, ", "), hint)
}
