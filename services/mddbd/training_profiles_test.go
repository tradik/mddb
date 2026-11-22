package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeTrainers writes a profile file with {{exe}} replaced and points
// MDDB_TRAINERS_CONFIG at it.
func writeTrainers(t *testing.T, yaml string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "trainers.yaml")
	if err := os.WriteFile(p, []byte(strings.ReplaceAll(yaml, "{{exe}}", exe)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(trainersConfigEnv, p)
}

func TestTrainerProfilesLoadAndDefaults(t *testing.T) {
	t.Setenv(trainersConfigEnv, "")
	if p := loadTrainerProfiles(); len(p.list) != 0 || len(p.errors) != 0 {
		t.Fatalf("no file: %+v", p)
	}
	writeTrainers(t, "trainers:\n  - {name: a, command: '{{exe}}'}\n")
	p := loadTrainerProfiles()
	if len(p.list) != 1 {
		t.Fatalf("load: %+v", p.errors)
	}
	a, ok := p.byName("a")
	if !ok || a.Output != defaultTrainOutput || a.MaxConcurrent != 1 || a.TrialRecords != defaultTrialRecords ||
		a.timeout != defaultTrainTimeout || a.trialTimeout != defaultTrainTimeout || len(a.trainerEnvNames()) != len(defaultTrainerEnv) {
		t.Errorf("defaults: %+v", a)
	}
	if _, ok := p.byName("b"); ok {
		t.Error("an unknown profile was found")
	}
	writeTrainers(t, "")
	if p := loadTrainerProfiles(); len(p.list) != 0 || len(p.errors) != 0 {
		t.Errorf("empty file: %+v", p)
	}
	t.Setenv(trainersConfigEnv, filepath.Join(t.TempDir(), "missing.yaml"))
	if p := loadTrainerProfiles(); len(p.errors) != 1 {
		t.Errorf("missing file: %+v", p)
	}
	writeTrainers(t, "trainers:\n  - {name: a, comand: x}\n")
	if p := loadTrainerProfiles(); len(p.errors) != 1 || !strings.Contains(p.errors[0], "comand") {
		t.Errorf("unknown key: %+v", p.errors)
	}
}

func TestTrainerProfilesRefuseUnsafeConfiguration(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"name":            "{name: 'a b', command: '{{exe}}'}",
		"description":     "{name: a, command: '{{exe}}', description: '" + strings.Repeat("d", 1001) + "'}",
		"relative":        "{name: a, command: train.sh}",
		"unclean":         "{name: a, command: '" + dir + "/../x'}",
		"missing":         "{name: a, command: '" + filepath.Join(dir, "none") + "'}",
		"directory":       "{name: a, command: '" + dir + "'}",
		"output dir":      "{name: a, command: '{{exe}}', output: 'out/*.gguf'}",
		"output pattern":  "{name: a, command: '{{exe}}', output: '['}",
		"progress":        "{name: a, command: '{{exe}}', progress: \"a\\nb\"}",
		"trial timeout":   "{name: a, command: '{{exe}}', trialTimeout: soon}",
		"trial unknown":   "{name: a, command: '{{exe}}', trialParameters: {x: 1}}",
		"trial value":     "{name: a, command: '{{exe}}', parameters: [{name: n, type: int, max: 2, args: ['{value}']}], trialParameters: {n: 3}}",
		"evaluate":        "{name: a, command: '{{exe}}', evaluate: {command: rel}}",
		"too many args":   "{name: a, command: '{{exe}}', args: [" + strings.Repeat("x,", maxTrainerArgs) + "x]}",
		"placeholder":     "{name: a, command: '{{exe}}', args: ['{secret}']}",
		"value in fixed":  "{name: a, command: '{{exe}}', args: ['{value}']}",
		"arg newline":     "{name: a, command: '{{exe}}', args: [\"a\\nb\"]}",
		"param nil":       "{name: a, command: '{{exe}}', parameters: [null]}",
		"param twice":     "{name: a, command: '{{exe}}', parameters: [{name: n, type: bool, args: [x]}, {name: n, type: bool, args: [x]}]}",
		"param name":      "{name: a, command: '{{exe}}', parameters: [{name: 1n, type: bool, args: [x]}]}",
		"param type":      "{name: a, command: '{{exe}}', parameters: [{name: n, type: list, args: [x]}]}",
		"enum empty":      "{name: a, command: '{{exe}}', parameters: [{name: n, type: enum, args: [x]}]}",
		"enum value":      "{name: a, command: '{{exe}}', parameters: [{name: n, type: enum, values: ['-x'], args: [x]}]}",
		"min max":         "{name: a, command: '{{exe}}', parameters: [{name: n, type: int, min: 2, max: 1, args: [x]}]}",
		"max length":      "{name: a, command: '{{exe}}', parameters: [{name: n, type: string, maxLength: 99999, args: [x]}]}",
		"pattern":         "{name: a, command: '{{exe}}', parameters: [{name: n, type: string, pattern: '(', args: [x]}]}",
		"no args":         "{name: a, command: '{{exe}}', parameters: [{name: n, type: bool}]}",
		"param template":  "{name: a, command: '{{exe}}', parameters: [{name: n, type: bool, args: ['{nope}']}]}",
		"default":         "{name: a, command: '{{exe}}', parameters: [{name: n, type: int, default: x, args: [x]}]}",
		"env name":        "{name: a, command: '{{exe}}', env: ['A-B']}",
		"env server":      "{name: a, command: '{{exe}}', env: [MDDB_JWT_SECRET]}",
		"env server case": "{name: a, command: '{{exe}}', env: [mddb_path]}",
		"work dir":        "{name: a, command: '{{exe}}', workDir: rel}",
		"work dir file":   "{name: a, command: '{{exe}}', workDir: '" + plain + "'}",
		"work dir gone":   "{name: a, command: '{{exe}}', workDir: '" + filepath.Join(dir, "gone") + "'}",
		"timeout":         "{name: a, command: '{{exe}}', timeout: 0s}",
		"too many params": "{name: a, command: '{{exe}}', parameters: [" + strings.Repeat("{name: n, type: bool, args: [x]},", maxTrainerParameters) + "{name: m, type: bool, args: [x]}]}",
	}
	if runtime.GOOS != "windows" {
		cases["not executable"] = "{name: a, command: '" + plain + "'}"
	}
	for name, profile := range cases {
		writeTrainers(t, "trainers:\n  - "+profile+"\n  - {name: ok, command: '{{exe}}'}\n")
		p := loadTrainerProfiles()
		if len(p.list) != 1 || p.list[0].Name != "ok" || len(p.errors) != 1 {
			t.Errorf("%s: %d profiles, errors %v", name, len(p.list), p.errors)
		}
	}
	writeTrainers(t, "trainers:\n  - {name: ok, command: '{{exe}}'}\n  - {name: ok, command: '{{exe}}'}\n  - null\n")
	if p := loadTrainerProfiles(); len(p.list) != 1 || !strings.Contains(p.errors[0], "earlier") {
		t.Errorf("duplicate name: %+v", p.errors)
	}
	writeTrainers(t, "trainers:\n  - {name: w, command: '{{exe}}', workDir: '"+dir+"', env: [PATH], trialTimeout: 1m, timeout: 2h}\n")
	if p := loadTrainerProfiles(); len(p.list) != 1 || workDir(&p.list[0].commandSpec, "job") != dir || p.list[0].trainerEnvNames()[0] != "PATH" {
		t.Errorf("workDir and env: %+v", p.errors)
	}
}
