package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"
)

type spreadCarryElemsCase struct {
	name  string
	src   string
	leaks bool
}

const spreadCarryElemsMain = "\nfunction main(): i32 { let t: i32 = 0; let i: i32 = 0; " +
	"while (i < 100) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }\n"

var spreadCarryElemsCases = []spreadCarryElemsCase{
	{name: "append_through_copy", src: `enum E { A(i32[]), B }
struct Sc { types: E[], depth: i32 }
function (s: Sc) bind(t: E): Sc { return Sc { types: s.types.append(t), depth: s.depth }; }
function lam(s: Sc, k: i32): i32 {
    let ls: Sc = Sc { ...s, depth: 0 };
    let i: i32 = 0;
    while (i < 2) { ls = ls.bind(E.A([k, i])); i = i + 1; }
    let v: i32 = 0;
    match (ls.types[0]) { E.A(xs) => { v = xs[0]; }, E.B => { v = 0 - 1; } }
    return ls.types.len() + v;
}
function round(i: i32): i32 {
    let s: Sc = Sc { types: [], depth: 1 };
    s = s.bind(E.A([i, 1]));
    s = s.bind(E.B);
    let r: i32 = lam(s, i + 3);
    let v: i32 = 0;
    match (s.types[0]) { E.A(xs) => { v = xs[0] + xs[1]; }, E.B => { v = 0 - 1; } }
    if (s.types.len() != 2) { return 0 - 1; }
    return (r + v) % 101;
}` + spreadCarryElemsMain},
	{name: "append_through_base", src: `enum E { A(i32[]), B }
struct P { f: E[], n: i32 }
function (p: P) grow(k: i32): P { return P { f: p.f.append(E.A([k])), n: p.n + 1 }; }
function round(i: i32): i32 {
    let q: P = P { f: [E.A([i, 1])], n: 0 };
    let z: P = P { ...q, n: 5 };
    q = q.grow(i + 2);
    let v: i32 = 0;
    match (z.f[0]) { E.A(xs) => { v = xs[0] + xs[1]; }, E.B => { v = 0 - 1; } }
    return (q.f.len() + z.f.len() + v + q.n + z.n) % 101;
}` + spreadCarryElemsMain},
	{name: "clone_override", src: `enum E { A(i32[]), B }
struct P { f: E[], n: i32 }
function churn(i: i32): i32 { let a: i32[] = [i, i + 1, i + 2, i + 3]; let b: i32[] = [i + 4, i + 5, i + 6, i + 7]; return a[0] + b[3]; }
function round(i: i32): i32 {
    let q: P = P { f: [E.A([i, 1])], n: 0 };
    let t: i32 = 0;
    if (i >= 0) { let p: P = P { f: q.f.append(E.B), n: 1 }; t = p.f.len(); }
    let junk: i32 = churn(i * 7 + 3);
    let v: i32 = 0;
    match (q.f[0]) { E.A(xs) => { v = xs[0] + xs[1]; }, E.B => { v = 0 - 1; } }
    if (v != i + 1) { return 0 - 50; }
    return (t + v + junk) % 101;
}` + spreadCarryElemsMain},
	{name: "clone_override_spread", src: `enum E { A(i32[]), B }
struct P { f: E[], n: i32 }
function churn(i: i32): i32 { let a: i32[] = [i, i + 1, i + 2, i + 3]; let b: i32[] = [i + 4, i + 5, i + 6, i + 7]; return a[0] + b[3]; }
function round(i: i32): i32 {
    let q: P = P { f: [E.A([i, 1])], n: 0 };
    let t: i32 = 0;
    if (i >= 0) { let p: P = P { ...q, f: q.f.append(E.B) }; t = p.f.len(); }
    let junk: i32 = churn(i * 7 + 3);
    let v: i32 = 0;
    match (q.f[0]) { E.A(xs) => { v = xs[0] + xs[1]; }, E.B => { v = 0 - 1; } }
    if (v != i + 1) { return 0 - 50; }
    return (t + v + junk) % 101;
}` + spreadCarryElemsMain},
	{name: "handback_elem_append", src: `struct F { n: i32, xs: i32[] }
struct M { funcs: F[] }
struct H { fs: F[], k: i32 }
function churn(i: i32): i32 { let a: i32[] = [i, i + 1, i + 2, i + 3]; let b: i32[] = [i + 4, i + 5, i + 6, i + 7]; return a[0] + b[3]; }
function pass(f: F): F { if (f.n < 0) { return F { n: 0, xs: [] }; } return f; }
function copy_all(m: M): F[] {
    let fs: F[] = [];
    let i: i32 = 0;
    while (i < m.funcs.len()) { fs = fs.append(pass(m.funcs[i])); i = i + 1; }
    return fs;
}
function round(i: i32): i32 {
    let m: M = M { funcs: [F { n: i, xs: [i] }, F { n: i + 1, xs: [i, i + 2] }] };
    let t: i32 = 0;
    if (i >= 0) { let h: H = H { fs: copy_all(m), k: 1 }; t = h.fs.len() + h.fs[1].xs[1]; }
    let junk: i32 = churn(i * 7 + 3);
    let v: i32 = m.funcs[1].xs[1] + m.funcs[0].n;
    if (v != i + i + 2) { return 0 - 50; }
    return (t + v + junk) % 101;
}` + spreadCarryElemsMain},
	{name: "view_of_borrowed_elems", src: `enum E { A(i32[]), B }
struct F { n: i32, xs: i32[] }
struct S { k: i32 }
struct M { funcs: F[], structs: S[], tops: E[], tags: i32[] }
function churn(i: i32): i32 { let a: i32[] = [i, i + 1, i + 2, i + 3]; let b: i32[] = [i + 4, i + 5, i + 6, i + 7]; return a[0] + b[3]; }
function view_of(mods: M[]): i32 {
    let funcs: F[] = [];
    let structs: S[] = [];
    for mod in mods {
        for fd in mod.funcs { funcs = funcs.append(fd); }
        for sd in mod.structs { structs = structs.append(sd); }
    }
    let view = M { ...mods[0], funcs: funcs, structs: structs };
    return view.funcs.len() + view.funcs[2].n + view.structs.len();
}
function round(i: i32): i32 {
    let a: M = M { funcs: [F { n: i, xs: [i] }, F { n: i + 1, xs: [i, i] }], structs: [S { k: 1 }], tops: [E.B], tags: [i] };
    let b: M = M { funcs: [F { n: i + 2, xs: [] }], structs: [], tops: [E.A([i])], tags: [] };
    let mods: M[] = [a, b];
    let t: i32 = view_of(mods);
    let junk: i32 = churn(i * 7 + 3);
    let v: i32 = mods[0].funcs[1].xs[1] + mods[1].funcs[0].n;
    if (v != i + i + 2) { return 0 - 50; }
    return (t + v + junk) % 101;
}` + spreadCarryElemsMain, leaks: true},
	{name: "with_on_closure_array", src: `function round(i: i32): i32 {
    let v1: (i32) => i32 = (x: i32): i32 => x + i;
    let xs: ((i32) => i32)[] = [v1, v1, (x: i32): i32 => x * 2];
    let ys: ((i32) => i32)[] = xs;
    let ws: ((i32) => i32)[] = xs.with(1, (x: i32): i32 => x + 10);
    let f: (i32) => i32 = ys[1];
    return (f(1) + ws[1](1) + xs[2](3)) % 101;
}` + spreadCarryElemsMain, leaks: true},
	{name: "param_elems_appended", src: `struct F { n: i32, xs: i32[] }
struct M { funcs: F[], name: string }
function churn(i: i32): i32 { let a: i32[] = [i, i + 1, i + 2, i + 3]; let b: i32[] = [i + 4, i + 5, i + 6, i + 7]; return a[0] + b[3]; }
function picked(m: M): F[] {
    let out: F[] = [];
    let k: i32 = 0;
    while (k < m.funcs.len()) { let fd: F = m.funcs[k]; if (fd.n % 2 == 0) { out = out.append(fd); } k = k + 1; }
    for f in m.funcs { out = out.append(f); }
    return out;
}
function round(i: i32): i32 {
    let a: M = M { funcs: [F { n: 2, xs: [i] }, F { n: 3, xs: [i, i] }], name: "a" };
    let t: i32 = 0;
    if (i >= 0) { let ps: F[] = picked(a); t = ps.len() + ps[0].xs[0]; }
    let junk: i32 = churn(i * 7 + 3);
    let v: i32 = a.funcs[1].xs[1] + a.funcs[0].n;
    if (v != i + 2) { return 0 - 50; }
    return (t + v + junk) % 101;
}` + spreadCarryElemsMain},
	{name: "with_through_copy", src: `struct In { v: i32 }
struct H { xs: In[], n: i32 }
function (h: H) set0(v: i32): H { return H { xs: h.xs.with(0, In { v: v }), n: h.n + 1 }; }
function round(i: i32): i32 {
    let q: H = H { xs: [In { v: i }, In { v: i + 1 }], n: 0 };
    let z: H = H { ...q, n: 5 };
    z = z.set0(i + 7);
    return (q.xs[0].v + z.xs[0].v + z.xs[1].v + z.n + q.n) % 101;
}` + spreadCarryElemsMain},
}

func spreadCarryElemsBalanced(t *testing.T, name, stderr string) {
	t.Helper()
	allocs, frees, live := parseLeakcheck(t, name, stderr)
	if allocs == 0 {
		t.Fatalf("%s: allocs=0 — the probe exercised no allocation", name)
	}
	if allocs != frees || live != 0 {
		t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — must balance at live_bytes 0", name, allocs, frees, live)
	}
}

func TestSelfHostSpreadCarryElemsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range spreadCarryElemsCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureEnv(t, runner, driverBin, []byte(tc.src),
				[]string{"PATH=/usr/bin:/bin", "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1"})
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, "sce_"+tc.name, string(asm))
			stderr, code := runCaptureStderrExit(t, runner, bin)
			if code != want {
				t.Fatalf("%s exited %d, want %d (interp oracle; 99 = rc underflow)", tc.name, code, want)
			}
			if !tc.leaks {
				spreadCarryElemsBalanced(t, tc.name, stderr)
			}
		})
	}
}

func TestSelfHostSpreadCarryElemsSanitizeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range spreadCarryElemsCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureEnv(t, runner, driverBin, []byte(tc.src),
				[]string{"PATH=/usr/bin:/bin", "FERN_STRICT_IR=1", "FERN_SANITIZE=1"})
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, "scesan_"+tc.name, string(asm))
			stderr, code := runCaptureStderrExit(t, runner, bin)
			if code != want {
				t.Fatalf("%s exited %d under the sanitizer, want %d (interp oracle; 124 = fatal sanitizer check)", tc.name, code, want)
			}
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "fern-sanitizer:") && !strings.HasPrefix(line, "fern-sanitizer: leak") {
					t.Errorf("%s: %s", tc.name, line)
				}
			}
		})
	}
}

func TestSelfHostSpreadCarryElemsArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range spreadCarryElemsCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureEnv(t, x86runner, driverBin, []byte(tc.src),
				[]string{"PATH=/usr/bin:/bin", "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1"}, "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, "sce_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			var errBuf strings.Builder
			cmd.Stderr = &errBuf
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Fatalf("%s exited %d, want %d (interp oracle; 99 = rc underflow)", tc.name, code, want)
			}
			if !tc.leaks {
				spreadCarryElemsBalanced(t, tc.name, errBuf.String())
			}
		})
	}
}

func TestSelfHostSpreadCarryElemsWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm spread-carry e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range spreadCarryElemsCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			wat := wasmLcCompile(t, runner, driverBin, tc.src, []string{"FERN_STRICT_IR=1", "FERN_LEAKCHECK=1"})
			stderr, code := wasmLcRun(t, dir, "sce_"+tc.name, wat)
			if code != want {
				t.Fatalf("%s exited %d, want %d (interp oracle; 99 = rc underflow)", tc.name, code, want)
			}
			if !tc.leaks {
				spreadCarryElemsBalanced(t, tc.name, stderr)
			}
		})
	}
}
