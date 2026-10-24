package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostWideCaptureIR(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	const outcome = "enum Outcome { Pass, Fail(i32) }\n" +
		"function run(f: () => Outcome): i32 {\n" +
		"    match (f()) { Pass => { return 0; }, Fail(n) => { return n; } }\n" +
		"}\n"

	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"escaping-i64-capture", "function make(n: i64): (i32) => i32 { return (x: i32): i32 => { return x + (n as i32); }; }\nfunction main(): i32 { let f = make(100 as i64); return f(5); }", 105},
		{"escaping-u64-capture", "function make(n: u64): (i32) => i32 { return (x: i32): i32 => { return x + (n as i32); }; }\nfunction main(): i32 { let f = make(100 as u64); return f(5); }", 105},
		{"escaping-f64-capture", "function make(d: f64): (i32) => i32 { return (x: i32): i32 => { return x + (d as i32); }; }\nfunction main(): i32 { let f = make(100.0); return f(5); }", 105},
		{"escaping-f32-capture", "function make(d: f32): (i32) => i32 { return (x: i32): i32 => { return x + (d as i32); }; }\nfunction main(): i32 { let f = make(100.0f32); return f(5); }", 105},
		{"escaping-i32-capture-control", "function make(n: i32): (i32) => i32 { return (x: i32): i32 => { return x + n; }; }\nfunction main(): i32 { let f = make(100); return f(5); }", 105},

		{"escaping-i64-local-capture", "function make(): (i32) => i32 { let n: i64 = 100 as i64; return (x: i32): i32 => { return x + (n as i32); }; }\nfunction main(): i32 { let f = make(); return f(5); }", 105},

		{"callarg-i64-capture", outcome + "function check(): i32 { let b: i64 = 42 as i64; return run((): Outcome => { return Fail(b as i32); }); }\nfunction main(): i32 { return check(); }", 42},
		{"callarg-i64-param-capture", outcome + "function check(b: i64): i32 { return run((): Outcome => { return Fail(b as i32); }); }\nfunction main(): i32 { return check(42 as i64); }", 42},
		{"callarg-f64-capture", outcome + "function check(): i32 { let d: f64 = 10.5; return run((): Outcome => { return Fail((d * 4.0) as i32); }); }\nfunction main(): i32 { return check(); }", 42},
		{"array-elem-f32-capture", "function main(): i32 { let w: f32 = 6.0f32; let fs: ((i32) => i32)[] = [((x: i32) => (w as i32) + x)]; return fs[0](36); }", 42},
		{"callarg-i32-capture-control", outcome + "function check(): i32 { let b: i32 = 42; return run((): Outcome => { return Fail(b); }); }\nfunction main(): i32 { return check(); }", 42},

		{"two-wide-captures", outcome + "function check(): i32 { let a: i64 = 20 as i64; let b: i64 = 22 as i64; return run((): Outcome => { return Fail((a + b) as i32); }); }\nfunction main(): i32 { return check(); }", 42},

		{"mixed-wide-narrow-pointer", outcome + "function check(): i32 { let a: i64 = 40 as i64; let n: i32 = 2; let s: string = \"xy\"; return run((): Outcome => { return Fail((a as i32) + n + s.len() - 2); }); }\nfunction main(): i32 { return check(); }", 42},

		{"wide-capture-declared-in-if-block", outcome + "function check(k: i32): i32 {\n    if (k > 0) { let a: i64 = 42 as i64; return run((): Outcome => { return Fail(a as i32); }); }\n    return 7;\n}\nfunction main(): i32 { return check(1); }", 42},

		{"wide-capture-read-twice", outcome + "function check(): i32 { let a: i64 = 21 as i64; return run((): Outcome => { return Fail((a + a) as i32); }); }\nfunction main(): i32 { return check(); }", 42},

		{"reassigned-wide-capture-stays-shared", outcome + "function main(): i32 { let a: i64 = 1 as i64; let g = (): Outcome => { return Fail(a as i32); }; a = 42 as i64; return run(g); }", 42},

		{"direct-called-i64-binding", "function main(): i32 { let base: i64 = 7000000000; let f = () => base + 2000000000; return (f() / 1000000000) as i32; }", 9},
		{"direct-called-f64-binding", "function main(): i32 { let d: f64 = 10.5; let f = () => d * 4.0; return f() as i32; }", 42},

		{"iife-i64-two-value-blocks", "function main(): i32 {\n    let v1: i64 = 40i64;\n    let q: i64 = (if (true) { 2i64 } else { 3i64 });\n    let r: i64 = (if (false) { 9i64 } else { (v1 | v1) });\n    return ((r + q) as i32) & 63i32;\n}", 42},
		{"iife-u64-two-value-blocks", "function main(): i32 {\n    let v1: u64 = 40u64;\n    let q: u64 = (if (true) { 2u64 } else { 3u64 });\n    let r: u64 = (if (false) { 9u64 } else { (v1 | v1) });\n    return ((r + q) as i32) & 63i32;\n}", 42},
		{"iife-i64-beside-closure-local", "function main(): i32 {\n    let v1: i64 = 40i64;\n    let g: () => i32 = (() => 2i32);\n    let r: i64 = (if (false) { 9i64 } else { (v1 | v1) });\n    return (((r as i32) + g()) & 63i32);\n}", 42},
		{"iife-match-i64-beside-closure-local", "enum S { A, B }\nfunction main(): i32 {\n    let v1: i64 = 40i64;\n    let g: () => i32 = (() => 2i32);\n    let r: i64 = (match (B) { A => 9i64, B => (v1 | v1) });\n    return (((r as i32) + g()) & 63i32);\n}", 42},
		{"iife-i32-two-value-blocks-control", "function main(): i32 {\n    let v1: i32 = 40i32;\n    let q: i32 = (if (true) { 2i32 } else { 3i32 });\n    let r: i32 = (if (false) { 9i32 } else { (v1 | v1) });\n    return (r + q) & 63i32;\n}", 42},
		{"iife-beside-escaping-closure", outcome + "function check(): i32 {\n    let b: i64 = 20 as i64;\n    let v: i64 = if (b > 4i64) { b | b } else { 7i64 };\n    return run((): Outcome => { return Fail((v + 22i64) as i32); });\n}\nfunction main(): i32 { return check(); }", 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			route := strings.TrimSpace(string(runCapture(t, gcc, runner, driverBin, src, "-decide")))
			if route != "ir" {
				t.Fatalf("%s routed %q, want \"ir\"", tc.name, route)
			}
			wat := runCapture(t, gcc, runner, driverBin, src)
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if werr := os.WriteFile(watPath, wat, 0o644); werr != nil {
				t.Fatalf("write wat: %v", werr)
			}
			cmd := exec.Command(wasmtime, "run", watPath)
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n%s", tc.name, code, tc.exit, out)
			}
		})
	}
}
