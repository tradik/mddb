//go:build windows

package main

import (
	"testing"

	"golang.org/x/sys/windows"
)

// The conversion this file guards is the one place the Windows port can be
// silently wrong rather than loudly broken: a FILETIME holding a duration and a
// FILETIME holding a timestamp are the same 64-bit count of 100-nanosecond
// intervals, and only the caller knows which it has.
func TestFiletimeDurationNanos(t *testing.T) {
	cases := []struct {
		name string
		ft   windows.Filetime
		want int64
	}{
		{"zero", windows.Filetime{}, 0},
		{"one 100ns tick", windows.Filetime{LowDateTime: 1}, 100},
		{"one second", windows.Filetime{LowDateTime: 10_000_000}, 1_000_000_000},
		{"high word carries", windows.Filetime{HighDateTime: 1}, int64(1) << 32 * 100},
		{
			"both words",
			windows.Filetime{LowDateTime: 10_000_000, HighDateTime: 2},
			(int64(2)<<32 + 10_000_000) * 100,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filetimeDurationNanos(tc.ft); got != tc.want {
				t.Fatalf("filetimeDurationNanos = %d, want %d", got, tc.want)
			}
		})
	}
}

// The trap, asserted rather than described: windows.Filetime.Nanoseconds is
// right there, reads like the obvious call, and returns a number roughly 369
// years in the past for any realistic amount of CPU time. If a future edit
// reaches for it, this is what should stop the edit.
func TestFiletimeNanosecondsMethodIsWrongForDurations(t *testing.T) {
	oneSecondOfCPU := windows.Filetime{LowDateTime: 10_000_000}

	if got := oneSecondOfCPU.Nanoseconds(); got >= 0 {
		t.Fatalf("Filetime.Nanoseconds returned %d for a one-second duration; "+
			"it was expected to subtract the 1601 epoch and go negative, which is "+
			"the whole reason filetimeDurationNanos exists", got)
	}
}
