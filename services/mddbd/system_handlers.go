package main

import (
	"net"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	json "mddb/internal/jsonx"
)

// Global variable to track server start time
var serverStartTime = time.Now()

// ---- CPU Sampler ----

type cpuSamplerState struct {
	mu           sync.Mutex
	lastTime     time.Time
	lastUserNs   int64
	lastSystemNs int64
	cpuPercent   float64
}

var cpuSampler = &cpuSamplerState{
	lastTime: time.Now(),
}

func init() {
	// Take initial sample
	if userNs, systemNs, err := processCPUTime(); err == nil {
		cpuSampler.lastUserNs = userNs
		cpuSampler.lastSystemNs = systemNs
	}
}

func (c *cpuSamplerState) sample() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	userNs, systemNs, err := processCPUTime()
	if err != nil {
		return c.cpuPercent
	}

	elapsed := now.Sub(c.lastTime).Nanoseconds()
	if elapsed <= 0 {
		return c.cpuPercent
	}

	deltaCPU := float64((userNs - c.lastUserNs) + (systemNs - c.lastSystemNs))
	numCPU := float64(runtime.NumCPU())
	c.cpuPercent = (deltaCPU / float64(elapsed) / numCPU) * 100

	if c.cpuPercent < 0 {
		c.cpuPercent = 0
	}
	if c.cpuPercent > 100 {
		c.cpuPercent = 100
	}

	c.lastTime = now
	c.lastUserNs = userNs
	c.lastSystemNs = systemNs

	return c.cpuPercent
}

// ---- Request/Response types ----

// NetworkInterface represents a non-loopback network interface with its addresses.
type NetworkInterface struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
	Flags     string   `json:"flags"`
}

// SystemInfoResponse contains system-level information such as OS, CPU, memory, and uptime.
type SystemInfoResponse struct {
	Hostname        string             `json:"hostname"`
	OS              string             `json:"os"`
	Arch            string             `json:"arch"`
	NumCPU          int                `json:"numCPU"`
	GoVersion       string             `json:"goVersion"`
	Version         string             `json:"version"`
	UptimeSeconds   int64              `json:"uptimeSeconds"`
	MemoryTotal     uint64             `json:"memoryTotal"`
	MemoryUsed      uint64             `json:"memoryUsed"`
	MemorySystem    uint64             `json:"memorySystem"`
	MemoryHeap      uint64             `json:"memoryHeap"`
	NumGoroutines   int                `json:"numGoroutines"`
	CPUUsagePercent float64            `json:"cpuUsagePercent"`
	Network         []NetworkInterface `json:"network"`
}

// ---- Handlers ----

// handleSystemInfo returns system information
func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get hostname
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	// Get memory stats
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// Calculate uptime
	uptime := int64(time.Since(serverStartTime).Seconds())

	// Sample CPU usage
	cpuPercent := cpuSampler.sample()

	// Gather network interfaces
	var netIfaces []NetworkInterface
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil || len(addrs) == 0 {
				continue
			}
			ni := NetworkInterface{
				Name:  iface.Name,
				Flags: iface.Flags.String(),
			}
			for _, addr := range addrs {
				ni.Addresses = append(ni.Addresses, addr.String())
			}
			netIfaces = append(netIfaces, ni)
		}
	}

	response := SystemInfoResponse{
		Hostname:        hostname,
		OS:              runtime.GOOS,
		Arch:            runtime.GOARCH,
		NumCPU:          runtime.NumCPU(),
		GoVersion:       runtime.Version(),
		Version:         VERSION,
		UptimeSeconds:   uptime,
		MemoryTotal:     memStats.TotalAlloc,
		MemoryUsed:      memStats.Alloc,
		MemorySystem:    memStats.Sys,
		MemoryHeap:      memStats.HeapInuse,
		NumGoroutines:   runtime.NumGoroutine(),
		CPUUsagePercent: cpuPercent,
		Network:         netIfaces,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}
