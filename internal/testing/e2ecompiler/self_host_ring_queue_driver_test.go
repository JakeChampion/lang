package e2ecompiler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func ringQueueDriver(t *testing.T, representation string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"ring_queue", "bounded_ring", "queue_inline", "queue_array"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "fip", name+".fern"))
		if err != nil {
			t.Fatal(err)
		}
		if name == "ring_queue" && representation != "ring" {
			data = []byte(strings.ReplaceAll(strings.ReplaceAll(string(data), "bounded_ring", "queue_"+representation), "Ring[i64]", "Ring"))
		}
		if err := os.WriteFile(filepath.Join(dir, name+".fern"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "ring_queue.fern")
}

func TestSelfHostRingQueueDriver(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, representation := range []string{"ring", "inline", "array"} {
		t.Run(representation, func(t *testing.T) {
			path := ringQueueDriver(t, representation)
			bin := cli.x86Binary(t, path, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			for _, capacity := range []int{1, 8, 17} {
				for _, mode := range []string{"flow", "full"} {
					for _, sharing := range []string{"unique", "shared"} {
						t.Run(fmt.Sprintf("%d/%s/%s", capacity, mode, sharing), func(t *testing.T) {
							const turns = 19
							queue := make([]int64, capacity)
							for i := range queue {
								queue[i] = int64(i + 1)
							}
							var checks, checksum int64
							for i := 0; i < turns; i++ {
								checks += queue[0] + queue[len(queue)-1]
								if mode == "flow" {
									queue = append(queue[1:], int64(capacity+i+1))
								}
							}
							for _, value := range queue {
								checksum += value
							}
							out, err := runX86_64Bin(cli.runner, bin, strconv.Itoa(capacity), "19", mode, sharing, "samples").CombinedOutput()
							if err != nil {
								t.Fatalf("%v: %s", err, out)
							}
							assertBalancedCensus(t, string(out))
							var report map[string]json.RawMessage
							var samples []int64
							for _, line := range strings.Split(string(out), "\n") {
								if strings.HasPrefix(line, "sample,") {
									var index int
									var duration int64
									if n, err := fmt.Sscanf(line, "sample,%d,%d", &index, &duration); err != nil || n != 2 || index != len(samples) || duration < 0 {
										t.Fatalf("invalid sample %q", line)
									}
									samples = append(samples, duration)
								} else if strings.HasPrefix(line, "{") {
									if report != nil {
										t.Fatal("duplicate report")
									}
									if err := json.Unmarshal([]byte(line), &report); err != nil {
										t.Fatal(err)
									}
								}
							}
							if len(samples) != turns || report == nil {
								t.Fatal("missing samples or report")
							}
							read := func(key string) int64 {
								var value int64
								if err := json.Unmarshal(report[key], &value); err != nil {
									t.Fatal(err)
								}
								return value
							}
							for key, expected := range map[string]int64{"capacity": int64(capacity), "count": int64(capacity), "turns": turns, "checks": checks, "checksum": checksum} {
								if read(key) != expected {
									t.Fatalf("%s=%d, expected%d", key, read(key), expected)
								}
							}
							for key, expected := range map[string]string{"mode": mode, "sharing": sharing} {
								var value string
								if err := json.Unmarshal(report[key], &value); err != nil || value != expected {
									t.Fatalf("%s=%s", key, report[key])
								}
							}
							if representation != "array" && (read("fill_allocs") != 0 || sharing == "unique" && read("allocs") != 0) {
								t.Fatal("fixed unique ring allocated")
							}
							sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
							for key, p := range map[string]int{"turn_p50_ns": 500, "turn_p95_ns": 950, "turn_p99_ns": 990, "turn_p999_ns": 999, "turn_max_ns": 1000} {
								if read(key) != samples[(turns*p+999)/1000-1] {
									t.Fatalf("incorrect %s", key)
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
							stderr, code := cli.exitOfFileArgs(t, path, target, nil, []string{"8", "19", mode, sharing, "samples"}, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
							if code != 0 {
								t.Fatalf("exit%d: %s", code, stderr)
							}
							assertBalancedCensus(t, stderr)
						})
					}
				}
			}
			for i, args := range [][]string{nil, {"0", "5", "flow", "unique"}, {"4097", "5", "flow", "unique"}, {"8", "0", "flow", "unique"}, {"8", "1000001", "flow", "unique"}, {"4294967297", "5", "flow", "unique"}, {"8", "5", "bad", "unique"}, {"8", "5", "flow", "bad"}, {"8", "5", "flow", "unique", "bad"}, {"8", "5", "flow", "unique", "samples", "extra"}} {
				t.Run(fmt.Sprintf("invalid%d", i), func(t *testing.T) {
					cmd := runX86_64Bin(cli.runner, bin, args...)
					out, err := cmd.CombinedOutput()
					if err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 2 {
						t.Fatalf("expectedexit2, got%v: %s", err, out)
					}
				})
			}
		})
	}
}
