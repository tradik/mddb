package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// The checks of a trainer profile, run once when the operator's file is
// read: a profile that fails one is refused with the reason, and the others
// still load.

// validate checks a profile and fills its defaults.
func (t *trainerProfile) validate() error {
	if !trainerNamePattern.MatchString(t.Name) {
		return errors.New("name: 1 to 64 letters, digits, '.', '-' or '_'")
	}
	if len(t.Description) > maxTrainerDescription {
		return errors.New("description is too long")
	}
	if err := t.commandSpec.validate(defaultTrainTimeout); err != nil {
		return err
	}
	if t.Output == "" {
		t.Output = defaultTrainOutput
	}
	if _, err := filepath.Match(t.Output, "x"); err != nil || strings.ContainsAny(t.Output, `/\`) {
		return fmt.Errorf("output %q: a file name pattern without a directory", t.Output)
	}
	if t.MaxConcurrent <= 0 {
		t.MaxConcurrent = 1
	}
	if strings.ContainsAny(t.Progress, "\r\n") {
		return errors.New("progress: a prefix on one line")
	}
	if err := t.validateTrial(); err != nil {
		return err
	}
	if t.Evaluate != nil {
		if err := t.Evaluate.validate(defaultEvalTimeout); err != nil {
			return fmt.Errorf("evaluate: %w", err)
		}
	}
	return nil
}

// validateTrial checks the settings of a trial run and fills their
// defaults: the records, the timeout (the run's own) and the parameters a
// trial overrides.
func (t *trainerProfile) validateTrial() error {
	if t.TrialRecords <= 0 {
		t.TrialRecords = defaultTrialRecords
	}
	d, err := durationOr(t.TrialTimeout, t.timeout)
	if err != nil {
		return fmt.Errorf("trialTimeout: %w", err)
	}
	t.trialTimeout = d
	t.TrialTimeout = d.String()
	for name, v := range t.TrialParameters {
		p := findParam(t.Parameters, name)
		if p == nil {
			return fmt.Errorf("trialParameters: %q is not a parameter of the profile", name)
		}
		if _, err := p.check(v); err != nil {
			return fmt.Errorf("trialParameters: %w", err)
		}
	}
	return nil
}

// validate checks a command, its arguments, parameters and environment.
func (c *commandSpec) validate(defaultTimeout time.Duration) error {
	if err := validExecutable(c.Command); err != nil {
		return err
	}
	if len(c.Args) > maxTrainerArgs {
		return errors.New("args: too many")
	}
	if err := validTemplates(c.Args, false); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if err := validParameters(c.Parameters); err != nil {
		return err
	}
	if err := validEnvNames(c.Env); err != nil {
		return err
	}
	if c.WorkDir != "" {
		if err := validDirectory(c.WorkDir); err != nil {
			return fmt.Errorf("workDir: %w", err)
		}
	}
	d, err := durationOr(c.Timeout, defaultTimeout)
	if err != nil {
		return fmt.Errorf("timeout: %w", err)
	}
	c.timeout = d
	c.Timeout = d.String() // the profile list shows what applies
	return nil
}

// validParameters checks the declarations of a command's parameters: each
// valid, and no name twice.
func validParameters(params []*trainerParam) error {
	if len(params) > maxTrainerParameters {
		return errors.New("parameters: too many")
	}
	seen := map[string]bool{}
	for _, p := range params {
		if p == nil {
			return errors.New("parameters: an empty entry")
		}
		if err := p.validate(); err != nil {
			return fmt.Errorf("parameter %q: %w", p.Name, err)
		}
		if seen[p.Name] {
			return fmt.Errorf("parameter %q is declared twice", p.Name)
		}
		seen[p.Name] = true
	}
	return nil
}

// validEnvNames checks a command's environment allowlist: variable names,
// none of the server's own MDDB_ settings.
func validEnvNames(names []string) error {
	for _, name := range names {
		if !envNamePattern.MatchString(name) {
			return fmt.Errorf("env: %q is not a variable name", name)
		}
		if strings.HasPrefix(strings.ToUpper(name), "MDDB_") {
			return fmt.Errorf("env: %q is the server's own setting and is never passed to a command", name)
		}
	}
	return nil
}

// validExecutable refuses anything but an absolute path to an existing
// regular file, executable outside Windows: no lookup in PATH, no shell.
func validExecutable(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("command %q: an absolute, clean path to an executable", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("command: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("command %q is not a regular file", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("command %q is not executable", path)
	}
	return nil
}

// validDirectory refuses anything but an absolute path to a directory.
func validDirectory(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%q is not an absolute path", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", path)
	}
	return nil
}

// validTemplates checks argument templates: each a line of bounded length,
// naming known placeholders only; {value} only where withValue is set.
func validTemplates(args []string, withValue bool) error {
	for _, a := range args {
		if len(a) > maxTrainerArgLength || strings.ContainsAny(a, "\x00\r\n") {
			return fmt.Errorf("%q: one line of at most %d bytes", a, maxTrainerArgLength)
		}
		for _, ph := range placeholderPattern.FindAllString(a, -1) {
			if !knownPlaceholders[ph] && (ph != "{value}" || !withValue) {
				return fmt.Errorf("%q: unknown placeholder %s", a, ph)
			}
		}
	}
	return nil
}

// durationOr is raw as a duration above zero, or def when raw is empty.
func durationOr(raw string, def time.Duration) (time.Duration, error) {
	if raw == "" {
		return def, nil
	}
	return positiveDuration(raw)
}

// positiveDuration parses a duration that must be above zero.
func positiveDuration(raw string) (time.Duration, error) {
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, errors.New("must be above zero")
	}
	return d, nil
}
