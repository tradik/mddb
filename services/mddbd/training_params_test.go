package main

import (
	"math"
	"strings"
	"testing"
)

// param builds a validated declaration.
func param(t *testing.T, p trainerParam) *trainerParam {
	t.Helper()
	if len(p.Args) == 0 {
		p.Args = []string{"--" + p.Name, "{value}"}
	}
	if err := p.validate(); err != nil {
		t.Fatalf("%s: %v", p.Name, err)
	}
	return &p
}

func TestTrainerParamValues(t *testing.T) {
	lo, hi := 0.5, 2.0
	minOnly := param(t, trainerParam{Name: "a", Type: paramFloat, Min: &lo})
	maxOnly := param(t, trainerParam{Name: "b", Type: paramInt, Max: &hi})
	both := param(t, trainerParam{Name: "c", Type: paramFloat, Min: &lo, Max: &hi})
	spaced := param(t, trainerParam{Name: "d", Type: paramString, Pattern: `^[a-z ]+$`})
	for _, c := range []struct {
		p    *trainerParam
		v    any
		want string
		ok   bool
	}{
		{minOnly, 0.75, "0.75", true}, {minOnly, 0.1, "at least 0.5", false},
		{maxOnly, int64(2), "2", true}, {maxOnly, 3, "at most 2", false},
		{both, 3.0, "from 0.5 to 2", false}, {both, math.NaN(), "a number", false},
		{both, math.Inf(1), "a number", false}, {both, "1", "a number", false},
		{maxOnly, float64(1 << 54), "whole", false},
		{spaced, "two words", "two words", true}, {spaced, "Caps", "not allowed", false}, {spaced, 1.0, "a string", false},
	} {
		got, err := c.p.check(c.v)
		if c.ok && (err != nil || got != c.want) || !c.ok && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s(%v): %q %v", c.p.Name, c.v, got, err)
		}
	}
}

func TestResolveParams(t *testing.T) {
	params := []*trainerParam{
		param(t, trainerParam{Name: "epochs", Type: paramInt, Default: 3}),
		param(t, trainerParam{Name: "fast", Type: paramBool, Args: []string{"--fast"}}),
		param(t, trainerParam{Name: "need", Type: paramString, Required: true}),
		param(t, trainerParam{Name: "opt", Type: paramString}),
	}
	if _, _, err := resolveParams(params, map[string]any{}, nil); err == nil || !strings.Contains(err.Error(), "need: required") {
		t.Errorf("required: %v", err)
	}
	used, args, err := resolveParams(params, map[string]any{"need": "x", "fast": false, "epochs": nil}, map[string]any{"epochs": 1})
	if err != nil || used["epochs"] != "1" || used["fast"] != "false" || len(args) != 2 {
		t.Fatalf("resolve: %v %v %v", used, args, err)
	}
	// A value is expanded once: placeholder text it brings stays text.
	argv := buildArgv([]string{"run", "--data={dataset}"}, []paramArgs{{templates: []string{"--tag={value}"}, value: "{dataset}"}},
		map[string]string{"{dataset}": "/d/train.jsonl"})
	if strings.Join(argv, " ") != "run --data=/d/train.jsonl --tag={dataset}" {
		t.Errorf("argv: %q", argv)
	}
}
