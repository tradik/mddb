package main

import (
	"fmt"

	"mddb/proto"
)

// A scenario's repetition-loop guard (#413).
//
// mddb-chat watches an answer as it streams and stops it once most of what
// the model writes repeats what it already wrote — the loop small models
// behind Ollama fall into. Its defaults live in mddb-chat's [llm.loop_guard];
// a scenario overrides them field by field, so a code-heavy scenario can
// loosen the guard or turn it off. Every field left at its zero value is "not
// set" and keeps mddb-chat's setting.

// Units the guard counts in.
const (
	LoopGuardUnitChars = "chars"
	LoopGuardUnitWords = "words"
)

// Bounds. Generous, but a window of millions would only cost mddb-chat memory.
const (
	maxLoopGuardNgram     = 4096
	maxLoopGuardWindow    = 65536
	maxLoopGuardMinOutput = 1_000_000
)

// ChatLoopGuard is llm.loopGuard of a scenario. Enabled is a pointer because
// false is a meaningful override.
type ChatLoopGuard struct {
	Enabled   *bool   `json:"enabled,omitempty"`
	Ngram     int     `json:"ngram,omitempty"`
	Window    int     `json:"window,omitempty"`
	Ratio     float64 `json:"ratio,omitempty"`
	MinOutput int     `json:"minOutput,omitempty"`
	Unit      string  `json:"unit,omitempty"`
}

// validate refuses a guard that cannot work. nil is no override.
func (g *ChatLoopGuard) validate() error {
	if g == nil {
		return nil
	}
	for _, b := range []struct {
		field  string
		v, max int
	}{{"ngram", g.Ngram, maxLoopGuardNgram}, {"window", g.Window, maxLoopGuardWindow},
		{"minOutput", g.MinOutput, maxLoopGuardMinOutput}} {
		if b.v < 0 || b.v > b.max {
			return fmt.Errorf("llm.loopGuard.%s must be between 0 (not set) and %d", b.field, b.max)
		}
	}
	switch {
	case g.Ratio < 0 || g.Ratio > 1:
		return fmt.Errorf("llm.loopGuard.ratio must be between 0 (not set) and 1")
	case g.Unit != "" && g.Unit != LoopGuardUnitChars && g.Unit != LoopGuardUnitWords:
		return fmt.Errorf("llm.loopGuard.unit must be %s or %s", LoopGuardUnitChars, LoopGuardUnitWords)
	case g.Ngram > 0 && g.Window > 0 && g.Window < g.Ngram:
		return fmt.Errorf("llm.loopGuard.window (%d) may not be shorter than ngram (%d)", g.Window, g.Ngram)
	}
	return nil
}

// chatLoopGuardToProto is the guard on the wire; nil stays absent.
func chatLoopGuardToProto(g *ChatLoopGuard) *proto.ChatLoopGuardProto {
	if g == nil {
		return nil
	}
	return &proto.ChatLoopGuardProto{Enabled: g.Enabled, Ngram: safeInt32(g.Ngram), Window: safeInt32(g.Window),
		Ratio: g.Ratio, MinOutput: safeInt32(g.MinOutput), Unit: g.Unit}
}

// chatLoopGuardFromProto reads the guard a client sent; absent is nil.
func chatLoopGuardFromProto(p *proto.ChatLoopGuardProto) *ChatLoopGuard {
	if p == nil {
		return nil
	}
	return &ChatLoopGuard{Enabled: p.Enabled, Ngram: int(p.GetNgram()), Window: int(p.GetWindow()),
		Ratio: p.GetRatio(), MinOutput: int(p.GetMinOutput()), Unit: p.GetUnit()}
}
