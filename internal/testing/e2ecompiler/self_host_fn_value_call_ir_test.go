package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var fnValueCallCases = []struct {
	name string
	src  string
}{
	{"fnfield_f64", `struct H { f: () => f64 }
function mkval(): f64 { return 4.5; }
function main(): i32 {
    let h: H = H { f: mkval };
    return (h.f() * 10.0) as i32;
}`},
	{"fnfield_f64_local", `struct H { f: () => f64 }
function mkval(): f64 { return 4.5; }
function main(): i32 {
    let h: H = H { f: mkval };
    let v: f64 = h.f();
    return (v * 10.0) as i32;
}`},
	{"fnfield_i64", `struct H { f: () => i64 }
function mkval(): i64 { return 7000000045i64; }
function main(): i32 {
    let h: H = H { f: mkval };
    return (h.f() % 1000i64) as i32;
}`},
	{"fnfield_i32", `struct H { f: () => i32 }
function mkval(): i32 { return 45; }
function main(): i32 {
    let h: H = H { f: mkval };
    return h.f();
}`},

	{"fnarray_f64", `function mkval(): f64 { return 4.5; }
function main(): i32 {
    let fs: (() => f64)[] = [mkval];
    return (fs[0]() * 10.0) as i32;
}`},
	{"fnarray_f64_local", `function mkval(): f64 { return 4.5; }
function main(): i32 {
    let fs: (() => f64)[] = [mkval];
    let v: f64 = fs[0]();
    return (v * 10.0) as i32;
}`},
	{"fnarray_i64", `function mkval(): i64 { return 7000000045i64; }
function main(): i32 {
    let fs: (() => i64)[] = [mkval];
    return (fs[0]() % 1000i64) as i32;
}`},
	{"fnarray_i32", `function mkval(): i32 { return 45; }
function main(): i32 {
    let fs: (() => i32)[] = [mkval];
    return fs[0]();
}`},
	{"fnarray_closure_f64", `function main(): i32 {
    let fs: (() => f64)[] = [(): f64 => { return 4.5; }];
    return (fs[0]() * 10.0) as i32;
}`},

	{"fntuple_f64", `function mkval(): f64 { return 4.5; }
function main(): i32 {
    let t: (() => f64, i32) = (mkval, 1);
    return (t.0() * 10.0) as i32;
}`},
	{"fntuple_f64_local", `function mkval(): f64 { return 4.5; }
function main(): i32 {
    let t: (() => f64, i32) = (mkval, 1);
    let v: f64 = t.0();
    return (v * 10.0) as i32;
}`},
	{"fntuple_i64", `function mkval(): i64 { return 7000000045i64; }
function main(): i32 {
    let t: (() => i64, i32) = (mkval, 1);
    return (t.0() % 1000i64) as i32;
}`},
	{"fntuple_i32", `function mkval(): i32 { return 45; }
function main(): i32 {
    let t: (() => i32, i32) = (mkval, 1);
    return t.0();
}`},

	{"fnlocal_annotated_f64", `function mkval(): f64 { return 4.5; }
function main(): i32 {
    let f: () => f64 = mkval;
    return (f() * 10.0) as i32;
}`},
	{"fnlocal_annotated_i64", `function mkval(): i64 { return 7000000045i64; }
function main(): i32 {
    let f: () => i64 = mkval;
    return (f() % 1000i64) as i32;
}`},
	{"fnlocal_annotated_arg_f64", `function scale(x: f64): f64 { return x * 10.0; }
function main(): i32 {
    let f: (f64) => f64 = scale;
    return f(4.5) as i32;
}`},

	{"fnfield_arg_f64", `struct H { f: (f64) => f64 }
function scale(x: f64): f64 { return x * 10.0; }
function main(): i32 {
    let h: H = H { f: scale };
    return h.f(4.5) as i32;
}`},
	{"fnfield_arg_mixed", `struct H { f: (i64, f64) => f64 }
function comb(a: i64, x: f64): f64 { return x * (a as f64); }
function main(): i32 {
    let h: H = H { f: comb };
    return h.f(10i64, 4.5) as i32;
}`},
	{"fnfield_arg_i64_ret", `struct H { f: (i64) => i64 }
function id64(x: i64): i64 { return x; }
function main(): i32 {
    let h: H = H { f: id64 };
    return h.f(45i64) as i32;
}`},
	{"fnfield_arg_i32_to_i64", `struct H { f: (i32) => i64 }
function widen(x: i32): i64 { return x as i64; }
function main(): i32 {
    let h: H = H { f: widen };
    return h.f(45) as i32;
}`},
	{"fnfield_arg_string", `struct H { f: (string) => i32 }
function slen(s: string): i32 { return s.len(); }
function main(): i32 {
    let h: H = H { f: slen };
    return h.f("x") + 44;
}`},
	{"fnfield_arg_i32", `struct H { f: (i32) => i32 }
function add5(x: i32): i32 { return x + 5; }
function main(): i32 {
    let h: H = H { f: add5 };
    return h.f(40);
}`},

	{"fntuple_arg_f64", `function scale(x: f64): f64 { return x * 10.0; }
function main(): i32 {
    let t: ((f64) => f64, i32) = (scale, 1);
    return t.0(4.5) as i32;
}`},
	{"fntuple_arg_mixed", `function comb(a: i64, x: f64): f64 { return x * (a as f64); }
function main(): i32 {
    let t: ((i64, f64) => f64, i32) = (comb, 1);
    return t.0(10i64, 4.5) as i32;
}`},
	{"fntuple_arg_i64_ret", `function id64(x: i64): i64 { return x; }
function main(): i32 {
    let t: ((i64) => i64, i32) = (id64, 1);
    return t.0(45i64) as i32;
}`},
	{"fntuple_arg_i32", `function add5(x: i32): i32 { return x + 5; }
function main(): i32 {
    let t: ((i32) => i32, i32) = (add5, 1);
    return t.0(40);
}`},
	{"fntuple_arg_string", `function slen(s: string): i32 { return s.len(); }
function main(): i32 {
    let t: ((string) => i32, i32) = (slen, 1);
    return t.0("x") + 44;
}`},

	{"fnarray_arg_f64", `function scale(x: f64): f64 { return x * 10.0; }
function main(): i32 {
    let fs: ((f64) => f64)[] = [scale];
    return fs[0](4.5) as i32;
}`},
	{"fnarray_arg_mixed", `function comb(a: i64, x: f64): f64 { return x * (a as f64); }
function main(): i32 {
    let fs: ((i64, f64) => f64)[] = [comb];
    return fs[0](10i64, 4.5) as i32;
}`},
	{"fnarray_arg_i64_ret", `function id64(x: i64): i64 { return x; }
function main(): i32 {
    let fs: ((i64) => i64)[] = [id64];
    return fs[0](45i64) as i32;
}`},
	{"fnarray_arg_f64_to_i32", `function pick(x: f64): i32 { return (x as i32) + 1; }
function main(): i32 {
    let fs: ((f64) => i32)[] = [pick];
    return fs[0](44.5);
}`},
	{"fnarray_arg_i32", `function add5(x: i32): i32 { return x + 5; }
function main(): i32 {
    let fs: ((i32) => i32)[] = [add5];
    return fs[0](40);
}`},
	{"fnarray_arg_string", `function slen(s: string): i32 { return s.len(); }
function main(): i32 {
    let fs: ((string) => i32)[] = [slen];
    return fs[0]("x") + 44;
}`},
	{"fnarray_arg_alias", `function scale(x: f64): f64 { return x * 10.0; }
function main(): i32 {
    let fs: ((f64) => f64)[] = [scale];
    let xs = fs;
    return xs[0](4.5) as i32;
}`},
	{"fnarray_arg_closure_f64", `function main(): i32 {
    let k: f64 = 10.0;
    let fs: ((f64) => f64)[] = [(x: f64) => x * k];
    return fs[0](4.5) as i32;
}`},
	{"fnarrayfield_arg_f64", `struct Reg { hs: ((f64) => f64)[] }
function scale(x: f64): f64 { return x * 10.0; }
function main(): i32 {
    let r: Reg = Reg { hs: [scale] };
    return r.hs[0](4.5) as i32;
}`},

	{"fnretcall_f64", `function mk(): () => f64 { return () => 4.5; }
function main(): i32 {
    return (mk()() * 10.0) as i32;
}`},
	{"fnretcall_arg_f64", `function mk(): (f64) => f64 { return (x: f64) => x * 10.0; }
function main(): i32 {
    return mk()(4.5) as i32;
}`},
	{"fnretcall_arg_mixed", `function mk(): (i64, f64) => f64 { return (a: i64, x: f64) => x * (a as f64); }
function main(): i32 {
    return mk()(10i64, 4.5) as i32;
}`},
	{"fnretcall_i64", `function mk(): () => i64 { return () => 7000000045i64; }
function main(): i32 {
    return (mk()() % 1000i64) as i32;
}`},
	{"fnretcall_i32", `function mk(): () => i32 { return () => 45; }
function main(): i32 {
    return mk()();
}`},
	{"fnretcall_arg_i32", `function mk(): (i32) => i32 { return (x: i32) => x + 5; }
function main(): i32 {
    return mk()(40);
}`},
	{"fnretcall_closure_f64", `function mk(k: f64): (f64) => f64 { return (x: f64) => x * k; }
function main(): i32 {
    return mk(10.0)(4.5) as i32;
}`},

	{"fnfield_generic_wide", `struct Box[T] { f: (T) => T }
function id64(x: i64): i64 { return x; }
function main(): i32 {
    let b: Box[i64] = Box[i64] { f: id64 };
    return b.f(45i64) as i32;
}`},
	{"fnfield_generic_i32", `struct Box[T] { f: (T) => T }
function id32(x: i32): i32 { return x; }
function main(): i32 {
    let b: Box[i32] = Box[i32] { f: id32 };
    return b.f(45);
}`},
	{"fnfield_generic_wide_field_arg", `struct Box[T] { f: (T) => T, seed: T }
function id64(x: i64): i64 { return x; }
function main(): i32 {
    let b: Box[i64] = Box[i64] { f: id64, seed: 45i64 };
    return b.f(b.seed) as i32;
}`},
}

func runFnValueCallX86_64(t *testing.T, gcc string, runner []string, fernBin, stdlibRoot, name, src string, want int) {
	t.Helper()
	proj := t.TempDir()
	mainPath := filepath.Join(proj, "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	asmPath := filepath.Join(proj, "out.s")
	if out, cerr := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asmPath, mainPath, stdlibRoot).CombinedOutput(); cerr != nil {
		t.Fatalf("compile: %v (%s)", cerr, out)
	}
	binPath := filepath.Join(proj, "out.bin")
	if out, lerr := exec.Command(gcc, "-nostdlib", "-static", "-o", binPath, asmPath).CombinedOutput(); lerr != nil {
		t.Fatalf("link: %v (%s)", lerr, out)
	}
	rcmd := runX86_64Bin(runner, binPath)
	_ = rcmd.Run()
	if got := rcmd.ProcessState.ExitCode(); got != want {
		t.Errorf("%s = %d, want %d (interp oracle) — a call through a fn value must carry the callee's result type", name, got, want)
	}
}

func TestSelfHostFnValueCallX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range fnValueCallCases {
		t.Run(tc.name, func(t *testing.T) {
			runFnValueCallX86_64(t, gcc, runner, fernBin, stdlibRoot, tc.name, tc.src, interpExit(t, interpBin, tc.src))
		})
	}
}

func TestSelfHostFnValueCallWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping fn-value call wasm cases")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range fnValueCallCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			outWat := filepath.Join(proj, "out.wat")
			var stderr strings.Builder
			cmd := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", "-o", outWat, mainPath, stdlibRoot)
			cmd.Stderr = &stderr
			if cerr := cmd.Run(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, stderr.String())
			}
			rcmd := exec.Command("wasmtime", "run", outWat)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle) — the funcref type must name the callee's real signature", tc.name, got, want)
			}
		})
	}
}
