package main

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Parameters of a trainer profile. A request gives values, never arguments:
// each value is checked against the type the operator declared and becomes
// its own argv element through the parameter's argument template, so no
// value is ever read by a shell or split into several arguments.

// Parameter types.
const (
	paramString = "string"
	paramInt    = "int"
	paramFloat  = "float"
	paramBool   = "bool"
	paramEnum   = "enum"
)

// Limits on string values.
const (
	defaultParamMaxLength = 256
	maxParamMaxLength     = 4096
)

var (
	paramNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	// safeValuePattern is the conservative set a string or enum value is held
	// to unless the profile gives its own pattern: no spaces, quotes, shell
	// characters or path separators, and never a leading '-', which a
	// trainer's argument parser would read as an option.
	safeValuePattern = regexp.MustCompile(`^[A-Za-z0-9._,:+=@][A-Za-z0-9._,:+=@-]*$`)
)

// trainerParam is one parameter a profile declares.
type trainerParam struct {
	Name        string   `yaml:"name" json:"name"`
	Type        string   `yaml:"type" json:"type"`
	Description string   `yaml:"description" json:"description,omitempty"`
	Min         *float64 `yaml:"min" json:"min,omitempty"`
	Max         *float64 `yaml:"max" json:"max,omitempty"`
	Values      []string `yaml:"values" json:"values,omitempty"`
	Default     any      `yaml:"default" json:"default,omitempty"`
	Required    bool     `yaml:"required" json:"required,omitempty"`
	MaxLength   int      `yaml:"maxLength" json:"maxLength,omitempty"`
	Pattern     string   `yaml:"pattern" json:"pattern,omitempty"`
	// Args is the argument template, e.g. ["--epochs", "{value}"]. A bool
	// adds it when true and nothing when false.
	Args []string `yaml:"args" json:"-"`

	pattern *regexp.Regexp
}

// validate checks a declaration and its default.
func (p *trainerParam) validate() error {
	if !paramNamePattern.MatchString(p.Name) {
		return errors.New("name: a letter, then up to 63 letters, digits or '_'")
	}
	if err := p.validateType(); err != nil {
		return err
	}
	if err := p.validateLimits(); err != nil {
		return err
	}
	if err := p.compilePattern(); err != nil {
		return err
	}
	return p.validateUse()
}

// validateType checks the type, and an enum's values.
func (p *trainerParam) validateType() error {
	switch p.Type {
	case paramString, paramInt, paramFloat, paramBool:
		return nil
	case paramEnum:
		return validEnumValues(p.Values)
	default:
		return fmt.Errorf("type %q: string, int, float, bool or enum", p.Type)
	}
}

// validEnumValues checks an enum's values against the conservative set.
func validEnumValues(values []string) error {
	if len(values) == 0 {
		return errors.New("an enum needs values")
	}
	for _, v := range values {
		if !safeValuePattern.MatchString(v) || len(v) > defaultParamMaxLength {
			return fmt.Errorf("enum value %q: letters, digits and . _ , : + = @ -, not starting with '-'", v)
		}
	}
	return nil
}

// validateLimits checks min against max and maxLength, giving a string the
// default length.
func (p *trainerParam) validateLimits() error {
	if p.Min != nil && p.Max != nil && *p.Min > *p.Max {
		return errors.New("min is above max")
	}
	if p.MaxLength < 0 || p.MaxLength > maxParamMaxLength {
		return fmt.Errorf("maxLength: 0 to %d", maxParamMaxLength)
	}
	if p.MaxLength == 0 && p.Type == paramString {
		p.MaxLength = defaultParamMaxLength
	}
	return nil
}

// compilePattern sets the pattern a value is held to: the profile's, or the
// conservative set.
func (p *trainerParam) compilePattern() error {
	p.pattern = safeValuePattern
	if p.Pattern == "" {
		return nil
	}
	re, err := regexp.Compile(p.Pattern)
	if err != nil {
		return fmt.Errorf("pattern: %w", err)
	}
	p.pattern = re
	return nil
}

// validateUse checks the argument template and the default.
func (p *trainerParam) validateUse() error {
	if len(p.Args) == 0 {
		return errors.New("args: the argument template the value goes into, e.g. [\"--" + p.Name + "\", \"{value}\"]")
	}
	if err := validTemplates(p.Args, true); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if p.Default != nil {
		if _, err := p.check(p.Default); err != nil {
			return fmt.Errorf("default: %w", err)
		}
	}
	return nil
}

// check validates one value and returns it as the text it is passed as.
func (p *trainerParam) check(v any) (string, error) {
	switch p.Type {
	case paramBool:
		b, ok := v.(bool)
		if !ok {
			return "", fmt.Errorf("%s: true or false", p.Name)
		}
		return strconv.FormatBool(b), nil
	case paramInt, paramFloat:
		return p.checkNumber(v)
	case paramEnum:
		s, ok := v.(string)
		if !ok || !slices.Contains(p.Values, s) {
			return "", fmt.Errorf("%s: one of %s", p.Name, strings.Join(p.Values, ", "))
		}
		return s, nil
	default:
		return p.checkString(v)
	}
}

// checkNumber validates an int or a float against min and max.
func (p *trainerParam) checkNumber(v any) (string, error) {
	f, ok := numberValue(v)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return "", fmt.Errorf("%s: a number", p.Name)
	}
	if p.Type == paramInt && (f != math.Trunc(f) || math.Abs(f) > 1<<53) {
		return "", fmt.Errorf("%s: a whole number", p.Name)
	}
	if (p.Min != nil && f < *p.Min) || (p.Max != nil && f > *p.Max) {
		return "", fmt.Errorf("%s: %s", p.Name, p.rangeText())
	}
	if p.Type == paramInt {
		return strconv.FormatInt(int64(f), 10), nil
	}
	return strconv.FormatFloat(f, 'g', -1, 64), nil
}

// rangeText says what range a number must be in.
func (p *trainerParam) rangeText() string {
	switch {
	case p.Min != nil && p.Max != nil:
		return fmt.Sprintf("from %g to %g", *p.Min, *p.Max)
	case p.Min != nil:
		return fmt.Sprintf("at least %g", *p.Min)
	default:
		return fmt.Sprintf("at most %g", *p.Max)
	}
}

// checkString validates a string: one line, no NUL, bounded, and in the
// profile's pattern or the conservative set.
func (p *trainerParam) checkString(v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: a string", p.Name)
	}
	if strings.ContainsAny(s, "\x00\r\n") {
		return "", fmt.Errorf("%s: no NUL or line breaks", p.Name)
	}
	if len(s) > p.MaxLength {
		return "", fmt.Errorf("%s: at most %d bytes", p.Name, p.MaxLength)
	}
	if !p.pattern.MatchString(s) {
		return "", fmt.Errorf("%s: %q is not allowed by the profile", p.Name, s)
	}
	return s, nil
}

// numberValue reads a JSON or YAML number.
func numberValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
