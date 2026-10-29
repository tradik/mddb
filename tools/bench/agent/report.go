package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// run executes every task and prints the comparison.
func run(w io.Writer, client *http.Client, url, collection, root, tasksPath, search string, topK int, asJSON bool) error {
	tasks, err := loadTasks(tasksPath)
	if err != nil {
		return err
	}
	files, err := textFiles(root)
	if err != nil {
		return err
	}
	results := make([]taskResult, 0, len(tasks))
	for _, t := range tasks {
		r := taskResult{ID: t.ID}
		r.GrepBest, r.GrepTypical = grepPath(root, files, t)
		m, elapsed, err := mddbPath(client, url, collection, search, topK, t)
		if err != nil {
			return fmt.Errorf("%s: %w", t.ID, err)
		}
		r.MDDB, r.MDDBMs = m, float64(elapsed.Microseconds())/1000
		results = append(results, r)
	}
	if asJSON {
		return json.NewEncoder(w).Encode(results)
	}
	printReport(w, results, search)
	return nil
}

// totals sums a column: tokens, calls and tasks found.
func totals(results []taskResult, pick func(taskResult) pathCost) (tokens, calls, found int) {
	for _, r := range results {
		c := pick(r)
		tokens, calls = tokens+c.Tokens, calls+c.Calls
		if c.Found {
			found++
		}
	}
	return tokens, calls, found
}

func printReport(w io.Writer, results []taskResult, search string) {
	_, _ = fmt.Fprintf(w, "%-16s %22s %22s %22s\n", "task", "grep+read (best)", "grep+read (typical)", "mddb "+search)
	cell := func(c pathCost) string {
		mark := " "
		if !c.Found {
			mark = "✗"
		}
		return fmt.Sprintf("%8d tok %2d calls%s", c.Tokens, c.Calls, mark)
	}
	for _, r := range results {
		_, _ = fmt.Fprintf(w, "%-16s %22s %22s %22s\n", r.ID, cell(r.GrepBest), cell(r.GrepTypical), cell(r.MDDB))
	}
	bt, bc, bf := totals(results, func(r taskResult) pathCost { return r.GrepBest })
	tt, tc, tf := totals(results, func(r taskResult) pathCost { return r.GrepTypical })
	mt, mc, mf := totals(results, func(r taskResult) pathCost { return r.MDDB })
	n := len(results)
	_, _ = fmt.Fprintf(w, "\n%-16s %9d tok %3d calls %9d tok %3d calls %9d tok %3d calls\n", "total", bt, bc, tt, tc, mt, mc)
	_, _ = fmt.Fprintf(w, "%-16s %18d/%d %18d/%d %18d/%d\n", "answer found", bf, n, tf, n, mf, n)
	if mt > 0 {
		_, _ = fmt.Fprintf(w, "\ngrep (best) uses %.1f× the tokens of mddb; grep (typical) %.1f×. ✗ = answer not reached.\n",
			float64(bt)/float64(mt), float64(tt)/float64(mt))
	}
}
