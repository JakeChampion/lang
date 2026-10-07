package e2ecompiler

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

type kvDriverCase struct {
	name                                     string
	capacity, key, value, batch, turns, load int
	mix                                      string
}

func kvDriverOracle(c kvDriverCase) (map[string]int64, map[string]string) {
	const modulus int64 = 2147483647
	universe := max(1, c.capacity*c.load/100)
	stride := 1 + c.key + c.value
	request := func(n, id int, op byte) []byte {
		wire := make([]byte, stride)
		wire[0] = op
		for j := 0; j < c.key; j++ {
			v := id*31 + j
			if j == 0 {
				v = id
			}
			if j == 1 {
				v = id >> 8
			}
			wire[1+j] = byte(v)
		}
		for j := 0; j < c.value; j++ {
			wire[1+c.key+j] = byte(n*17 + j*13 + 7)
		}
		return wire
	}
	entries := map[string][]byte{}
	var seed, output []byte
	for n := 0; n < min(c.capacity, universe)/2; n++ {
		seed = append(seed, request(n, n, 1)...)
	}
	for at := 0; at < len(seed); at += c.batch * stride {
		output = kvConfigReference(entries, seed[at:min(len(seed), at+c.batch*stride)], c.capacity, c.key, c.value)
	}
	var tape []byte
	read, put, inc := 50, 25, 15
	if c.mix == "read" {
		read, put, inc = 80, 10, 5
	}
	if c.mix == "write" {
		read, put, inc = 20, 45, 20
	}
	for n := 0; n < c.batch*100; n++ {
		phase := n * 37 % 100
		op := byte(3)
		switch {
		case phase < read:
			op = 2
		case phase < read+put:
			op = 1
		case phase < read+put+inc:
			op = 4
		}
		id := int((int64(n)*1103515245+12345)%modulus) % universe
		tape = append(tape, request(n, id, op)...)
	}
	fold := func(d int64, b []byte) int64 {
		for _, v := range b {
			d = (d*1000003 + int64(v)) % modulus
		}
		return d
	}
	var responses, checks int64
	for turn := 0; turn < c.turns; turn++ {
		at := turn % 100 * c.batch * stride
		input := tape[at : at+c.batch*stride]
		observed := fold(int64(len(entries)), entries[string(input[1:1+c.key])])
		checks = (checks + fold(observed, output)) % modulus
		output = kvConfigReference(entries, input, c.capacity, c.key, c.value)
		responses = fold(responses, output)
	}
	exported := map[string]string{}
	for k, v := range entries {
		exported[hex.EncodeToString([]byte(k))] = hex.EncodeToString(v)
	}
	return map[string]int64{"universe": int64(universe), "live": int64(len(entries)), "response_digest": responses, "snapshot_checks": checks,
		"capacity": int64(c.capacity), "key_bytes": int64(c.key), "value_bytes": int64(c.value), "batch": int64(c.batch), "turns": int64(c.turns), "load": int64(c.load)}, exported
}

func kvDriverFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"kv_config", "kv_workload", "kv_config_core", "kv_config_map", "kv_config_pmap", "kv_config_records"} {
		data, err := os.ReadFile(filepath.Join("../../../examples/fip", name+".fern"))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, name+".fern"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "kv_config.fern")
}

func kvDriverCommand(t *testing.T, cli *selfHostCLI, path, target string) func(...string) *exec.Cmd {
	t.Helper()
	env := []string{"FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1"}
	switch target {
	case "x86-64-linux":
		bin := cli.x86Binary(t, path, env...)
		return func(args ...string) *exec.Cmd { return runX86_64Bin(cli.runner, bin, args...) }
	case "arm64-linux":
		gcc, qemu := arm64Tooling(t)
		asm, err := os.ReadFile(cli.emit(t, path, target, env...))
		if err != nil {
			t.Fatal(err)
		}
		bin := buildBinArm64(t, gcc, filepath.Dir(path), "kv-driver", string(asm))
		return func(args ...string) *exec.Cmd { return runArm64Bin(qemu, bin, args...) }
	case "wasm32-wasi":
		wasmtime := e2eharness.Wasmtime(t)
		bin := cli.emit(t, path, target, env...)
		return func(args ...string) *exec.Cmd {
			return exec.Command(wasmtime, append([]string{"run", bin}, args...)...)
		}
	default:
		t.Fatalf("unsupported target %s", target)
		return nil
	}
}

func checkKVDriverOutput(t *testing.T, out string, c kvDriverCase, representation, sharing string) {
	t.Helper()
	var report map[string]json.RawMessage
	reports := 0
	entries := map[string]string{}
	var samples []int64
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		switch {
		case strings.HasPrefix(line, "entry,"):
			f := strings.Split(line, ",")
			if len(f) != 3 {
				t.Fatalf("bad entry %q", line)
			}
			if _, found := entries[f[1]]; found {
				t.Fatalf("duplicate entry %q", line)
			}
			entries[f[1]] = f[2]
		case strings.HasPrefix(line, "sample,"):
			f := strings.Split(line, ",")
			if len(f) != 3 {
				t.Fatalf("bad sample %q", line)
			}
			i, e1 := strconv.Atoi(f[1])
			n, e2 := strconv.ParseInt(f[2], 10, 64)
			if e1 != nil || e2 != nil || i != len(samples) || n < 0 {
				t.Fatalf("bad sample %q", line)
			}
			samples = append(samples, n)
		case strings.HasPrefix(line, "{"):
			reports++
			if err := json.Unmarshal([]byte(line), &report); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unexpected output %q", line)
		}
	}
	if reports != 1 || len(samples) != c.turns {
		t.Fatalf("reports=%d samples=%d", reports, len(samples))
	}
	number := func(key string) int64 {
		t.Helper()
		var n int64
		if err := json.Unmarshal(report[key], &n); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return n
	}
	want, exported := kvDriverOracle(c)
	if !reflect.DeepEqual(entries, exported) {
		t.Fatalf("entries=%v want %v", entries, exported)
	}
	for k, v := range want {
		if got := number(k); got != v {
			t.Errorf("%s=%d want %d", k, got, v)
		}
	}
	for k, v := range map[string]string{"representation": representation, "sharing": sharing, "mix": c.mix} {
		var got string
		if err := json.Unmarshal(report[k], &got); err != nil || got != v {
			t.Errorf("%s=%s want %s (%v)", k, got, v, err)
		}
	}
	for _, k := range []string{"wall_ns", "allocs", "first_batch_allocs", "fresh_bytes", "startup_ns", "startup_allocs", "startup_fresh_bytes", "seed_allocs"} {
		if number(k) < 0 {
			t.Errorf("negative %s", k)
		}
	}
	if representation == "fip" && sharing == "unique" && number("allocs") != 0 {
		t.Fatal("unique bounded core allocated")
	}
	var total int64
	for _, n := range samples {
		total += n
	}
	if total > number("wall_ns") || number("first_batch_allocs") > number("allocs") {
		t.Fatal("inconsistent metrics")
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	for k, p := range map[string]int{"batch_p50_ns": 500, "batch_p95_ns": 950, "batch_p99_ns": 990, "batch_p999_ns": 999, "batch_max_ns": 1000} {
		if number(k) != samples[(len(samples)*p+999)/1000-1] {
			t.Errorf("incorrect %s", k)
		}
	}
}

func TestSelfHostKVConfigDriver(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			command := kvDriverCommand(t, cli, kvDriverFile(t), target)
			for _, c := range []kvDriverCase{
				{"pilot", 5, 2, 9, 3, 5, 100, "mixed"},
				{"tape_wrap", 1, 1, 8, 1, 103, 200, "write"},
				{"wide", 3, 64, 256, 2, 7, 50, "read"},
				{"full_tape", 17, 3, 13, 7, 101, 150, "mixed"},
			} {
				for _, representation := range []string{"fip", "map", "pmap"} {
					for _, sharing := range []string{"unique", "shared"} {
						t.Run(c.name+"/"+representation+"/"+sharing, func(t *testing.T) {
							cmd := command(representation, strconv.Itoa(c.capacity), strconv.Itoa(c.key), strconv.Itoa(c.value), strconv.Itoa(c.batch), strconv.Itoa(c.turns), c.mix, strconv.Itoa(c.load), sharing, "samples")
							var stderr bytes.Buffer
							cmd.Stderr = &stderr
							out, err := cmd.Output()
							if err != nil {
								t.Fatalf("%v: %s", err, stderr.String())
							}
							assertBalancedCensus(t, stderr.String())
							checkKVDriverOutput(t, string(out), c, representation, sharing)
						})
					}
				}
			}
		})
	}
}

func TestSelfHostKVConfigDriverArguments(t *testing.T) {
	cli := buildSelfHostCLI(t)
	command := kvDriverCommand(t, cli, kvDriverFile(t), "x86-64-linux")
	valid := []string{"fip", "5", "2", "9", "3", "5", "mixed", "100", "unique", "samples"}
	cases := [][]string{nil, {"fip"}, append(append([]string{}, valid...), "extra"), {"fip", "257", "1", "8", "1", "5", "mixed", "100", "unique"}}
	for field, values := range map[int][]string{0: {"other"}, 1: {"bad", "0", "-1", "4097", "4294967297"}, 2: {"0", "65"}, 3: {"7", "257"}, 4: {"0", "1025"}, 5: {"0", "1000001"}, 6: {"other"}, 7: {"0", "201"}, 8: {"other"}, 9: {"other"}} {
		for _, v := range values {
			args := append([]string{}, valid...)
			args[field] = v
			cases = append(cases, args)
		}
	}
	for i, args := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			cmd := command(args...)
			out, err := cmd.CombinedOutput()
			if err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 2 {
				t.Fatalf("expected exit2: %v %s", err, out)
			}
		})
	}
}
