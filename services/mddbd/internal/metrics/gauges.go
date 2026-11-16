package metrics

import (
	"sort"
	"strconv"
	"strings"
)

// Gauges the server sets as events happen, rather than ones read from the
// StatsProvider at scrape time — the time of the last successful scheduled
// backup (OPS-023) is the first — and counters it adds to as events happen
// (GO-054: per-tenant refusals and queue waits).

// gauge is one metric family: its help text and its series by label set.
type gauge struct {
	help   string
	series map[string]float64 // rendered labels (`rule="x"`) or "" -> value
}

// SetGauge sets the series of the gauge name with the given labels, already
// rendered (`rule="nightly"`, or "" for none), to value. A nil receiver sets
// nothing, like IncOp.
func (m *Metrics) SetGauge(name, help, labels string, value float64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gauges == nil {
		m.gauges = make(map[string]*gauge)
	}
	familyOf(m.gauges, name, help).series[labels] = value
}

// AddCounter adds delta to the series of the counter name with the given
// labels, rendered as for SetGauge. A nil receiver adds nothing.
func (m *Metrics) AddCounter(name, help, labels string, delta float64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counters == nil {
		m.counters = make(map[string]*gauge)
	}
	familyOf(m.counters, name, help).series[labels] += delta
}

// familyOf is the family name of set, created when missing.
func familyOf(set map[string]*gauge, name, help string) *gauge {
	g := set[name]
	if g == nil {
		g = &gauge{help: help, series: make(map[string]float64)}
		set[name] = g
	}
	return g
}

// writeGauges renders every gauge set so far, families and series sorted,
// then every counter added to so far (GO-054).
func (m *Metrics) writeGauges(buf *strings.Builder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	writeFamilies(buf, m.gauges, "gauge")
	writeFamilies(buf, m.counters, "counter")
}

// writeFamilies renders families of one type, families and series sorted.
func writeFamilies(buf *strings.Builder, set map[string]*gauge, typ string) {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		g := set[name]
		writef(buf, "# HELP %s %s\n", name, g.help)
		writef(buf, "# TYPE %s %s\n", name, typ)
		labels := make([]string, 0, len(g.series))
		for l := range g.series {
			labels = append(labels, l)
		}
		sort.Strings(labels)
		for _, l := range labels {
			if l == "" {
				writef(buf, "%s %s\n", name, strconv.FormatFloat(g.series[l], 'f', -1, 64))
			} else {
				writef(buf, "%s{%s} %s\n", name, l, strconv.FormatFloat(g.series[l], 'f', -1, 64))
			}
		}
		buf.WriteString("\n")
	}
}
