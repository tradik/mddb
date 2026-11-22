package main

import (
	"fmt"
	"strings"
)

// From a request's values to a command's arguments: each value checked
// against its declaration (training_params.go), then put into its
// parameter's argument template as one argv element.

// findParam is the declaration named name, or nil.
func findParam(params []*trainerParam, name string) *trainerParam {
	for _, p := range params {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// paramArgs is one parameter's argument template and the value it takes.
type paramArgs struct {
	templates []string
	value     string
}

// resolveParams checks a request's values against the declarations and
// returns the values used, as text, and their argument templates in
// declaration order. overrides (a trial's trialParameters) win over values.
// A name the profile does not declare is refused.
func resolveParams(params []*trainerParam, values, overrides map[string]any) (map[string]string, []paramArgs, error) {
	for name := range values {
		if findParam(params, name) == nil {
			return nil, nil, fmt.Errorf("unknown parameter %q", name)
		}
	}
	used := map[string]string{}
	var args []paramArgs
	for _, p := range params {
		v, ok := p.valueOf(values, overrides)
		if !ok {
			if p.Required {
				return nil, nil, fmt.Errorf("%s: required", p.Name)
			}
			continue
		}
		text, err := p.check(v)
		if err != nil {
			return nil, nil, err
		}
		used[p.Name] = text
		if p.Type == paramBool && text == "false" {
			continue
		}
		args = append(args, paramArgs{templates: p.Args, value: text})
	}
	return used, args, nil
}

// valueOf is the value a request gives the parameter: the override, else
// the value, else the default (a null counts as no value); false when there
// is none.
func (p *trainerParam) valueOf(values, overrides map[string]any) (any, bool) {
	v, ok := overrides[p.Name]
	if !ok {
		v, ok = values[p.Name]
	}
	if ok && v != nil {
		return v, true
	}
	return p.Default, p.Default != nil
}

// buildArgv is the command's arguments: the fixed ones, then each
// parameter's, every element expanded in one pass, so text a value brings
// in is never expanded again.
func buildArgv(fixed []string, params []paramArgs, places map[string]string) []string {
	argv := expandArgs(fixed, places, "")
	for _, p := range params {
		argv = append(argv, expandArgs(p.templates, places, p.value)...)
	}
	return argv
}

// expandArgs replaces the placeholders of templates, {value} with value.
func expandArgs(templates []string, places map[string]string, value string) []string {
	pairs := make([]string, 0, 2*len(places)+2)
	for k, v := range places {
		pairs = append(pairs, k, v)
	}
	r := strings.NewReplacer(append(pairs, "{value}", value)...)
	out := make([]string, len(templates))
	for i, t := range templates {
		out[i] = r.Replace(t)
	}
	return out
}
