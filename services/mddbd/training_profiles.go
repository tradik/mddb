package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Trainer profiles (model training module). MDDB trains nothing: it starts a
// command the server's operator named in a file, MDDB_TRAINERS_CONFIG, read
// once at startup like MDDB_MCP_CONFIG. Nothing a request carries can name a
// command, an argument template, a path or an environment variable; a request
// picks a profile by name and gives values for the parameters the profile
// declares, which training_params.go checks against their declared types.

// trainersConfigEnv names the operator's profile file (YAML or JSON).
const trainersConfigEnv = "MDDB_TRAINERS_CONFIG"

// Defaults of a profile's optional fields.
const (
	defaultTrainTimeout   = 24 * time.Hour
	defaultEvalTimeout    = time.Hour
	defaultTrialRecords   = 100
	defaultTrainOutput    = "*.gguf"
	maxTrainerParameters  = 32
	maxTrainerArgs        = 64
	maxTrainerArgLength   = 4096
	maxTrainerDescription = 1000
)

// trainerFile is the structure of MDDB_TRAINERS_CONFIG.
type trainerFile struct {
	Trainers []*trainerProfile `yaml:"trainers"`
}

// commandSpec is a command the operator configured: the trainer itself, or
// its evaluate block. Both are validated and started the same way.
type commandSpec struct {
	Command    string          `yaml:"command" json:"-"`
	Args       []string        `yaml:"args" json:"-"`
	Parameters []*trainerParam `yaml:"parameters" json:"parameters"`
	Timeout    string          `yaml:"timeout" json:"timeout"`
	Env        []string        `yaml:"env" json:"-"`
	WorkDir    string          `yaml:"workDir" json:"-"`

	timeout time.Duration
}

// trainerProfile is one trainer the operator configured.
type trainerProfile struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description,omitempty"`
	commandSpec `yaml:",inline"`
	// Output is the file name pattern of the result in the output directory.
	Output        string `yaml:"output" json:"output"`
	MaxConcurrent int    `yaml:"maxConcurrent" json:"maxConcurrent"`
	// Progress is the prefix of a progress line the command prints:
	// "<prefix> <percent> [message]".
	Progress        string         `yaml:"progress" json:"progress,omitempty"`
	TrialRecords    int            `yaml:"trialRecords" json:"trialRecords"`
	TrialParameters map[string]any `yaml:"trialParameters" json:"trialParameters,omitempty"`
	TrialTimeout    string         `yaml:"trialTimeout" json:"trialTimeout,omitempty"`
	Evaluate        *evaluateSpec  `yaml:"evaluate" json:"evaluate,omitempty"`

	trialTimeout time.Duration
}

// evaluateSpec is a profile's optional evaluation command. Baseline says it
// can score the base model too, with MDDB_EVAL_MODEL empty.
type evaluateSpec struct {
	commandSpec `yaml:",inline"`
	Baseline    bool `yaml:"baseline" json:"baseline"`
}

var (
	trainerNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	envNamePattern     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	placeholderPattern = regexp.MustCompile(`\{[^{}]*\}`)
)

// knownPlaceholders are what an argument template may name; {value} only in
// a parameter's own arguments.
var knownPlaceholders = map[string]bool{
	"{dataset}": true, "{validation}": true, "{outputDir}": true, "{jobId}": true, "{jobDir}": true,
	"{evalDataset}": true, "{evalModel}": true, "{evalOutput}": true,
}

// defaultTrainerEnv is the environment passed through when a command lists
// none: what most programs need to start, and nothing of the server's own.
var defaultTrainerEnv = []string{"PATH", "HOME", "LANG", "LC_ALL", "TZ", "TMPDIR", "SYSTEMROOT", "TEMP", "TMP"}

// trainerProfiles is what the file held: the valid profiles, and one message
// per refused profile or file error for the panel to show the operator.
type trainerProfiles struct {
	list   []*trainerProfile
	errors []string
}

// byName finds a profile.
func (p *trainerProfiles) byName(name string) (*trainerProfile, bool) {
	for _, t := range p.list {
		if t.Name == name {
			return t, true
		}
	}
	return nil, false
}

// loadTrainerProfiles reads MDDB_TRAINERS_CONFIG. No variable, no profiles:
// the module still exports datasets. A profile that fails validation is
// left out and reported; the others load.
func loadTrainerProfiles() *trainerProfiles {
	out := &trainerProfiles{}
	path := os.Getenv(trainersConfigEnv)
	if path == "" {
		return out
	}
	// #nosec G304 G703 -- the operator's configuration path from the environment, never request input
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		out.errors = append(out.errors, fmt.Sprintf("%s: %v", trainersConfigEnv, err))
		return out
	}
	var f trainerFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // a misspelt key is an error, not a silently ignored setting
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		out.errors = append(out.errors, fmt.Sprintf("%s: %v", trainersConfigEnv, err))
		return out
	}
	seen := map[string]bool{}
	for i, t := range f.Trainers {
		if t == nil {
			continue
		}
		err := t.validate()
		if err == nil && seen[t.Name] {
			err = errors.New("the name is used by an earlier profile")
		}
		if err != nil {
			out.errors = append(out.errors, fmt.Sprintf("trainer %d (%q): %v", i+1, t.Name, err))
			continue
		}
		seen[t.Name] = true
		out.list = append(out.list, t)
	}
	return out
}

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
	if t.TrialRecords <= 0 {
		t.TrialRecords = defaultTrialRecords
	}
	if strings.ContainsAny(t.Progress, "\r\n") {
		return errors.New("progress: a prefix on one line")
	}
	t.trialTimeout = t.timeout
	if t.TrialTimeout != "" {
		d, err := positiveDuration(t.TrialTimeout)
		if err != nil {
			return fmt.Errorf("trialTimeout: %w", err)
		}
		t.trialTimeout = d
	}
	t.TrialTimeout = t.trialTimeout.String()
	for name, v := range t.TrialParameters {
		p := findParam(t.Parameters, name)
		if p == nil {
			return fmt.Errorf("trialParameters: %q is not a parameter of the profile", name)
		}
		if _, err := p.check(v); err != nil {
			return fmt.Errorf("trialParameters: %w", err)
		}
	}
	if t.Evaluate != nil {
		if err := t.Evaluate.validate(defaultEvalTimeout); err != nil {
			return fmt.Errorf("evaluate: %w", err)
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
	if len(c.Parameters) > maxTrainerParameters {
		return errors.New("parameters: too many")
	}
	seen := map[string]bool{}
	for _, p := range c.Parameters {
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
	for _, name := range c.Env {
		if !envNamePattern.MatchString(name) {
			return fmt.Errorf("env: %q is not a variable name", name)
		}
		if strings.HasPrefix(strings.ToUpper(name), "MDDB_") {
			return fmt.Errorf("env: %q is the server's own setting and is never passed to a command", name)
		}
	}
	if c.WorkDir != "" {
		if err := validDirectory(c.WorkDir); err != nil {
			return fmt.Errorf("workDir: %w", err)
		}
	}
	c.timeout = defaultTimeout
	if c.Timeout != "" {
		d, err := positiveDuration(c.Timeout)
		if err != nil {
			return fmt.Errorf("timeout: %w", err)
		}
		c.timeout = d
	}
	c.Timeout = c.timeout.String() // the profile list shows what applies
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

// trainerEnvNames is the allowlist of a command: its own, or the default.
func (c *commandSpec) trainerEnvNames() []string {
	if len(c.Env) > 0 {
		return c.Env
	}
	return defaultTrainerEnv
}
