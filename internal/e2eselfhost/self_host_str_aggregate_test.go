package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A `str` struct field or tuple element is a view like any other `str`, and
// the AST lowering reads it as the string box it is (#9915). Each row runs
// through the production CLI on both lowerings; a pinned row still leaks its
// view there (#10331), and its counts move when that closes. The typed
// lowering refuses a tuple with a `str` element (#10331), so a refused row's
// semantic leg compiles without FERN_SEM_IR_STRICT and runs the AST fallback
// the CLI ships.
var strAggregateCases = []struct {
	name    string
	src     string
	want    int
	refused bool
	pinned  map[string][2]int64
}{
	{"tuple_literal", `function main(): i32 { var p: (str, i32) = ("abc", 4); return p.0.len() + p.1; }
`, 7, true, nil},
	{"tuple_result", `function mk(): (str, i32) { return ("abc", 4); }
function main(): i32 { var p: (str, i32) = mk(); return p.0.len() + p.1; }
`, 7, true, nil},
	{"field_literal", `struct H { s: str, n: i32 }
function main(): i32 { var h: H = H { s: "abc", n: 1 }; return h.n + h.s.len(); }
`, 4, false, nil},
	{"field_view", `struct H { s: str, n: i32 }
function mk(t: string): H { return H { s: slice_unchecked(t, 0, 3), n: 1 }; }
function main(): i32 { var h: H = mk("abcde"); return h.n + h.s.len(); }
`, 4, false, map[string][2]int64{"ast": {2, 1}}},
	{"tuple_view", `function main(): i32 { var t: string = "abcde"; var p: (str, i32) = (slice_unchecked(t, 0, 3), 4); return p.0.len() + p.1; }
`, 7, true, map[string][2]int64{"semantic": {2, 0}, "ast": {2, 0}}},
}

var strAggregateLowerings = []struct{ name, env string }{
	{"semantic", "FERN_SEM_IR=1"},
	{"ast", "FERN_SEM_IR="},
}

// strAggregateEnv is the compiler environment for one row and lowering.
func strAggregateEnv(refused bool, lowering, env string) []string {
	out := []string{"FERN_LEAKCHECK=1", env}
	if refused && lowering == "semantic" {
		out = append(out, "FERN_SEM_IR_STRICT=")
	}
	return out
}

func writeStrAggregateSrc(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertStrAggregateCensus(t *testing.T, stderr string, pinned map[string][2]int64, lowering string) {
	t.Helper()
	if pin, ok := pinned[lowering]; ok {
		assertLeakPinned(t, stderr, pin, "#10331")
		return
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostStrAggregateX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range strAggregateCases {
		src := writeStrAggregateSrc(t, tc.name, tc.src)
		for _, lw := range strAggregateLowerings {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, strAggregateEnv(tc.refused, lw.name, lw.env)...), nil)
				if exit != tc.want {
					t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				assertStrAggregateCensus(t, stderr, tc.pinned, lw.name)
			})
		}
	}
}

// TestSelfHostStrAggregateNative holds the native compiler to the same rows,
// every one clean.
func TestSelfHostStrAggregateNative(t *testing.T) {
	_, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	for _, tc := range strAggregateCases {
		t.Run(tc.name, func(t *testing.T) {
			src := writeStrAggregateSrc(t, tc.name, tc.src)
			bin := filepath.Join(t.TempDir(), tc.name+".nat")
			compile := exec.Command(cli, "-target", "x86-64-linux", "-o", bin, src)
			compile.Env = childEnv("FERN_LEAKCHECK=1")
			if out, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("native compile: %v\n%s", err, out)
			}
			stderr, exit := runWithStdin(t, runner, bin, nil)
			if exit != tc.want {
				t.Fatalf("native: exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostStrAggregateArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	for _, tc := range strAggregateCases {
		src := writeStrAggregateSrc(t, tc.name, tc.src)
		for _, lw := range strAggregateLowerings {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", strAggregateEnv(tc.refused, lw.name, lw.env)...))
				if err != nil {
					t.Fatal(err)
				}
				cmd := runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), tc.name, string(asm)))
				var eb strings.Builder
				cmd.Stderr = &eb
				_ = cmd.Run()
				if code := cmd.ProcessState.ExitCode(); code != tc.want {
					t.Fatalf("exit = %d, want %d\n%s", code, tc.want, eb.String())
				}
				assertStrAggregateCensus(t, eb.String(), tc.pinned, lw.name)
			})
		}
	}
}

func TestSelfHostStrAggregateWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	cli := buildSelfHostCLI(t)
	for _, tc := range strAggregateCases {
		src := writeStrAggregateSrc(t, tc.name, tc.src)
		for _, lw := range strAggregateLowerings {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", strAggregateEnv(tc.refused, lw.name, lw.env)...))
				if exit != tc.want {
					t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				assertStrAggregateCensus(t, stderr, tc.pinned, lw.name)
			})
		}
	}
}
