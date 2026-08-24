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
// right there and reads like the obvious call. If a future edit reaches for it,
// this is what should stop the edit.
//
// The first version of this test asserted the method goes negative, on the
// reasoning that subtracting the 1601 epoch from one second of CPU lands about
// 369 years before 1970. CI said otherwise. That value is about -1.16e19 and an
// int64 stops at -9.22e18, so the multiply by 100 wraps and the method returns
// a large positive number — which is worse than a negative one, because a
// negative CPU time is obviously broken and this looks like a reading.
func TestFiletimeNanosecondsMethodIsWrongForDurations(t *testing.T) {
	oneSecondOfCPU := windows.Filetime{LowDateTime: 10_000_000}

	const observed = 6802270474709551616 // measured on windows/amd64

	got := oneSecondOfCPU.Nanoseconds()
	if got == 1_000_000_000 {
		t.Fatal("Filetime.Nanoseconds now returns the duration correctly; if the " +
			"method has been fixed upstream, filetimeDurationNanos can go — but " +
			"check every Go version this project supports first")
	}
	if got != observed {
		t.Errorf("Filetime.Nanoseconds returned %d for a one-second duration, "+
			"expected the wrapped %d. The method is still wrong for a duration "+
			"either way; the arithmetic behind it has changed", got, observed)
	}
}
