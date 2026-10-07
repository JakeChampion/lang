package e2ecompiler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func dspDriverFiles(t *testing.T, dir string) string {
	t.Helper()
	for _, name := range []string{"dsp.fern", "dsp_core.fern", "dsp_fip.fern", "dsp_direct.fern"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "fip", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "dsp.fern")
}

func dspDriverOracle(block, blocks, delay int) map[string]int64 {
	hash := func(values []float64) int64 {
		var result int64
		for _, value := range values {
			result = (result*65599 + int64(value*4096) + 1048576) % 1000000007
		}
		return result
	}
	ring := make([]float64, delay)
	output := make([]float64, block)
	cursor := 0
	previous, previous2 := 0.0, 0.0
	var checks int64
	for tick := 0; tick < blocks; tick++ {
		for i := range output {
			x := float64(i*73%257-128) / 64
			if i%31 == 0 {
				x = 4
			} else if i%37 == 0 {
				x = -4
			} else if i%11 == 0 {
				x = 0
			}
			gained := x * 1.5
			filtered := gained*0.5 + previous*0.25 + previous2*0.25
			previous2, previous = previous, gained
			wet := ring[cursor]
			ring[cursor] = filtered + wet*0.25
			cursor = (cursor + 1) % delay
			output[i] = min(1.0, max(-1.0, filtered*0.75+wet*0.5))
		}
		checks = (checks + hash(output)) % 1000000007
	}
	return map[string]int64{"block_size": int64(block), "blocks": int64(blocks), "delay_samples": int64(delay), "processed_samples": int64(block * blocks), "checksum": hash(output), "block_checks": checks, "ring_checksum": hash(ring), "cursor": int64(cursor)}
}

func TestSelfHostDSPDriver(t *testing.T) {
	cli := buildSelfHostCLI(t)
	path := dspDriverFiles(t, t.TempDir())
	bin := cli.x86Binary(t, path, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	for _, tc := range []struct {
		name                 string
		block, blocks, delay int
	}{{"pilot", 7, 5, 3}, {"single", 1, 9, 1}, {"partial_ring", 64, 3, 67}} {
		for _, variant := range []string{"fip", "direct"} {
			for _, sharing := range []string{"unique", "shared"} {
				t.Run(tc.name+"/"+variant+"/"+sharing, func(t *testing.T) {
					out, err := runX86_64Bin(cli.runner, bin, variant, strconv.Itoa(tc.block), strconv.Itoa(tc.blocks), strconv.Itoa(tc.delay), sharing, "samples").CombinedOutput()
					if err != nil {
						t.Fatalf("%v: %s", err, out)
					}
					assertBalancedCensus(t, string(out))
					var samples []int64
					var report map[string]json.RawMessage
					reports := 0
					for _, line := range strings.Split(string(out), "\n") {
						if strings.HasPrefix(line, "sample,") {
							fields := strings.Split(line, ",")
							if len(fields) != 3 {
								t.Fatalf("bad sample %q", line)
							}
							index, e1 := strconv.Atoi(fields[1])
							duration, e2 := strconv.ParseInt(fields[2], 10, 64)
							if e1 != nil || e2 != nil || index != len(samples) || duration < 0 {
								t.Fatalf("bad sample %q", line)
							}
							samples = append(samples, duration)
						} else if strings.HasPrefix(line, "{") {
							reports++
							if err := json.Unmarshal([]byte(line), &report); err != nil {
								t.Fatal(err)
							}
						}
					}
					if reports != 1 || len(samples) != tc.blocks {
						t.Fatalf("reports=%d samples=%d", reports, len(samples))
					}
					value := func(key string) int64 {
						t.Helper()
						var n int64
						if err := json.Unmarshal(report[key], &n); err != nil {
							t.Fatalf("%s: %v", key, err)
						}
						return n
					}
					for key, want := range dspDriverOracle(tc.block, tc.blocks, tc.delay) {
						if got := value(key); got != want {
							t.Errorf("%s=%d want %d", key, got, want)
						}
					}
					if variant == "fip" && sharing == "unique" && (value("allocs") != 0 || value("first_callback_allocs") != 0) {
						t.Fatal("unique FIP callback allocated")
					}
					sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
					for key, p := range map[string]int{"block_p50_ns": 500, "block_p95_ns": 950, "block_p99_ns": 990, "block_p999_ns": 999, "block_max_ns": 1000} {
						if got := value(key); got != samples[(len(samples)*p+999)/1000-1] {
							t.Errorf("incorrect %s=%d", key, got)
						}
					}
				})
			}
		}
	}
	for _, target := range []string{"arm64-linux", "wasm32-wasi"} {
		for _, variant := range []string{"fip", "direct"} {
			for _, sharing := range []string{"unique", "shared"} {
				t.Run(target+"/"+variant+"/"+sharing, func(t *testing.T) {
					stderr, code := cli.exitOfFileArgs(t, path, target, nil, []string{variant, "7", "5", "3", sharing, "samples"}, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("exit %d: %s", code, stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
		}
	}
}

func TestSelfHostDSPDriverArguments(t *testing.T) {
	cli := buildSelfHostCLI(t)
	bin := cli.x86Binary(t, dspDriverFiles(t, t.TempDir()))
	cases := [][]string{nil, {"fip"}, {"fip", "7", "5", "3", "unique", "samples", "extra"}}
	for field, values := range map[int][]string{0: {"other"}, 1: {"bad", "0", "-1", "4097", "4294967297"}, 2: {"bad", "0", "-1", "1000001", "4294967297"}, 3: {"bad", "0", "-1", "8193", "4294967297"}, 4: {"other"}, 5: {"other"}} {
		for _, value := range values {
			args := []string{"fip", "7", "5", "3", "unique", "samples"}
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
