package server

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/JoshFarwig/kvstore/store"
	"github.com/shirou/gopsutil/v4/mem"
)

func writeCGroupMem(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memory.max")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write cgroup memory.max file failed: %v", err)
	}
	return path
}

func writeCGroupCPU(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cpu.max")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write cgroup memory.cpu file failed: %v", err)
	}
	return path
}

func TestMemBytesLimit(t *testing.T) {
	vm, err := mem.VirtualMemory()
	if err != nil {
		t.Fatalf("unable to retrieve virtual memory: %v", err)
	}

	tests := []struct {
		name string
		path string
		want uint64
	}{
		{"cgroup cat set", writeCGroupMem(t, "1073741824"), 1073741824},
		{"missing file", filepath.Join(t.TempDir(), "doe"), vm.Total},
		{"max", writeCGroupMem(t, "max"), vm.Total},
		{"unparseable", writeCGroupMem(t, "foobar"), vm.Total},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := availableMemBytesFrom(tt.path)
			if err != nil {
				t.Fatalf("availableMemBytesFrom() error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("availableMemBytesFrom() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCPUQuotaCores(t *testing.T) {
	hostCores := float64(runtime.NumCPU())

	tests := []struct {
		name string
		path string
		want float64
	}{
		{"missing file", filepath.Join(t.TempDir(), "doe"), hostCores},
		{"max quota", writeCGroupCPU(t, "max 100000"), hostCores},
		{"quota unparseable", writeCGroupCPU(t, "foo 100000"), hostCores},
		{"period unparseable", writeCGroupCPU(t, "200000 foo"), hostCores},
		{"period zero", writeCGroupCPU(t, "200000 0"), hostCores},
		{"wrong field count", writeCGroupCPU(t, "200000"), hostCores},
		{"valid whole", writeCGroupCPU(t, "200000 100000"), 2},
		{"valid half", writeCGroupCPU(t, "150000 100000"), 1.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := availableCoresFrom(tt.path)
			if got != tt.want {
				t.Fatalf("availableCoresFrom() = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestSampleCPUPerecent(t *testing.T) {
	var s cpuSampler
	const startingCPUTime = 1.0

	pct, at, _, _ := s.sample(startingCPUTime)
	if pct != 0 {
		t.Fatalf("first sample() pct = %f, want 0", pct)
	}
	if at.IsZero() {
		t.Fatal("first sample() readAt is zero")
	}

	// Instead of sleep(minCPUsampleWindow), s.at = now - minCPUsampleWindow,
	// leaves only function-call overhead as jitter.
	s.at = s.at.Add(-minCPUsampleWindow)
	const totalDelta = 0.7
	availCores, _ := availableCores()
	wantPct := 100 * totalDelta / time.Since(s.at).Seconds() / availCores
	pct, _, _, _ = s.sample(startingCPUTime + totalDelta)

	if pct <= 0 {
		t.Fatalf("second sample() = %f, want > 0", pct)
	}
	if relDiff := math.Abs(pct-wantPct) / wantPct; relDiff > 0.1 {
		t.Fatalf("second sample() = %f, want ~%f (%.0f%% off)", pct, wantPct, relDiff*100)
	}
}

func TestThrottleThresholdValidate(t *testing.T) {
	tests := []struct {
		name string
		tt   ThrottleThreshold
		ok   bool
	}{
		{"valid", ThrottleThreshold{80, 80}, true},
		{"cpu zero", ThrottleThreshold{0, 80}, false},
		{"cpu over 100", ThrottleThreshold{101, 80}, false},
		{"mem zero", ThrottleThreshold{80, 0}, false},
		{"mem over 100", ThrottleThreshold{80, 101}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.tt.Validate(); (err == nil) != tt.ok {
				t.Fatalf("Validate() err = %v, want ok = %v", err, tt.ok)
			}
		})
	}
}

func TestSetGetThrottleThreshold(t *testing.T) {
	s := store.NewStore()

	if got := GetThrottleThreshold(s, "n1"); got != (ThrottleThreshold{}) {
		t.Fatalf("GetThrottleThreshold() unset = %v, want zero value", got)
	}

	want := ThrottleThreshold{CPUPctCap: 75, MemPctCap: 90}
	if err := SetThrottleThreshold(s, "n1", want); err != nil {
		t.Fatalf("SetThrottleThreshold() error: %v", err)
	}
	if got := GetThrottleThreshold(s, "n1"); got != want {
		t.Fatalf("GetThrottleThreshold() = %v, want %v", got, want)
	}
}

func TestSetThrottleThresholdRejectsInvalid(t *testing.T) {
	s := store.NewStore()
	if err := SetThrottleThreshold(s, "n1", ThrottleThreshold{}); err == nil {
		t.Fatal("SetThrottleThreshold() zero value, want error")
	}
}

func TestToggleThrottle(t *testing.T) {
	s := store.NewStore()

	ToggleThrottle(s, "n1", NodeVitals{CPUPercent: 95})
	if _, ok := GetThrottledNodes(s)["n1"]; ok {
		t.Fatal("throttled with no threshold configured")
	}

	SetThrottleThreshold(s, "n1", ThrottleThreshold{CPUPctCap: 80, MemPctCap: 80})

	ToggleThrottle(s, "n1", NodeVitals{CPUPercent: 95})
	if _, ok := GetThrottledNodes(s)["n1"]; !ok {
		t.Fatal("over cap not throttled")
	}

	ToggleThrottle(s, "n1", NodeVitals{CPUPercent: 10})
	if _, ok := GetThrottledNodes(s)["n1"]; ok {
		t.Fatal("under cap still throttled")
	}
}

func TestSampleVitals(t *testing.T) {
	v, err := SampleVitals()
	if err != nil {
		t.Fatalf("SampleVitals() error: %v", err)
	}
	if v.CPUPercent < 0 {
		t.Errorf("SampleVitals() cpu = %f, want >= 0", v.CPUPercent)
	}
	if v.MemPercent <= 0 || v.MemPercent > 100 {
		t.Errorf("SampleVitals() mem = %f, want 0 < mem <= 100", v.MemPercent)
	}
	if v.ReadAt.IsZero() {
		t.Errorf("SampleVitals() readAt is zero")
	}
}
