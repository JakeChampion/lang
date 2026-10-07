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

func brokerDriverFiles(t *testing.T, dir string) string {
	t.Helper()
	for _, name := range []string{"broker.fern", "broker_core.fern"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "fip", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "broker.fern")
}

func brokerDriverOracle(subscribers, occupancy, turns int, mode string) map[string]int64 {
	// Independent serial stream. No subject queues or lifecycle helpers.
	payload := func(serial int) int64 { return int64(serial)*17 - 1000000 }
	var observed, queued int64
	for i := 1; i <= turns; i++ {
		if mode == "flow" {
			observed += payload(i)
		} else {
			observed += payload(1)
		}
	}
	for i := 1; i <= occupancy; i++ {
		serial := i
		if mode == "flow" {
			serial += turns
		}
		queued += payload(serial)
	}
	result := map[string]int64{"observed_checksum": observed, "queued_checksum": queued, "free_slots": 0, "fill_allocs": 0}
	if mode == "flow" {
		result["serial"] = int64(occupancy + turns)
		result["deliveries"] = int64(subscribers * turns)
		result["refusals"] = 0
		result["producer_slot"] = -1
	} else {
		result["serial"] = int64(occupancy + 1)
		result["deliveries"] = 0
		result["refusals"] = int64(turns)
		result["producer_slot"] = int64(occupancy)
	}
	return result
}

func TestSelfHostBrokerDriver(t *testing.T) {
	testBrokerDriverRepresentation(t, false)
}

func TestSelfHostRingBrokerDriver(t *testing.T) {
	testBrokerDriverRepresentation(t, true)
}

func testBrokerDriverRepresentation(t *testing.T, ring bool) {
	cli := buildSelfHostCLI(t)
	path := brokerDriverFiles(t, t.TempDir())
	if ring {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source := prepareBrokerRepresentation(t, filepath.Dir(path), string(data), true)
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bin := cli.x86Binary(t, path, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	for _, tc := range []struct {
		name                          string
		subscribers, occupancy, turns int
	}{{"pilot", 1, 1, 5}, {"fanout", 4, 8, 19}, {"max_subscribers", 16, 3, 7}} {
		for _, mode := range []string{"flow", "full"} {
			for _, sharing := range []string{"unique", "shared"} {
				t.Run(tc.name+"/"+mode+"/"+sharing, func(t *testing.T) {
					out, err := runX86_64Bin(cli.runner, bin, strconv.Itoa(tc.subscribers), strconv.Itoa(tc.occupancy), strconv.Itoa(tc.turns), mode, sharing, "samples").CombinedOutput()
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
					if reports != 1 || len(samples) != tc.turns {
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
					for key, want := range brokerDriverOracle(tc.subscribers, tc.occupancy, tc.turns, mode) {
						if got := value(key); got != want {
							t.Errorf("%s=%d want %d", key, got, want)
						}
					}
					if sharing == "unique" && (value("allocs") != 0 || value("first_turn_allocs") != 0) {
						t.Fatal("unique broker allocated")
					}
					sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
					for key, p := range map[string]int{"turn_p50_ns": 500, "turn_p95_ns": 950, "turn_p99_ns": 990, "turn_p999_ns": 999, "turn_max_ns": 1000} {
						if got := value(key); got != samples[(len(samples)*p+999)/1000-1] {
							t.Errorf("incorrect %s=%d", key, got)
						}
					}
				})
			}
		}
	}
	for _, target := range []string{"arm64-linux", "wasm32-wasi"} {
		for _, mode := range []string{"flow", "full"} {
			for _, sharing := range []string{"unique", "shared"} {
				t.Run(target+"/"+mode+"/"+sharing, func(t *testing.T) {
					stderr, code := cli.exitOfFileArgs(t, path, target, nil, []string{"4", "8", "19", mode, sharing, "samples"}, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("exit %d: %s", code, stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
		}
	}
}

func TestSelfHostBrokerDriverArguments(t *testing.T) {
	cli := buildSelfHostCLI(t)
	bin := cli.x86Binary(t, brokerDriverFiles(t, t.TempDir()))
	cases := [][]string{nil, {"1"}, {"1", "8", "5", "flow", "unique", "samples", "extra"}, {"1", "4096", "5", "full", "unique"}}
	for field, values := range map[int][]string{0: {"bad", "0", "-1", "17", "4294967297"}, 1: {"bad", "0", "-1", "4097", "4294967297"}, 2: {"bad", "0", "-1", "1000001", "4294967297"}, 3: {"other"}, 4: {"other"}, 5: {"other"}} {
		for _, value := range values {
			args := []string{"1", "8", "5", "flow", "unique", "samples"}
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
