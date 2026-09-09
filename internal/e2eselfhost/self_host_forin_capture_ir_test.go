package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Loop captures retain the iterator's checked element or key/value types.
func TestSelfHostForinCaptureIRX86_64(t *testing.T) {
	testForinCaptureIR(t, "x86-64-linux")
}

func TestSelfHostForinCaptureIRArm64(t *testing.T) {
	testForinCaptureIR(t, "arm64-linux")
}

func TestSelfHostForinCaptureIRWasm(t *testing.T) {
	testForinCaptureIR(t, "wasm32-wasi")
}

func testForinCaptureIR(t *testing.T, target string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	driverName := "asm_ir_run.fern"
	args := []string{"-target", target}
	if target == "wasm32-wasi" {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Fatal("wasmtime is required for loop capture coverage")
		}
		driverName = "wasm_ir_run.fern"
		args = nil
	}
	copySelfHostDriver(t, dir, driverName)
	driverBin := buildSelfHostBin(t, gcc, dir, driverName, "driver")

	cases := []struct {
		name string
		src  string
		want int
		// The x86 output carries the corresponding IR function label.
		irWitness string
	}{
		{"forin-binder-fn-field",
			`struct H { f: (i32) => i32, id: i32 } function main(): i32 { var acc: i32 = 0; var xs: i32[] = [1, 2, 3]; for x in xs { var h: H = H { f: (q: i32): i32 => { return q + x; }, id: x }; acc = acc + h.f(1) + h.id; } return acc; }`,
			15, ".Lir_main"},
		{"forin-nested-two-binders",
			`struct H { f: (i32) => i32, id: i32 } function main(): i32 { var acc: i32 = 0; var xs: i32[] = [1, 2, 3]; for x in xs { for y in xs { var h: H = H { f: (q: i32): i32 => { return q + x + y; }, id: x * y }; acc = acc + h.f(1) + h.id; } } return acc; }`,
			81, ".Lir_main"},
		{"forin-map-keys-binder",
			`import "core/map";
struct H { f: (i32) => i32, id: i32 } function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); var acc: i32 = 0; for k in m.keys() { var h: H = H { f: (x: i32): i32 => { return x + k; }, id: k }; acc = acc + h.f(5) + h.id; } return acc; }`,
			16, ".Lir_main"},
		{"forin-map-values-binder",
			`import "core/map";
struct H { f: (i32) => i32, id: i32 } function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); var acc: i32 = 0; for v in m.values() { var h: H = H { f: (x: i32): i32 => { return x + v; }, id: v }; acc = acc + h.f(1) + h.id; } return acc; }`,
			62, ".Lir_main"},
		// The iterator's type also resolves through field and slice expressions.
		{"forin-struct-field-array-binder",
			`struct S { items: i32[], n: i32 } struct H { f: (i32) => i32, id: i32 } function g(s: S): i32 { var acc: i32 = 0; for x in s.items { var h: H = H { f: (q: i32): i32 => { return q + x; }, id: x }; acc = acc + h.f(1) + h.id; } return acc; } function main(): i32 { return g(S { items: [1, 2, 3], n: 0 }); }`,
			15, ".Lir_g"},
		{"forin-slice-binder",
			`struct H { f: (i32) => i32, id: i32 } function g(): i32 { var xs: i32[] = [1, 2, 3, 4]; var acc: i32 = 0; for x in xs[0:2] { var h: H = H { f: (q: i32): i32 => { return q + x; }, id: x }; acc = acc + h.f(1) + h.id; } return acc; } function main(): i32 { return g(); }`,
			8, ".Lir_g"},
		{"forin-string-field-array-binder",
			`struct S { tags: string[], n: i32 } struct H { f: (i32) => i32, id: i32 } function g(s: S): i32 { var acc: i32 = 0; for t in s.tags { var h: H = H { f: (q: i32): i32 => { return q + t.len(); }, id: 1 }; acc = acc + h.f(0); } return acc; } function main(): i32 { return g(S { tags: ["ab", "cde"], n: 0 }); }`,
			5, ".Lir_g"},
		// Method and function iterators use their declared array return type.
		{"forin-method-iter-binder",
			`struct S { items: i32[], n: i32 } function (s: S) get(): i32[] { return s.items; } struct H { f: (i32) => i32, id: i32 } function g(s: S): i32 { var acc: i32 = 0; for x in s.get() { var h: H = H { f: (q: i32): i32 => { return q + x; }, id: x }; acc = acc + h.f(1) + h.id; } return acc; } function main(): i32 { return g(S { items: [1, 2, 3], n: 0 }); }`,
			15, ".Lir_g"},
		{"forin-free-fn-iter-binder",
			`function items(): i32[] { return [10, 20, 30]; } struct H { f: (i32) => i32, id: i32 } function g(): i32 { var acc: i32 = 0; for x in items() { var h: H = H { f: (q: i32): i32 => { return q + x; }, id: x }; acc = acc + h.f(1) + h.id; } return acc; } function main(): i32 { return g(); }`,
			123, ".Lir_g"},
		// Map pair binders carry distinct types and declaration identities.
		{"forin-kv-binder-fn-field",
			`import "core/map";
struct H { f: (i32) => i32, id: i32 } function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); var acc: i32 = 0; for (k, v) in m { var h: H = H { f: (q: i32): i32 => { return q + k + v; }, id: v }; acc = acc + h.f(1) + h.id; } return acc; }`,
			65, ".Lir_main"},
		{"forin-kv-binder-closure-array",
			`import "core/map";
function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); var acc: i32 = 0; for (k, v) in m { var fs: ((i32) => i32)[] = [(a: i32): i32 => { return a + v; }, (b: i32): i32 => { return b + k; }]; acc = acc + fs[0](1) + fs[1](1); } return acc; }`,
			13, ".Lir_main"},
		{"forin-kv-wide-shadow",
			`import "core/map";
struct H { f: () => i64 }
function read(m: Map[string, i64], k: i32): i32 { var sum: i64 = 0; for (k, v) in m { var h = H { f: (): i64 => { return v + k.len(); } }; sum = sum + h.f(); } return (sum - 5000000000i64) as i32 + k; }
function main(): i32 { var m: Map[string, i64] = map_new(4); m = m.insert("abc", 5000000007i64); return read(m, 32); }`,
			42, ".Lir_main"},
		{"forin-tuple-capture",
			`struct H { f: () => i64 } function main(): i32 { var xs: (i64, string)[] = [(5000000000i64, "abc")]; for (n, text) in xs { var h = H { f: (): i64 => { return n + text.len(); } }; return (h.f() - 4999999961i64) as i32; } return 1; }`,
			42, ".Lir_main"},
		{"forin-nested-tuple-capture",
			`struct H { f: () => i64 } function main(): i32 { var xs: ((i64, string), boolean)[] = [((5000000000i64, "abc"), true)]; for ((n, text), flag) in xs { var h = H { f: (): i64 => { if (flag) { return n + text.len(); } return 0; } }; return (h.f() - 4999999961i64) as i32; } return 1; }`,
			42, ".Lir_main"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, want := runFixtureInterp(t, entry, ""); want != tc.want {
				t.Fatalf("interpreter returned %d, want %d", want, tc.want)
			}
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), args...)
			if len(asm) == 0 {
				t.Fatalf("%s: self-host compiler emitted 0 bytes", tc.name)
			}
			if target == "x86-64-linux" && tc.irWitness != "" && !strings.Contains(string(asm), tc.irWitness) {
				t.Fatalf("%s: emitted asm missing %q — the for-in capture shape did not lower through the IR", tc.name, tc.irWitness)
			}
			var cmd *exec.Cmd
			switch target {
			case "wasm32-wasi":
				wat := filepath.Join(dir, tc.name+".wat")
				if err := os.WriteFile(wat, asm, 0o644); err != nil {
					t.Fatal(err)
				}
				cmd = exec.Command("wasmtime", "run", wat)
			case "arm64-linux":
				linker, armrunner := arm64Tooling(t)
				bin := buildBin(t, linker, dir, tc.name, string(asm))
				cmd = runArm64Bin(armrunner, bin)
			default:
				bin := buildBin(t, gcc, dir, tc.name, string(asm))
				cmd = runX86_64Bin(runner, bin)
			}
			out, err := cmd.CombinedOutput()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.want {
				t.Errorf("%s: %v, want exit %d\n%s", tc.name, err, tc.want, out)
			}
		})
	}
}
