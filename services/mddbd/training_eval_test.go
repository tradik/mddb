package main

import (
	"strings"
	"testing"
)

func TestParseEvalReport(t *testing.T) {
	samples := strings.Repeat(`{"input":"`+strings.Repeat("i", maxEvalText+5)+`","expected":"e","actual":"a","correct":true},`, maxEvalSamples+3)
	rep, err := parseEvalReport([]byte(`{"total":4,"correct":3,"score":0.75,"metrics":{"exact":0.75},"samples":[` + strings.TrimSuffix(samples, ",") + `],"extra":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total != 4 || len(rep.Samples) != maxEvalSamples || len([]rune(rep.Samples[0].Input)) != maxEvalText+1 {
		t.Errorf("report: %d samples, input %d", len(rep.Samples), len(rep.Samples[0].Input))
	}
	many := "{"
	for i := 0; i <= maxEvalMetrics; i++ {
		many += `"m` + strings.Repeat("x", i) + `":1,`
	}
	many = strings.TrimSuffix(many, ",") + "}"
	for name, raw := range map[string]string{
		"not json":      `[1]`,
		"no total":      `{"correct":1,"score":1}`,
		"fraction":      `{"total":1.5,"correct":1,"score":1}`,
		"negative":      `{"total":-1,"correct":0,"score":0}`,
		"correct>total": `{"total":1,"correct":2,"score":1}`,
		"no score":      `{"total":1,"correct":1}`,
		"score>1":       `{"total":1,"correct":1,"score":1.5}`,
		"metric name":   `{"total":1,"correct":1,"score":1,"metrics":{"":1}}`,
		"too many":      `{"total":1,"correct":1,"score":1,"metrics":` + many + `}`,
		"metric type":   `{"total":1,"correct":1,"score":1,"metrics":{"a":"x"}}`,
	} {
		if _, err := parseEvalReport([]byte(raw)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestEvaluationCompare(t *testing.T) {
	rep := func(s float64) *evalStage { return &evalStage{Status: BulkJobCompleted, Report: &evalReport{Score: s}} }
	e := &trainingEvaluation{Base: rep(0.5), Result: rep(0.5)}
	if e.compare(); e.Delta == nil || *e.Delta != 0 || len(e.Notes) != 1 {
		t.Errorf("equal scores: %+v", e)
	}
	e = &trainingEvaluation{Base: &evalStage{Status: BulkJobFailed}, Result: rep(0.9)}
	if e.compare(); e.Delta != nil || !strings.Contains(e.Notes[0], "No baseline") {
		t.Errorf("failed baseline: %+v", e)
	}
	e = &trainingEvaluation{}
	if e.compare(); e.Delta != nil || e.Notes != nil {
		t.Errorf("nothing: %+v", e)
	}
}
