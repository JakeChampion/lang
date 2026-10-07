package e2ecompiler

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func storageDriverFiles(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"storage.fern", "storage_core.fern", "storage_fip.fern"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "fip", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "storage.fern")
}

func storageDiskSum(m *storageModel) int64 {
	var sum int64
	for page, data := range m.disk {
		for word, value := range data {
			sum += int64(page*m.width+word+1) * value
		}
	}
	return sum
}

func storageDataSum(m *storageModel) int64 {
	sum := storageDiskSum(m)
	for i, frame := range m.frames {
		for word, value := range frame.data {
			sum += int64(i*m.width+word+1) * value
		}
		dirty := 0
		if frame.dirty {
			dirty = 1
		}
		sum += int64((i + 1) * (frame.page*13 + dirty))
	}
	return sum
}

func storageDriverOracle(t *testing.T, depth, waves int, mode string) map[string]int64 {
	t.Helper()
	m := newStorageModel(depth, depth, 8, depth, 1)
	result := map[string]int64{"depth": int64(depth), "waves": int64(waves), "accepted": int64(depth * waves), "completed": int64(depth * waves), "refusals": int64(waves), "errors": 0, "checksum": 0, "snapshot_checks": 0, "logical_latency_sum": 0, "logical_latency_max": 0}
	for wave := 0; wave < waves; wave++ {
		result["snapshot_checks"] += storageDataSum(m)
		kind := []int{1, 2, 0, 3}[wave%4]
		delay := 0
		if mode == "delayed" {
			delay = 17
		}
		start := m.tick
		for page := 0; page < depth; page++ {
			fault := 0
			if mode == "faulted" && (wave*depth+page)%11 == 0 {
				fault = 1
			}
			m.apply(storageAction{op: 0, kind: kind, page: page, offset: wave / 4 % 8, value: int64((wave+1)*17 + page - 1000000), delay: delay, fault: fault})
			if m.status != 0 {
				t.Fatal("oracle submission failed")
			}
		}
		m.apply(storageAction{op: 0})
		if m.status != 2 {
			t.Fatal("oracle saturation did not refuse")
		}
		remaining := depth
		for remaining > 0 {
			slot := m.cursor
			before := m.requests[slot].phase
			m.apply(storageAction{op: 1})
			if m.tick-start > int64((delay+2)*depth) {
				t.Fatal("oracle did not drain")
			}
			if before != 3 && m.requests[slot].phase == 3 {
				remaining--
				result["logical_latency_sum"] += m.tick - start
			}
		}
		if m.tick-start > result["logical_latency_max"] {
			result["logical_latency_max"] = m.tick - start
		}
		for slot := 0; slot < depth; slot++ {
			m.apply(storageAction{op: 2, slot: slot, ticket: m.requests[slot].ticket})
			if m.status != 0 {
				result["errors"]++
			}
			result["checksum"] += int64(slot+1) * (int64(m.status)*1000003 + m.result)
		}
	}
	result["logical_ticks"], result["scheduling_work"] = m.tick, m.tick
	result["disk_checksum"], result["data_checksum"] = storageDiskSum(m), storageDataSum(m)
	return result
}

func checkStorageDriverOutput(t *testing.T, output string, depth, waves int, mode, sharing string) {
	t.Helper()
	assertBalancedCensus(t, output)
	var samples []int64
	var report map[string]json.RawMessage
	count := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "sample,") {
			fields := strings.Split(line, ",")
			if len(fields) != 3 {
				t.Fatalf("bad sample %q", line)
			}
			i, e1 := strconv.Atoi(fields[1])
			n, e2 := strconv.ParseInt(fields[2], 10, 64)
			if e1 != nil || e2 != nil || i != len(samples) || n < 0 {
				t.Fatalf("bad sample %q", line)
			}
			samples = append(samples, n)
		} else if strings.HasPrefix(line, "{") {
			count++
			if err := json.Unmarshal([]byte(line), &report); err != nil {
				t.Fatal(err)
			}
		}
	}
	if count != 1 || len(samples) != waves {
		t.Fatalf("reports=%d samples=%d", count, len(samples))
	}
	value := func(key string) int64 {
		t.Helper()
		var n int64
		if err := json.Unmarshal(report[key], &n); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return n
	}
	for key, want := range storageDriverOracle(t, depth, waves, mode) {
		if got := value(key); got != want {
			t.Errorf("%s=%d want %d", key, got, want)
		}
	}
	for key, want := range map[string]string{"mode": mode, "sharing": sharing} {
		var got string
		if err := json.Unmarshal(report[key], &got); err != nil || got != want {
			t.Errorf("%s=%s want %s (%v)", key, got, want, err)
		}
	}
	var total int64
	for _, sample := range samples {
		total += sample
	}
	if total > value("wall_ns") {
		t.Fatal("samples exceed measured wall time")
	}
	for _, key := range []string{"startup_ns", "startup_allocs", "startup_fresh_bytes", "wall_ns", "allocs", "first_wave_allocs", "fresh_bytes"} {
		if value(key) < 0 {
			t.Fatalf("negative %s", key)
		}
	}
	if sharing == "unique" && (value("allocs") != 0 || value("first_wave_allocs") != 0) {
		t.Fatal("unique state allocated")
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	for key, p := range map[string]int{"wave_p50_ns": 500, "wave_p95_ns": 950, "wave_p99_ns": 990, "wave_p999_ns": 999, "wave_max_ns": 1000} {
		if value(key) != samples[(len(samples)*p+999)/1000-1] {
			t.Fatalf("wrong %s", key)
		}
	}
}

func TestSelfHostStorageDriver(t *testing.T) {
	cli := buildSelfHostCLI(t)
	path := storageDriverFiles(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			env := []string{"FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1"}
			var command func(...string) *exec.Cmd
			switch target {
			case "x86-64-linux":
				bin := cli.x86Binary(t, path, env...)
				command = func(args ...string) *exec.Cmd { return runX86_64Bin(cli.runner, bin, args...) }
			case "arm64-linux":
				gcc, runner := arm64Tooling(t)
				asm, err := os.ReadFile(cli.emit(t, path, target, env...))
				if err != nil {
					t.Fatal(err)
				}
				bin := buildBinArm64(t, gcc, t.TempDir(), "storage", string(asm))
				command = func(args ...string) *exec.Cmd { return runArm64Bin(runner, bin, args...) }
			case "wasm32-wasi":
				wat := cli.emit(t, path, target, env...)
				command = func(args ...string) *exec.Cmd {
					return exec.Command(e2eharness.Wasmtime(t), append([]string{"run", wat}, args...)...)
				}
			}
			for _, tc := range []struct{ depth, waves int }{{1, 5}, {3, 19}, {17, 7}, {128, 1}} {
				for _, mode := range []string{"plain", "delayed", "faulted"} {
					for _, sharing := range []string{"unique", "shared"} {
						t.Run(strconv.Itoa(tc.depth)+"/"+mode+"/"+sharing, func(t *testing.T) {
							out, err := command(strconv.Itoa(tc.depth), strconv.Itoa(tc.waves), mode, sharing, "samples").CombinedOutput()
							if err != nil {
								t.Fatalf("%v: %s", err, out)
							}
							checkStorageDriverOutput(t, string(out), tc.depth, tc.waves, mode, sharing)
						})
					}
				}
			}
		})
	}
}

func TestSelfHostStorageDriverArguments(t *testing.T) {
	cli := buildSelfHostCLI(t)
	bin := cli.x86Binary(t, storageDriverFiles(t))
	cases := [][]string{nil, {"1"}, {"1", "5", "plain", "unique", "samples", "extra"}}
	for field, values := range map[int][]string{0: {"bad", "0", "-1", "129", "4294967297"}, 1: {"bad", "0", "-1", "10001", "4294967297"}, 2: {"other"}, 3: {"other"}, 4: {"other"}} {
		for _, value := range values {
			args := []string{"1", "5", "plain", "unique", "samples"}
			args[field] = value
			cases = append(cases, args)
		}
	}
	for i, args := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			cmd := runX86_64Bin(cli.runner, bin, args...)
			out, err := cmd.CombinedOutput()
			if err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 2 {
				t.Fatalf("expected exit2, got %v: %s", err, out)
			}
		})
	}
}
