//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// memoryStatusEx is MEMORYSTATUSEX; golang.org/x/sys/windows does not wrap
// GlobalMemoryStatusEx.
type memoryStatusEx struct {
	length, memoryLoad                   uint32
	totalPhys, availPhys                 uint64
	totalPageFile, availPageFile         uint64
	totalVirtual, availVirtual, availExt uint64
}

var procGlobalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

// availableMemory is the physical memory Windows reports as available.
func availableMemory() uint64 {
	m := memoryStatusEx{}
	m.length = uint32(unsafe.Sizeof(m))
	if r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); r == 0 {
		return 0
	}
	return m.availPhys
}
