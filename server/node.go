package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JoshFarwig/kvstore/store"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"
)

type NodeVitals struct {
	CPUPercent        float64   `json:"cpuPercent"`
	MemPercent        float64   `json:"memPercent"`
	AvailableCores    float64   `json:"availableCores"`
	AvailableMemBytes uint64    `json:"availableMemBytes"`
	CPUFromCgroup     bool      `json:"cpuFromCgroup"`
	MemFromCgroup     bool      `json:"memFromCgroup"`
	ReadAt            time.Time `json:"readAt"`
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

// SampleVitals returns CPU and Mem %.
// CPU is % during elapsed time frame, not culimative over process' life.
func SampleVitals() (NodeVitals, error) {
	p, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		return NodeVitals{}, err
	}

	times, err := p.Times()
	if err != nil {
		return NodeVitals{}, err
	}
	total := times.User + times.System
	cpuPct, readAt, availCores, cpuFromCgroup := cpuSample.sample(total)

	memInfo, err := p.MemoryInfo()
	if err != nil {
		return NodeVitals{}, err
	}
	availMemBytes, memFromCgroup, err := availableMemBytes()
	if err != nil {
		return NodeVitals{}, err
	}

	return NodeVitals{
		CPUPercent:        cpuPct,
		MemPercent:        100 * float64(memInfo.RSS) / float64(availMemBytes),
		AvailableCores:    availCores,
		AvailableMemBytes: availMemBytes,
		CPUFromCgroup:     cpuFromCgroup,
		MemFromCgroup:     memFromCgroup,
		ReadAt:            readAt,
	}, nil
}

func (s *cpuSampler) sample(total float64) (cpuPct float64, readAt time.Time, availCores float64, fromCgroup bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	availCores, fromCgroup = availableCores()
	now := time.Now().UTC()
	if !s.at.IsZero() {
		if elapsed := now.Sub(s.at); elapsed >= minCPUsampleWindow {
			s.pct = 100 * (total - s.total) / elapsed.Seconds() / availCores
		}
	}
	s.total, s.at = total, now
	return s.pct, now, availCores, fromCgroup
}

const cgroupMemMaxPath = "/sys/fs/cgroup/memory.max"

var memFallbackWarn sync.Once

func availableMemBytes() (uint64, bool, error) {
	return availableMemBytesFrom(cgroupMemMaxPath)
}

// availableMemBytesFrom reads the cgroup v2 memory cap at path, falls back to host total when unset or unavailable.
func availableMemBytesFrom(path string) (uint64, bool, error) {
	reason := ""
	if b, err := os.ReadFile(path); err != nil {
		reason = "cgroup file unreadable: " + err.Error()
	} else if v := strings.TrimSpace(string(b)); v == "max" {
		reason = "cgroup memory.max is unlimited"
	} else if n, err := strconv.ParseUint(v, 10, 64); err != nil {
		reason = "cgroup memory.max unparseable: " + v
	} else {
		return n, true, nil
	}

	memFallbackWarn.Do(func() {
		slog.Warn("no cgroup memory limit, falling back to host total for SampleVitals() call(s)", "reason", reason)
	})
	vm, err := mem.VirtualMemory()
	if err != nil {
		return 0, false, err
	}
	return vm.Total, false, nil
}

const cgroupCPUMaxPath = "/sys/fs/cgroup/cpu.max"

var cpuFallbackWarn sync.Once

func availableCores() (float64, bool) {
	return availableCoresFrom(cgroupCPUMaxPath)
}

// availableCoresFrom reads the cgroup v2 CPU quota at path, falls back to host core count when unset or unavailable.
func availableCoresFrom(path string) (float64, bool) {
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
		return quota / period, true
	}

	cpuFallbackWarn.Do(func() {
		slog.Warn("no cgroup CPU quota, falling back to host core count for SampleVitals() call(s)", "reason", reason)
	})
	return float64(runtime.NumCPU()), false
}

const (
	heartbeatTTLMultiplier = 2
	vitalsKey              = "kvs:vitals:"
)

func StartHeartbeat(ctx context.Context, s *store.Store, nodeID string, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				slog.Info("heartbeat shutting down", "nodeID", nodeID, "shutdownAt", time.Now().UTC())
				return
			case <-ticker.C:
				v, err := SampleVitals()
				if err != nil {
					slog.Warn("hearbeat: SampleVitals() failed", "nodeID", nodeID, "err", err)
					continue
				}

				body, _ := json.Marshal(v)
				s.Set(vitalsKey+nodeID, body, time.Now().UTC().Add(heartbeatTTLMultiplier*interval))
				ToggleThrottle(s, nodeID, v)
			}
		}
	}()
}

const (
	throttleGlobalKey    = "kvs:throttle:GLOBAL"
	throttleThresholdKey = "kvs:throttle:threshold:"
)

type ThrottledNodes map[string]time.Time

type ThrottleThreshold struct {
	CPUPctCap float64 `json:"CPUPercentCap"`
	MemPctCap float64 `json:"memPercentCap"`
}

func (t ThrottleThreshold) Validate() error {
	var errs []error
	if t.CPUPctCap <= 0 || t.CPUPctCap > 100 {
		errs = append(errs, fmt.Errorf("CPUPercentCap must be in (0, 100], got %v", t.CPUPctCap))
	}
	if t.MemPctCap <= 0 || t.MemPctCap > 100 {
		errs = append(errs, fmt.Errorf("MemPercentCap must be in (0, 100], got %v", t.MemPctCap))
	}
	return errors.Join(errs...)
}

func GetThrottleThreshold(s *store.Store, nodeID string) ThrottleThreshold {
	item, err := s.Get(throttleThresholdKey + nodeID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			slog.Warn("could not retrieve throttle threshold for key", "key", nodeID, "err", err)
		}
		return ThrottleThreshold{}
	}

	var tt ThrottleThreshold
	if err := json.Unmarshal(item.Value, &tt); err != nil {
		slog.Warn("could not unmarshal throttle threshold", "key", nodeID, "err", err)
	}
	return tt
}

func GetThrottledNodes(s *store.Store) ThrottledNodes {
	item, err := s.Get(throttleGlobalKey)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			slog.Warn("could not retrieve throttled nodes", "key", throttleGlobalKey, "err", err)
		}
		return ThrottledNodes{}
	}

	tn := ThrottledNodes{}
	if err := json.Unmarshal(item.Value, &tn); err != nil {
		slog.Warn("could not unmarshal throttled nodes", "key", throttleGlobalKey, "err", err)
		return ThrottledNodes{}
	}
	return tn
}

func SetThrottleThreshold(s *store.Store, nodeID string, tt ThrottleThreshold) error {
	if err := tt.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(tt)
	if err != nil {
		return err
	}
	s.Set(throttleThresholdKey+nodeID, body, time.Time{})
	return nil
}

func ToggleThrottle(s *store.Store, nodeID string, vitals NodeVitals) {
	tt := GetThrottleThreshold(s, nodeID)
	tn := GetThrottledNodes(s)

	// zero-value threshold means none configured for this node, never throttle it.
	isAtCap := tt != (ThrottleThreshold{}) &&
		(vitals.CPUPercent >= tt.CPUPctCap || vitals.MemPercent >= tt.MemPctCap)

	if isAtCap {
		tn[nodeID] = time.Now().UTC()
	} else {
		delete(tn, nodeID)
	}

	body, err := json.Marshal(tn)
	if err != nil {
		slog.Warn("could not marshal throttled nodes", "err", err)
		return
	}
	s.Set(throttleGlobalKey, body, time.Time{})
}
