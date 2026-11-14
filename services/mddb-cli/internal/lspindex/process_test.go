package lspindex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPeakRssMb(t *testing.T) {
	dir := t.TempDir()
	for content, want := range map[string]int{
		"Name:\tgopls\nVmHWM:\t 4800512 kB\nVmRSS:\t1 kB\n": 4688,
		"VmHWM:\n":           0,
		"VmHWM:\tlots kB\n":  0,
		"Name:\tno memory\n": 0,
	} {
		p := filepath.Join(dir, "status")
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := peakRssMb(p); got != want {
			t.Errorf("%q: %d, want %d", content, got, want)
		}
	}
	if got := peakRssMb(filepath.Join(dir, "missing")); got != 0 {
		t.Errorf("missing file: %d", got)
	}
}

func TestRingBufferKeepsTail(t *testing.T) {
	r := &ringBuffer{limit: 4}
	for _, s := range []string{"ab", "cde", "fghij"} {
		if n, err := r.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatal(n, err)
		}
	}
	if r.String() != "ghij" {
		t.Errorf("ring = %q", r.String())
	}
}

func TestStartServerPipeFailure(t *testing.T) {
	old := osPipe
	defer func() { osPipe = old }()
	boom := errors.New("out of descriptors")
	for fail := 1; fail <= 2; fail++ {
		calls := 0
		osPipe = func() (*os.File, *os.File, error) {
			if calls++; calls == fail {
				return nil, nil, boom
			}
			return os.Pipe()
		}
		if _, err := startServer(context.Background(), t.TempDir(), []string{fakePath(t)}); !errors.Is(err, boom) {
			t.Errorf("pipe %d failing: err = %v", fail, err)
		}
	}
}

func TestServerErrorFormat(t *testing.T) {
	inner := errors.New("boom")
	e := &ServerError{Server: "gopls serve", Err: inner}
	if e.Error() != "gopls serve: boom" || !errors.Is(e, inner) {
		t.Errorf("plain: %q", e.Error())
	}
	e.Stderr = strings.Repeat("x", stderrInError) + "END\n"
	e.Messages = []string{"one", "two"}
	msg := e.Error()
	if !strings.Contains(msg, "server messages:\n  one\n  two") || !strings.Contains(msg, "server stderr:\n...") ||
		!strings.HasSuffix(msg, "END") || len(msg) > stderrInError+100 {
		t.Errorf("long: %q", msg)
	}
}

// TestResultDropsUnencodable covers what the fake cannot easily produce:
// a reference or implementation whose value would pass the size limit.
func TestResultDropsUnencodable(t *testing.T) {
	long := strings.Repeat("n", maxValueBytes)
	r := newRun(ServerSpec{}, Options{}, "/root", nil)
	r.server = "s/1"
	r.defs["a.go"] = []symbol{{path: "a.go", def: Def{Name: "A", Kind: "struct", Start: 1, End: 1}}}
	r.defs["empty.go"] = nil
	r.refs["a.go"] = map[string]map[int]bool{"a.go#" + long: {1: true}, "a.go#A": {3: true, 2: true}}
	r.impls["a.go"] = map[Impl]bool{
		{Target: "b.go#I", Local: "A"}: true, {Target: "a.go#I", Local: "A"}: true,
		{Target: "a.go#I", Local: "B"}: true, {Target: "a.go#I", Local: long}: true,
	}
	res := r.result()
	want := &FileFacts{
		Defs:  []Def{{Name: "A", Kind: "struct", Start: 1, End: 1}},
		Refs:  []Ref{{Target: "a.go#A", Lines: []int{2, 3}}},
		Impls: []Impl{{Target: "a.go#I", Local: "A"}, {Target: "a.go#I", Local: "B"}, {Target: "b.go#I", Local: "A"}},
	}
	if !reflect.DeepEqual(res.Files["a.go"], want) || res.Stats.Unencodable != 2 || res.Stats.Files != 2 {
		t.Errorf("a.go = %+v, stats %+v", *res.Files["a.go"], res.Stats)
	}
	if !reflect.DeepEqual(res.Files["empty.go"], &FileFacts{}) {
		t.Errorf("empty.go = %+v", res.Files["empty.go"])
	}
}
