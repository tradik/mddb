package metrics

import (
	"strings"
	"testing"
)

// Event-set gauges: rendered once per family, series sorted, values in plain
// decimal (a Unix time must not come out as 1.79e+09).
func TestSetGaugeRendersFamiliesAndSeries(t *testing.T) {
	var none *Metrics
	none.SetGauge("x", "ignored", "", 1) // a nil collector sets nothing

	m := &Metrics{}
	m.SetGauge("mddb_b", "B help.", `rule="z"`, 1793336400)
	m.SetGauge("mddb_b", "B help.", `rule="a"`, 2)
	m.SetGauge("mddb_a", "A help.", "", 0.5)
	m.SetGauge("mddb_b", "B help.", `rule="a"`, 3) // replaces

	var buf strings.Builder
	m.writeGauges(&buf)
	want := "# HELP mddb_a A help.\n# TYPE mddb_a gauge\nmddb_a 0.5\n\n" +
		"# HELP mddb_b B help.\n# TYPE mddb_b gauge\nmddb_b{rule=\"a\"} 3\nmddb_b{rule=\"z\"} 1793336400\n\n"
	if buf.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}
