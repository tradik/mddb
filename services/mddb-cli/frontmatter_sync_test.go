package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestFrontmatterFileMatchesServer keeps the CLI's frontmatter reader the
// server's (#373): the server's is package main in another module, so the CLI
// carries the same file, and this test fails the moment the two differ.
func TestFrontmatterFileMatchesServer(t *testing.T) {
	for _, name := range []string{"frontmatter.go", "frontmatter_test.go"} {
		ours, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		theirs, err := os.ReadFile(filepath.Join("..", "mddbd", name))
		if err != nil {
			t.Fatalf("read services/mddbd/%s: %v", name, err)
		}
		if !bytes.Equal(ours, theirs) {
			t.Errorf("services/mddb-cli/%s differs from services/mddbd/%s; copy the changed one over the other", name, name)
		}
	}
}
