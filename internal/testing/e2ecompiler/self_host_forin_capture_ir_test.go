package e2ecompiler

import (
	"os"
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
	cli := newStrictCLI(t)
	dir := t.TempDir()

	cases := []struct {
		name string
		src  string
		want int
		// The x86 output carries the corresponding IR function label.
		irWitness string
	}{
		{"forin-string-byte-capture",
			`function main(): i32 { let total: i32 = 0; for ch in "é" { let read = (): u8 => ch; total = total + (read() as i32); } if (total == 364) { return 42; } return 1; }`,
			42, ".Lssa_main"},
		{"forin-generic-callback-capture",
			`function items(x: i32): i64[] { return [4294967296 + (x as i64)]; } function run[T](xs: T[], callback: (T) => i64[]): i32 { let total: i64 = 0; for x in xs { for y in callback(x) { let read = (): i64 => y; total = total + read(); } } if (total == 8589934634) { return 42; } return 1; } function main(): i32 { return run([20, 22], items); }`,
			42, ".Lssa_main"},
		{"forin-binder-fn-field",
			`struct H { f: (i32) => i32, id: i32 } function main(): i32 { let acc: i32 = 0; let xs: i32[] = [1, 2, 3]; for x in xs { let h: H = H { f: (q: i32): i32 => { return q + x; }, id: x }; acc = acc + h.f(1) + h.id; } return acc; }`,
			15, ".Lssa_main"},
		{"forin-nested-two-binders",
			`struct H { f: (i32) => i32, id: i32 } function main(): i32 { let acc: i32 = 0; let xs: i32[] = [1, 2, 3]; for x in xs { for y in xs { let h: H = H { f: (q: i32): i32 => { return q + x + y; }, id: x * y }; acc = acc + h.f(1) + h.id; } } return acc; }`,
			81, ".Lssa_main"},
		{"forin-map-keys-binder",
			`import "core/map";
struct H { f: (i32) => i32, id: i32 } function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); let acc: i32 = 0; for k in m.keys() { let h: H = H { f: (x: i32): i32 => { return x + k; }, id: k }; acc = acc + h.f(5) + h.id; } return acc; }`,
			16, ".Lssa_main"},
		{"forin-map-values-binder",
			`import "core/map";
struct H { f: (i32) => i32, id: i32 } function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); let acc: i32 = 0; for v in m.values() { let h: H = H { f: (x: i32): i32 => { return x + v; }, id: v }; acc = acc + h.f(1) + h.id; } return acc; }`,
			62, ".Lssa_main"},
		// The iterator's type also resolves through field and slice expressions.
		{"forin-struct-field-array-binder",
			`struct S { items: i32[], n: i32 } struct H { f: (i32) => i32, id: i32 } function g(s: S): i32 { let acc: i32 = 0; for x in s.items { let h: H = H { f: (q: i32): i32 => { return q + x; }, id: x }; acc = acc + h.f(1) + h.id; } return acc; } function main(): i32 { return g(S { items: [1, 2, 3], n: 0 }); }`,
			15, ".Lssa_g"},
		{"forin-slice-binder",
			`struct H { f: (i32) => i32, id: i32 } function g(): i32 { let xs: i32[] = [1, 2, 3, 4]; let acc: i32 = 0; for x in xs[0:2] { let h: H = H { f: (q: i32): i32 => { return q + x; }, id: x }; acc = acc + h.f(1) + h.id; } return acc; } function main(): i32 { return g(); }`,
			8, ".Lssa_g"},
		{"forin-string-field-array-binder",
			`struct S { tags: string[], n: i32 } struct H { f: (i32) => i32, id: i32 } function g(s: S): i32 { let acc: i32 = 0; for t in s.tags { let h: H = H { f: (q: i32): i32 => { return q + t.len(); }, id: 1 }; acc = acc + h.f(0); } return acc; } function main(): i32 { return g(S { tags: ["ab", "cde"], n: 0 }); }`,
			5, ".Lssa_g"},
		// Method and function iterators use their declared array return type.
		{"forin-method-iter-binder",
			`struct S { items: i32[], n: i32 } function (s: S) get(): i32[] { return s.items; } struct H { f: (i32) => i32, id: i32 } function g(s: S): i32 { let acc: i32 = 0; for x in s.get() { let h: H = H { f: (q: i32): i32 => { return q + x; }, id: x }; acc = acc + h.f(1) + h.id; } return acc; } function main(): i32 { return g(S { items: [1, 2, 3], n: 0 }); }`,
			15, ".Lssa_g"},
		{"forin-free-fn-iter-binder",
			`function items(): i32[] { return [10, 20, 30]; } struct H { f: (i32) => i32, id: i32 } function g(): i32 { let acc: i32 = 0; for x in items() { let h: H = H { f: (q: i32): i32 => { return q + x; }, id: x }; acc = acc + h.f(1) + h.id; } return acc; } function main(): i32 { return g(); }`,
			123, ".Lssa_g"},
		// Map pair binders carry distinct types and declaration identities.
		{"forin-kv-binder-fn-field",
			`import "core/map";
struct H { f: (i32) => i32, id: i32 } function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); let acc: i32 = 0; for (k, v) in m { let h: H = H { f: (q: i32): i32 => { return q + k + v; }, id: v }; acc = acc + h.f(1) + h.id; } return acc; }`,
			65, ".Lssa_main"},
		{"forin-kv-binder-closure-array",
			`import "core/map";
function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); let acc: i32 = 0; for (k, v) in m { let fs: ((i32) => i32)[] = [(a: i32): i32 => { return a + v; }, (b: i32): i32 => { return b + k; }]; acc = acc + fs[0](1) + fs[1](1); } return acc; }`,
			13, ".Lssa_main"},
		{"forin-kv-wide-shadow",
			`import "core/map";
struct H { f: () => i64 }
function read(m: Map[string, i64], k: i32): i32 { let sum: i64 = 0; for (k, v) in m { let h = H { f: (): i64 => { return v + k.len(); } }; sum = sum + h.f(); } return (sum - 5000000000i64) as i32 + k; }
function main(): i32 { let m: Map[string, i64] = map_new(4); m = m.insert("abc", 5000000007i64); return read(m, 32); }`,
			42, ".Lssa_main"},
		{"forin-tuple-capture",
			`struct H { f: () => i64 } function main(): i32 { let xs: (i64, string)[] = [(5000000000i64, "abc")]; for (n, text) in xs { let h = H { f: (): i64 => { return n + text.len(); } }; return (h.f() - 4999999961i64) as i32; } return 1; }`,
			42, ".Lssa_main"},
		{"forin-nested-tuple-capture",
			`struct H { f: () => i64 } function main(): i32 { let xs: ((i64, string), boolean)[] = [((5000000000i64, "abc"), true)]; for ((n, text), flag) in xs { let h = H { f: (): i64 => { if (flag) { return n + text.len(); } return 0; } }; return (h.f() - 4999999961i64) as i32; } return 1; }`,
			42, ".Lssa_main"},
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
			asm := cli.emit(t, target, tc.src)
			if target == "x86-64-linux" && tc.irWitness != "" && !strings.Contains(asm, tc.irWitness) {
				t.Fatalf("%s: emitted asm missing %q — the for-in capture shape did not lower through the IR", tc.name, tc.irWitness)
			}
			var code int
			switch target {
			case "wasm32-wasi":
				code, _ = runWasm(t, asm)
			case "arm64-linux":
				gcc, qemu := arm64Tooling(t)
				code, _ = runArm64(t, gcc, qemu, asm)
			default:
				code, _ = cli.runX86(t, asm)
			}
			if code != tc.want {
				t.Errorf("%s: exit %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
