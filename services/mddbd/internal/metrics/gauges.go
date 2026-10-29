package metrics

import (
	"sort"
	"strconv"
	"strings"
)

// Gauges the server sets as events happen, rather than ones read from the
// StatsProvider at scrape time — the time of the last successful scheduled
// backup (OPS-023) is the first.

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
	g := m.gauges[name]
	if g == nil {
		g = &gauge{help: help, series: make(map[string]float64)}
		m.gauges[name] = g
	}
	g.series[labels] = value
}

// writeGauges renders every gauge set so far, families and series sorted.
func (m *Metrics) writeGauges(buf *strings.Builder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.gauges))
	for name := range m.gauges {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		g := m.gauges[name]
		writef(buf, "# HELP %s %s\n", name, g.help)
		writef(buf, "# TYPE %s gauge\n", name)
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
