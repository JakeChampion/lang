package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// --- A spread copy is a counted holder of the pointer-element arrays it carries -
//
// `var z: P = P { ...q, … }` retains every array field it carries (#8983), and
// the copy releases them the way every other counted holder does: the rc-gated
// walk, per buffer and per element. What made that unsafe for a string[] /
// struct[] / enum[] carry was one copy that duplicated element pointers without
// a count — arr_push's un-share of a SHARED receiver, reached when a unique box
// appends through a field whose buffer another box also holds (`ls.bind(…)` on
// a `Scope { ...s }`) — and the value-form clone the `.append` / `.with`
// expressions take of a shared receiver. Both now retain the elements they
// copy (__fern_arr_inc_elems), so each buffer holds a count of each element
// and the last owner's walk frees them exactly once.
//
// Each case has a spread copy sharing a pointer-element buffer with its base
// and a mutation through one holder while the other is live. Every census must
// balance — allocs == frees at live_bytes 0 — on all three backends, the
// sanitizer must stay silent, and the exit is the interpreter's. Before the
// copy was credited at all, every case leaked the whole carried structure per
// round; credited without the retain, the enum cases free payloads under the
// other holder and the struct case double-frees.
//
// The carries here are enum[] and struct[]. A string[] field is not pinned
// because its rebind release is a gap of its own: `s = s.bind("a")` on a
// `names: string[]` field leaks two blocks a round with no spread anywhere,
// and the spread copy now measures exactly as the explicit field-share holder
// does (docs/rc-log/2026-09-10-spread-carry-elems-secured.md).

type spreadCarryElemsCase struct {
	name string
	src  string
}

const spreadCarryElemsMain = "\nfunction main(): i32 { var t: i32 = 0; var i: i32 = 0; " +
	"while (i < 100) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }\n"

var spreadCarryElemsCases = []spreadCarryElemsCase{
	// The #8983 shape with rc-boxed elements: the copy's box is unique, its
	// buffer is shared with the caller's, and the receiver append un-shares an
	// enum[] — twice, so the second append grows a buffer the first one already
	// copied. The payloads are read back on both sides afterwards.
	{"append_through_copy", `enum E { A(i32[]), B }
struct Sc { types: E[], depth: i32 }
function (s: Sc) bind(t: E): Sc { return Sc { types: s.types.append(t), depth: s.depth }; }
function lam(s: Sc, k: i32): i32 {
    var ls: Sc = Sc { ...s, depth: 0 };
    var i: i32 = 0;
    while (i < 2) { ls = ls.bind(E.A([k, i])); i = i + 1; }
    var v: i32 = 0;
    match (ls.types[0]) { E.A(xs) => { v = xs[0]; }, E.B => { v = 0 - 1; } }
    return ls.types.len() + v;
}
function round(i: i32): i32 {
    var s: Sc = Sc { types: [], depth: 1 };
    s = s.bind(E.A([i, 1]));
    s = s.bind(E.B);
    var r: i32 = lam(s, i + 3);
    var v: i32 = 0;
    match (s.types[0]) { E.A(xs) => { v = xs[0] + xs[1]; }, E.B => { v = 0 - 1; } }
    if (s.types.len() != 2) { return 0 - 1; }
    return (r + v) % 101;
}` + spreadCarryElemsMain},
	// The mutation goes through the BASE while the copy holds the buffer: the
	// base's box is unique, so its append un-shares the buffer the copy reads
	// back afterwards.
	{"append_through_base", `enum E { A(i32[]), B }
struct P { f: E[], n: i32 }
function (p: P) grow(k: i32): P { return P { f: p.f.append(E.A([k])), n: p.n + 1 }; }
function round(i: i32): i32 {
    var q: P = P { f: [E.A([i, 1])], n: 0 };
    var z: P = P { ...q, n: 5 };
    q = q.grow(i + 2);
    var v: i32 = 0;
    match (z.f[0]) { E.A(xs) => { v = xs[0] + xs[1]; }, E.B => { v = 0 - 1; } }
    return (q.f.len() + z.f.len() + v + q.n + z.n) % 101;
}` + spreadCarryElemsMain},
	// The value-form append is a clone too (arr_slice, then a push on the
	// copy), and it duplicated the elements the same way. A credited box
	// holding such a clone released the base's elements under it: with no
	// spread anywhere this row exits 99 on main's compiler, and the spread
	// form leaked instead only because the copy earned no release. `p` dies
	// in its branch and `q` reads its payload back after allocation churn.
	{"clone_override", `enum E { A(i32[]), B }
struct P { f: E[], n: i32 }
function churn(i: i32): i32 { var a: i32[] = [i, i + 1, i + 2, i + 3]; var b: i32[] = [i + 4, i + 5, i + 6, i + 7]; return a[0] + b[3]; }
function round(i: i32): i32 {
    var q: P = P { f: [E.A([i, 1])], n: 0 };
    var t: i32 = 0;
    if (i >= 0) { var p: P = P { f: q.f.append(E.B), n: 1 }; t = p.f.len(); }
    var junk: i32 = churn(i * 7 + 3);
    var v: i32 = 0;
    match (q.f[0]) { E.A(xs) => { v = xs[0] + xs[1]; }, E.B => { v = 0 - 1; } }
    if (v != i + 1) { return 0 - 50; }
    return (t + v + junk) % 101;
}` + spreadCarryElemsMain},
	{"clone_override_spread", `enum E { A(i32[]), B }
struct P { f: E[], n: i32 }
function churn(i: i32): i32 { var a: i32[] = [i, i + 1, i + 2, i + 3]; var b: i32[] = [i + 4, i + 5, i + 6, i + 7]; return a[0] + b[3]; }
function round(i: i32): i32 {
    var q: P = P { f: [E.A([i, 1])], n: 0 };
    var t: i32 = 0;
    if (i >= 0) { var p: P = P { ...q, f: q.f.append(E.B) }; t = p.f.len(); }
    var junk: i32 = churn(i * 7 + 3);
    var v: i32 = 0;
    match (q.f[0]) { E.A(xs) => { v = xs[0] + xs[1]; }, E.B => { v = 0 - 1; } }
    if (v != i + 1) { return 0 - 50; }
    return (t + v + junk) % 101;
}` + spreadCarryElemsMain},
	// `.with` through the copy over a struct[]: the in-place store takes a
	// clone of the shared buffer, retains its elements and hands back the
	// count of the one it replaces.
	{"with_through_copy", `struct In { v: i32 }
struct H { xs: In[], n: i32 }
function (h: H) set0(v: i32): H { return H { xs: h.xs.with(0, In { v: v }), n: h.n + 1 }; }
function round(i: i32): i32 {
    var q: H = H { xs: [In { v: i }, In { v: i + 1 }], n: 0 };
    var z: H = H { ...q, n: 5 };
    z = z.set0(i + 7);
    return (q.xs[0].v + z.xs[0].v + z.xs[1].v + z.n + q.n) % 101;
}` + spreadCarryElemsMain},
}

// spreadCarryElemsBalanced reads the census and requires it flat.
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
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range spreadCarryElemsCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureEnv(t, runner, driverBin, []byte(tc.src),
				[]string{"PATH=/usr/bin:/bin", "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1"}, "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, "sce_"+tc.name, string(asm))
			stderr, code := runCaptureStderrExit(t, runner, bin)
			if code != want {
				t.Fatalf("%s exited %d, want %d (interp oracle; 99 = rc underflow)", tc.name, code, want)
			}
			spreadCarryElemsBalanced(t, tc.name, stderr)
		})
	}
}

// The sanitizer leg: the quarantine and the over-release trap must stay
// silent — the copy now releases where it used to leak, so the direction to
// guard is a free too many.
func TestSelfHostSpreadCarryElemsSanitizeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range spreadCarryElemsCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureEnv(t, runner, driverBin, []byte(tc.src),
				[]string{"PATH=/usr/bin:/bin", "FERN_STRICT_IR=1", "FERN_SANITIZE=1"}, "-ir")
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
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

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
			spreadCarryElemsBalanced(t, tc.name, errBuf.String())
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
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range spreadCarryElemsCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			wat := wasmLcCompile(t, runner, driverBin, tc.src, []string{"FERN_STRICT_IR=1", "FERN_LEAKCHECK=1"})
			stderr, code := wasmLcRun(t, dir, "sce_"+tc.name, wat)
			if code != want {
				t.Fatalf("%s exited %d, want %d (interp oracle; 99 = rc underflow)", tc.name, code, want)
			}
			spreadCarryElemsBalanced(t, tc.name, stderr)
		})
	}
}
