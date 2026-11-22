package main

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	json "mddb/internal/jsonx"
)

// Evaluation reports. MDDB runs no model: an operator's evaluate command
// scores the base model and the tuned one on the validation part and writes
// a JSON report, which is checked here — size, types, counts — and stored
// on the job as data. Nothing in it is executed or rendered as markup.

// Limits of a report.
const (
	maxEvalReportBytes = 1 << 20
	maxEvalSamples     = 20
	maxEvalMetrics     = 32
	maxEvalMetricName  = 64
	maxEvalText        = 2000
)

// evalReport is a validated report.
type evalReport struct {
	Total   int                `json:"total"`
	Correct int                `json:"correct"`
	Score   float64            `json:"score"`
	Metrics map[string]float64 `json:"metrics,omitempty"`
	Samples []evalSample       `json:"samples,omitempty"`
}

// evalSample is one scored validation record.
type evalSample struct {
	Input    string `json:"input"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Correct  bool   `json:"correct"`
}

// evalStage is the outcome of one evaluation stage.
type evalStage struct {
	Status   BulkJobStatus `json:"status"`
	Error    string        `json:"error,omitempty"`
	ExitCode *int          `json:"exitCode,omitempty"`
	Report   *evalReport   `json:"report,omitempty"`
}

// trainingEvaluation is a run's before and after.
type trainingEvaluation struct {
	Skipped string     `json:"skipped,omitempty"`
	Base    *evalStage `json:"base,omitempty"`
	Result  *evalStage `json:"result,omitempty"`
	Delta   *float64   `json:"delta,omitempty"`
	Notes   []string   `json:"notes,omitempty"`
}

// rawEvalReport is a report as written, every field checked before use.
type rawEvalReport struct {
	Total   *float64           `json:"total"`
	Correct *float64           `json:"correct"`
	Score   *float64           `json:"score"`
	Metrics map[string]float64 `json:"metrics"`
	Samples []evalSample       `json:"samples"`
}

// readEvalReport reads and checks a report from a file the caller opened
// inside the job directory.
func readEvalReport(f *os.File) (*evalReport, error) {
	data, err := io.ReadAll(io.LimitReader(f, maxEvalReportBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxEvalReportBytes {
		return nil, fmt.Errorf("the report is larger than %d bytes", maxEvalReportBytes)
	}
	return parseEvalReport(data)
}

// parseEvalReport checks a report's types and bounds.
func parseEvalReport(data []byte) (*evalReport, error) {
	var raw rawEvalReport
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("the report is not the JSON object the contract describes: %w", err)
	}
	total, okT := wholeCount(raw.Total)
	correct, okC := wholeCount(raw.Correct)
	if !okT || !okC || correct > total {
		return nil, errors.New("total and correct are whole numbers, correct at most total")
	}
	if raw.Score == nil || math.IsNaN(*raw.Score) || *raw.Score < 0 || *raw.Score > 1 {
		return nil, errors.New("score is a number from 0 to 1")
	}
	if len(raw.Metrics) > maxEvalMetrics {
		return nil, fmt.Errorf("at most %d metrics", maxEvalMetrics)
	}
	for name, v := range raw.Metrics {
		if name == "" || len(name) > maxEvalMetricName || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("metric %q: a name of at most %d bytes and a finite number", cutText(name, maxEvalMetricName), maxEvalMetricName)
		}
	}
	rep := &evalReport{Total: total, Correct: correct, Score: *raw.Score, Metrics: raw.Metrics}
	for i := 0; i < len(raw.Samples) && i < maxEvalSamples; i++ {
		s := raw.Samples[i]
		rep.Samples = append(rep.Samples, evalSample{Input: cutText(s.Input, maxEvalText),
			Expected: cutText(s.Expected, maxEvalText), Actual: cutText(s.Actual, maxEvalText), Correct: s.Correct})
	}
	return rep, nil
}

// wholeCount is a non-negative whole number.
func wholeCount(v *float64) (int, bool) {
	if v == nil || *v < 0 || *v != math.Trunc(*v) || *v > 1<<53 {
		return 0, false
	}
	return int(*v), true
}

// compare fills the difference and the plain notes the panel shows.
func (e *trainingEvaluation) compare() {
	e.Delta, e.Notes = nil, nil
	after := e.Result != nil && e.Result.Report != nil
	before := e.Base != nil && e.Base.Report != nil
	switch {
	case after && before:
		// Rounded: 0.6 - 0.2 is 0.39999999999999997 in binary floating point.
		d := math.Round((e.Result.Report.Score-e.Base.Report.Score)*1e6) / 1e6
		e.Delta = &d
		if d <= 0 {
			e.Notes = append(e.Notes, "The tuned model did not score higher than the base model on the validation records.")
		}
	case after:
		e.Notes = append(e.Notes, "No baseline score: the evaluate command does not score the base model, or that stage failed, so there is nothing to compare with.")
	}
}
