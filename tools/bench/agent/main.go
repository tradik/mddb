// Command agent measures what an agent pays to find code (AGT-004).
//
// Each task is a question, the term an agent would grep for, and the file
// that answers it. The tool runs every task two ways and counts estimated
// tokens (4 bytes each, as the server estimates) and tool calls:
//
//   - grep: search the tracked files (git ls-files, as ripgrep skips what
//     .gitignore lists) for the term, output capped at 250 lines as agent
//     grep tools cap it, then read files. "best" reads only the answer file;
//     "typical" reads the matching files, most matches first, until it
//     reaches the answer.
//
//   - mddb: one search returning passages (format "passages"); the answer
//     counts as found when a passage comes from the answer file.
//
//     go run ./agent -url http://localhost:11023 -collection mddb -root ../.. \
//     -tasks evalsets/agent-tasks.json
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	grepLineCap = 250
	maxFileSize = 1 << 20
)

type task struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Grep     string `json:"grep"`
	Answer   string `json:"answer"`
}

// pathCost is one way of finding the answer.
type pathCost struct {
	Tokens int  `json:"tokens"`
	Calls  int  `json:"calls"`
	Found  bool `json:"found"`
}

type taskResult struct {
	ID          string   `json:"id"`
	GrepBest    pathCost `json:"grepBest"`
	GrepTypical pathCost `json:"grepTypical"`
	MDDB        pathCost `json:"mddb"`
	MDDBMs      float64  `json:"mddbMs"`
}

func tokens(n int) int { return (n + 3) / 4 }

// textFiles lists the files an agent's grep would search: the tracked files
// of a git checkout, else every file outside VCS and dependency directories.
// Large and binary files are skipped when read.
func textFiles(root string) ([]string, error) {
	if out, err := exec.Command("git", "-C", root, "ls-files").Output(); err == nil && len(out) > 0 { // #nosec G204 -- fixed command and arguments; root is the tree the user pointed at
		return strings.Split(strings.TrimSpace(string(out)), "\n"), nil
	}
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "dist") {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileSize {
			return nil //nolint:nilerr // unreadable entries are skipped, as grep -s does
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}

func readText(root, rel string) ([]byte, bool) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))) // #nosec G304 -- the benchmark reads the tree it was pointed at
	if err != nil || bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
		return nil, false
	}
	return b, true
}

// grepPath costs the grep route for one task.
func grepPath(root string, files []string, t task) (best, typical pathCost) {
	var out bytes.Buffer
	lines := 0
	var matched []string
	count := map[string]int{}
	for _, rel := range files {
		b, ok := readText(root, rel)
		if !ok {
			continue
		}
		hit := false
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(make([]byte, 0, 64*1024), maxFileSize)
		for n := 1; sc.Scan(); n++ {
			if strings.Contains(sc.Text(), t.Grep) {
				hit = true
				count[rel]++
				if lines < grepLineCap {
					fmt.Fprintf(&out, "%s:%d:%s\n", rel, n, sc.Text())
					lines++
				}
			}
		}
		if hit {
			matched = append(matched, rel)
		}
	}
	grepTokens := tokens(out.Len())
	best = pathCost{Tokens: grepTokens, Calls: 1}
	typical = best
	sort.SliceStable(matched, func(i, j int) bool { return count[matched[i]] > count[matched[j]] })
	for _, rel := range matched {
		b, _ := readText(root, rel)
		typical.Tokens += tokens(len(b))
		typical.Calls++
		if rel == t.Answer {
			typical.Found = true
			best.Tokens += tokens(len(b))
			best.Calls++
			best.Found = true
			break
		}
	}
	return best, typical
}

// mddbPath costs one passages search.
func mddbPath(client *http.Client, url, collection, search string, topK int, t task) (pathCost, time.Duration, error) {
	body, _ := json.Marshal(map[string]any{"collection": collection, "query": t.Question, "topK": topK, "limit": topK,
		"format": "passages"})
	endpoint := map[string]string{"fts": "/v1/fts", "vector": "/v1/vector-search", "hybrid": "/v1/hybrid-search"}[search]
	if endpoint == "" {
		return pathCost{}, 0, fmt.Errorf("unknown search %q", search)
	}
	start := time.Now()
	resp, err := client.Post(url+endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return pathCost{}, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	elapsed := time.Since(start)
	if err != nil {
		return pathCost{}, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return pathCost{}, 0, fmt.Errorf("%s: %d %s", endpoint, resp.StatusCode, raw)
	}
	var parsed struct {
		Passages []struct {
			Path string `json:"path"`
			Key  string `json:"key"`
		} `json:"passages"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return pathCost{}, 0, err
	}
	c := pathCost{Tokens: tokens(len(raw)), Calls: 1}
	for _, p := range parsed.Passages {
		if p.Path == t.Answer || p.Key == t.Answer {
			c.Found = true
		}
	}
	return c, elapsed, nil
}

func loadTasks(path string) ([]task, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the user names the file
	if err != nil {
		return nil, err
	}
	var f struct {
		Tasks []task `json:"tasks"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if len(f.Tasks) == 0 {
		return nil, errors.New("no tasks")
	}
	return f.Tasks, nil
}

func main() {
	os.Exit(cli(os.Args[1:], os.Stdout, os.Stderr))
}

// cli parses flags, runs the benchmark and returns the exit code.
func cli(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("agent", flag.ContinueOnError)
	fl.SetOutput(stderr)
	url := fl.String("url", "http://localhost:11023", "MDDB base URL")
	collection := fl.String("collection", "mddb", "Collection made with mddb-cli autoindex from -root")
	root := fl.String("root", ".", "The tree the collection was indexed from")
	tasksPath := fl.String("tasks", "evalsets/agent-tasks.json", "Tasks file")
	search := fl.String("search", "hybrid", "MDDB search: fts, vector or hybrid")
	topK := fl.Int("top-k", 5, "Passages per MDDB search")
	asJSON := fl.Bool("json", false, "Print JSON")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if err := run(stdout, &http.Client{Timeout: 60 * time.Second}, *url, *collection, *root, *tasksPath, *search, *topK, *asJSON); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
