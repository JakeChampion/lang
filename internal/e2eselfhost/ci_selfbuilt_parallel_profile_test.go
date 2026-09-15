package e2eselfhost

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestCISelfBuiltParallelProfile(t *testing.T) {
	root, result := os.Getenv("CI_BATCH_PROFILE_ROOT"), os.Getenv("CI_BATCH_PROFILE_RESULT")
	if root == "" {
		t.Skip("explicit scheduling experiment")
	}
	if !filepath.IsAbs(root) || !filepath.IsAbs(result) || runtime.GOOS != "linux" {
		t.Fatal("absolute paths and native Linux required")
	}
	target := "arm64-linux"
	if runtime.GOARCH == "amd64" {
		target = "x86-64-linux"
	} else if runtime.GOARCH != "arm64" {
		t.Fatal("unsupported native architecture")
	}
	mode, scale := os.Getenv("CI_BATCH_PROFILE_MODE"), os.Getenv("CI_BATCH_PROFILE_SCALE")
	weight := pmSelfBuiltEmitMemoryMB
	if mode == "parallel" {
		weight = pmGoBuiltEmitMemoryMB
	} else if mode != "serial" {
		t.Fatal("invalid mode")
	}
	data, err := os.ReadFile(filepath.Join(root, "unit-names.json"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil || len(names) < 32 {
		t.Fatalf("manifest: %v", err)
	}
	lo, hi := 16, 32
	if scale == "full" {
		lo, hi = 0, len(names)
	} else if scale != "pilot" {
		t.Fatal("invalid scale")
	}
	if err := os.MkdirAll(result, 0o755); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(result, "units")
	if err := os.Mkdir(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	type sample struct {
		Lo  int     `json:"lo"`
		Hi  int     `json:"hi"`
		CPU float64 `json:"cpu_seconds"`
		RSS int64   `json:"max_child_rss_bytes"`
	}
	var mu sync.Mutex
	active, peakActive := 0, 0
	var samples []sample
	start := time.Now()
	batches := runPMEmitBatches(hi-lo, pmEmitAllBatch, weight, func(begin, end int) (int64, error) {
		mu.Lock()
		active++
		peakActive = max(peakActive, active)
		mu.Unlock()
		defer func() { mu.Lock(); active--; mu.Unlock() }()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, filepath.Join(root, "gen1"), filepath.Join(root, "sources", "asm_modload_run.fern"),
			"-target", target, "-per-module-emit-all", "-assume-eligible", "-func-budget", strconv.Itoa(pmFuncBudget),
			"-unit-range", fmt.Sprintf("%d:%d", begin+lo, end+lo), "-out-dir", outDir)
		out, err := cmd.CombinedOutput()
		row := sample{Lo: begin + lo, Hi: end + lo}
		if cmd.ProcessState != nil {
			row.RSS = cmd.ProcessState.SysUsage().(*syscall.Rusage).Maxrss * 1024
			row.CPU = (cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()).Seconds()
		}
		mu.Lock()
		samples = append(samples, row)
		mu.Unlock()
		if err != nil {
			return row.RSS / 1024, fmt.Errorf("%w: %s", err, out)
		}
		return row.RSS / 1024, nil
	})
	wall := time.Since(start).Seconds()
	hashes := make(map[string]string)
	for _, b := range batches {
		if b.err != nil {
			t.Fatal(b.err)
		}
	}
	entries, err := os.ReadDir(outDir)
	if err != nil || len(entries) != hi-lo {
		t.Fatalf("unit count %d, %v", len(entries), err)
	}
	var cpu float64
	var peakChild int64
	for _, row := range samples {
		cpu += row.CPU
		peakChild = max(peakChild, row.RSS)
	}
	for _, name := range names[lo:hi] {
		actual, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			t.Fatal(err)
		}
		expected, err := os.ReadFile(filepath.Join(root, "sources", "gen1_out", name))
		if err != nil {
			t.Fatal(err)
		}
		a, e := sha256.Sum256(actual), sha256.Sum256(expected)
		if a != e {
			t.Fatalf("changed assembly: %s", name)
		}
		hashes[name] = fmt.Sprintf("%x", a)
	}
	wantPeak := 1
	if mode == "parallel" {
		wantPeak = 2
	}
	if peakActive != wantPeak {
		t.Fatalf("active children: %d, want %d", peakActive, wantPeak)
	}
	readCgroup := func(name string) string {
		b, err := os.ReadFile("/sys/fs/cgroup/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	cpuLimit, memoryLimit := readCgroup("cpu.max"), readCgroup("memory.max")
	fields := strings.Fields(cpuLimit)
	if len(fields) != 2 {
		t.Fatalf("invalid CPU quota: %q", cpuLimit)
	}
	quota, quotaErr := strconv.Atoi(fields[0])
	period, periodErr := strconv.Atoi(fields[1])
	if quotaErr != nil || periodErr != nil || period <= 0 || quota != 4*period || memoryLimit != "17179869184" {
		t.Fatalf("expected four CPUs and 16 GiB: cpu=%q memory=%q", cpuLimit, memoryLimit)
	}
	if runtime.GOMAXPROCS(0) != 4 || os.Getenv("FERN_BUILD_MEM_BUDGET_MB") != "13926" {
		t.Fatal("expected GOMAXPROCS=4 and build memory budget 13926 MiB")
	}
	report := map[string]any{"mode": mode, "scale": scale, "target": target, "units": hi - lo, "batches": samples,
		"wall_seconds": wall, "cpu_seconds": cpu, "max_child_rss_bytes": peakChild, "peak_active_children": peakActive,
		"gomaxprocs": runtime.GOMAXPROCS(0), "reservation_mb": weight, "budget_mb": os.Getenv("FERN_BUILD_MEM_BUDGET_MB"),
		"cgroup_cpu_max": cpuLimit, "cgroup_memory_max": memoryLimit, "cgroup_memory_peak": readCgroup("memory.peak"),
		"output_hashes": hashes, "verified": true}
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(result, "summary.json"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s %s: wall %.3fs cpu %.3fs peak child %d bytes active %d", scale, mode, wall, cpu, peakChild, peakActive)
}
