package server

import (
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"
)

type NodeVitals struct {
	CPUPercent float64   `json:"cpuPercent"`
	MemPercent float64   `json:"memPercent"`
	ReadAt     time.Time `json:"readAt"`
}

type cpuSampler struct {
	mu    sync.Mutex
	total float64
	pct   float64
	at    time.Time
}

var cpuSample cpuSampler

// If elapsed time shrinks close time it takes for OS to report CPU time,
// dividing by tiny tiny elapsed denominator can spike CPU %.
// Realistically in this project, heartbeats will only be ~1-3s,
// so should not be an issue, but implemented as a safegaurd if functionality changes in future.
const minCPUsampleWindow = 150 * time.Millisecond

// getVitals returns CPU and Mem %.
// CPU is % during elapsed time frame, not culimative over process' life.
func getVitals() (NodeVitals, error) {
	p, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		return NodeVitals{}, err
	}

	times, err := p.Times()
	if err != nil {
		return NodeVitals{}, err
	}
	total := times.User + times.System
	cpuPct, readAt := cpuSample.sample(total)

	memInfo, err := p.MemoryInfo()
	if err != nil {
		return NodeVitals{}, err
	}
	memLimit, err := memLimitBytes()
	if err != nil {
		return NodeVitals{}, err
	}

	return NodeVitals{
		CPUPercent: cpuPct,
		MemPercent: 100 * float64(memInfo.RSS) / float64(memLimit),
		ReadAt:     readAt,
	}, nil
}

func (s *cpuSampler) sample(total float64) (cpuPct float64, readAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if !s.at.IsZero() {
		if elapsed := now.Sub(s.at); elapsed >= minCPUsampleWindow {
			s.pct = 100 * (total - s.total) / elapsed.Seconds() / cpuQuotaCores()
		}
	}
	s.total, s.at = total, now
	return s.pct, now
}

const cgroupMemMaxPath = "/sys/fs/cgroup/memory.max"

var memFallbackWarn sync.Once

func memLimitBytes() (uint64, error) {
	return memLimitBytesFrom(cgroupMemMaxPath)
}

// memLimitBytesFrom reads the cgroup v2 memory cap at path, falls back to host total when unset or unavailable.
func memLimitBytesFrom(path string) (uint64, error) {
	reason := ""
	if b, err := os.ReadFile(path); err != nil {
		reason = "cgroup file unreadable: " + err.Error()
	} else if v := strings.TrimSpace(string(b)); v == "max" {
		reason = "cgroup memory.max is unlimited"
	} else if n, err := strconv.ParseUint(v, 10, 64); err != nil {
		reason = "cgroup memory.max unparseable: " + v
	} else {
		return n, nil
	}

	memFallbackWarn.Do(func() {
		slog.Warn("no cgroup memory limit, falling back to host total for getVitals() call(s)", "reason", reason)
	})
	vm, err := mem.VirtualMemory()
	if err != nil {
		return 0, err
	}
	return vm.Total, nil
}

const cgroupCPUMaxPath = "/sys/fs/cgroup/cpu.max"

var cpuFallbackWarn sync.Once

func cpuQuotaCores() float64 {
	return cpuQuotaCoresFrom(cgroupCPUMaxPath)
}

// cpuQuotaCoresFrom reads the cgroup v2 CPU quota at path, falls back to host core count when unset or unavailable.
func cpuQuotaCoresFrom(path string) float64 {
	reason := ""
	if b, err := os.ReadFile(path); err != nil {
		reason = "cgroup file unreadable: " + err.Error()
	} else if fields := strings.Fields(string(b)); len(fields) != 2 || fields[0] == "max" {
		reason = "cgroup cpu.max is unlimited"
	} else if quota, errQ := strconv.ParseFloat(fields[0], 64); errQ != nil {
		reason = "cgroup cpu.max cpu time unparseable: " + string(b)
	} else if period, errP := strconv.ParseFloat(fields[1], 64); errP != nil || period <= 0 {
		reason = "cgroup cpu.max wall clock period unparseable: " + string(b)
	} else {
		return quota / period
	}

	cpuFallbackWarn.Do(func() {
		slog.Warn("no cgroup CPU quota, falling back to host core count for getVitals() call(s)", "reason", reason)
	})
	return float64(runtime.NumCPU())
}
