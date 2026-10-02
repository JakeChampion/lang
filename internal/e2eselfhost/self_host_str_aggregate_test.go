package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A `str` struct field or tuple element is a view like any other `str`
// (#9915). Each row runs through the production CLI and balances.
var strAggregateCases = []struct {
	name string
	src  string
	want int
}{
	{"tuple_literal", `function main(): i32 { let p: (str, i32) = ("abc", 4); return p.0.len() + p.1; }
`, 7},
	{"tuple_result", `function mk(): (str, i32) { return ("abc", 4); }
function main(): i32 { let p: (str, i32) = mk(); return p.0.len() + p.1; }
`, 7},
	{"field_literal", `struct H { s: str, n: i32 }
function main(): i32 { let h: H = H { s: "abc", n: 1 }; return h.n + h.s.len(); }
`, 4},
	{"field_view", `struct H { s: str, n: i32 }
function mk(t: string): H { return H { s: slice_unchecked(t, 0, 3), n: 1 }; }
function main(): i32 { let h: H = mk("abcde"); return h.n + h.s.len(); }
`, 4},
	{"tuple_view", `function main(): i32 { let t: string = "abcde"; let p: (str, i32) = (slice_unchecked(t, 0, 3), 4); return p.0.len() + p.1; }
`, 7},
}

func writeStrAggregateSrc(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostStrAggregateX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range strAggregateCases {
		src := writeStrAggregateSrc(t, tc.name, tc.src)
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1"), nil)
			if exit != tc.want {
				t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
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
		t.Run(tc.name, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", "FERN_LEAKCHECK=1"))
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
			assertBalancedCensus(t, eb.String())
		})
	}
}

func TestSelfHostStrAggregateWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	cli := buildSelfHostCLI(t)
	for _, tc := range strAggregateCases {
		src := writeStrAggregateSrc(t, tc.name, tc.src)
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1"))
			if exit != tc.want {
				t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
