package main

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"mddb-bench/internal/benchkit"
)

// What the run was measured on. CPU model, kernel, memory and a process's
// peak resident size come from /proc; where there is none (not Linux) those
// fields stay empty or zero and nothing fails.

// procRoot is where the kernel's process and machine files live; a test
// points it at fixtures.
var procRoot = "/proc"

// environment describes this machine.
func environment() Environment {
	return Environment{
		OS: runtime.GOOS, Arch: runtime.GOARCH, Cores: runtime.NumCPU(), GoVersion: runtime.Version(),
		Kernel:   strings.TrimSpace(procFile("sys/kernel/osrelease")),
		CPU:      procField(procFile("cpuinfo"), "model name"),
		MemoryGB: math.Round(procKB(procFile("meminfo"), "MemTotal")/1024/1024*10) / 10,
	}
}

// peakRSSMB is the peak resident memory of process pid in MiB, 0 if unknown.
func peakRSSMB(pid int) float64 {
	return math.Round(procKB(procFile(strconv.Itoa(pid)+"/status"), "VmHWM")/1024*10) / 10
}

func procFile(name string) string {
	raw, _ := os.ReadFile(filepath.Join(procRoot, name)) // #nosec G304 -- a kernel file under /proc
	return string(raw)
}

// procField is the value of the first "key : value" line of text.
func procField(text, key string) string {
	for _, line := range strings.Split(text, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// procKB reads a "Key:  123 kB" line as kilobytes; 0 when absent.
func procKB(text, key string) float64 {
	kb, _ := strconv.ParseFloat(strings.TrimSuffix(procField(text, key), " kB"), 64)
	return kb
}

// calibrate times a fixed workload — dot products over a seeded set of
// vectors — and returns the fastest of five passes in milliseconds. It says
// how fast this machine is at the arithmetic a vector search is made of,
// with no server involved, so two result files from different machines can
// be told apart from two builds on one machine.
func calibrate() float64 {
	docs, centroids := benchkit.NewGen(1, 128).Clustered(20, 500)
	best := math.MaxFloat64
	var sink float32
	for pass := 0; pass < 5; pass++ {
		start := time.Now()
		for _, c := range centroids {
			for _, d := range docs {
				sink += benchkit.Dot(c, d)
			}
		}
		best = min(best, float64(time.Since(start).Microseconds())/1000)
	}
	_ = sink
	return best
}
