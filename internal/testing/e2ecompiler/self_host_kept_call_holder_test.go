package e2ecompiler

import (
	"strings"
	"testing"
)

// --- A holder handed whole to a call, and a closure array's clone -----------
//
// `let result: M = M { items: mod.items, funcs: fresh }` takes a counted share
// of `mod.items`, which grants the holder its deep drop. That drop walks
// `result.funcs`, whose element boxes `rebuild(result)` hands back inside its
// own result, so the store of a handed-back borrow must be counted (#9021);
// appended uncounted, every generation-2 self-host binary died reading a freed
// FuncDecl body (#9016). The two holder rows keep the shape running under the
// sanitizer, directly and through an alias.
//
// The other two rows are the value-form `.with` / `.append` on a
// closure-element array, whose clone must retain its elements only where they
// are rc-headed boxes (#9017).
//
// All four are pinned by the exit code against the interpreter and by the
// sanitizer staying silent: a use-after-free or an over-release is a
// `fern-sanitizer:` line and exit 124, and the interpreter never reaches
// either. A leak line is not a finding here.

type keptCallHolderCase struct {
	name, src string
}

var keptCallHolderCases = []keptCallHolderCase{
	{"kept_call_holder", `enum E { A(i32), B }
struct F { body: E[], n: i32 }
struct M { funcs: F[], items: E[], k: i32 }
function touch(f: F): F { if (f.n < 0) { return F { body: [], n: 0 }; } return f; }
function stamp(fs: F[]): F[] {
    let out: F[] = [];
    let i: i32 = 0;
    while (i < fs.len()) { out = out.append(fs[i]); i = i + 1; }
    return out;
}
function rebuild(m: M): M {
    let fs: F[] = [];
    let i: i32 = 0;
    while (i < m.funcs.len()) { fs = fs.append(touch(m.funcs[i])); i = i + 1; }
    return M { ...m, funcs: fs };
}
@noinline function infer(m: M): M { return m; }
function lift(mod: M): M {
    let worklist: F[] = [];
    let i: i32 = 0;
    while (i < mod.funcs.len()) { let wf: F = mod.funcs[i]; worklist = worklist.append(wf); i = i + 1; }
    let newfuncs: F[] = [];
    let wi: i32 = 0;
    while (wi < worklist.len()) { let fd: F = worklist[wi]; newfuncs = newfuncs.append(fd); wi = wi + 1; }
    let result: M = M { funcs: stamp(newfuncs), items: mod.items, k: mod.k + 1 };
    return infer(rebuild(result));
}
function round(i: i32): i32 {
    let m: M = M { funcs: [F { body: [E.A(i), E.B], n: 1 }, F { body: [E.B], n: 2 }], items: [E.A(i + 1)], k: 0 };
    let r: M = lift(m);
    let v: i32 = 0;
    match (r.funcs[0].body[0]) { E.A(x) => { v = x; }, E.B => { v = 0 - 1; } }
    return (v + r.funcs[1].n + r.funcs[0].body.len() + r.items.len() + r.k + m.k) % 101;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 50) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }
`},
	// The holder handed on through an alias: `let alias = result` is the same
	// box, so `infer(alias)` keeps it exactly as `infer(result)` would.
	{"kept_call_alias", `enum E { A(i32), B }
struct F { body: E[], n: i32 }
struct M { funcs: F[], items: E[], k: i32 }
function touch(f: F): F { if (f.n < 0) { return F { body: [], n: 0 }; } return f; }
function rebuild(m: M): M {
    let fs: F[] = [];
    let i: i32 = 0;
    while (i < m.funcs.len()) { fs = fs.append(touch(m.funcs[i])); i = i + 1; }
    return M { ...m, funcs: fs };
}
@noinline function infer(m: M): M { return m; }
function lift(mod: M): M {
    let fresh: F[] = [];
    let i: i32 = 0;
    while (i < mod.funcs.len()) { fresh = fresh.append(touch(mod.funcs[i])); i = i + 1; }
    let result: M = M { funcs: fresh, items: mod.items, k: mod.k + 1 };
    let alias: M = result;
    return infer(rebuild(alias));
}
function round(i: i32): i32 {
    let m: M = M { funcs: [F { body: [E.A(i), E.B], n: 1 }, F { body: [E.B], n: 2 }], items: [E.A(i + 1)], k: 0 };
    let r: M = lift(m);
    let v: i32 = 0;
    match (r.funcs[0].body[0]) { E.A(x) => { v = x; }, E.B => { v = 0 - 1; } }
    return (v + r.funcs[1].n + r.funcs[0].body.len() + r.items.len() + r.k + m.k) % 101;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 50) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }
`},
	// The closure array reached through a struct field. The elements are read
	// into locals before the call.
	{"closure_field_with", `struct H { hs: ((i32) => i32)[], n: i32 }
function inc(x: i32): i32 { return x + 7; }
function dec(x: i32): i32 { return x - 1; }
function round(i: i32): i32 {
    let h: H = H { hs: [inc, inc, dec], n: i };
    let keep: H = h;
    let w: ((i32) => i32)[] = h.hs.with(1, dec);
    let a: ((i32) => i32)[] = h.hs.append(inc);
    let f: (i32) => i32 = w[1];
    let g: (i32) => i32 = a[3];
    let k: (i32) => i32 = keep.hs[2];
    return (f(i) + g(i) + k(i) + a.len() + h.n) % 101;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 50) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }
`},
	{"closure_array_with", `function round(i: i32): i32 {
    let v1: (i32) => i32 = ((x: i32) => x + 7);
    let s0: ((i32) => i32)[] = [v1, v1, ((y: i32) => y - 1), v1];
    let a0: ((i32) => i32)[] = s0;
    let w: ((i32) => i32)[] = s0.with(1, ((z: i32) => z + 10));
    let a: ((i32) => i32)[] = w.append(v1);
    return (s0[1](i) + w[1](i) + a[4](i) + a0[2](i) + a.len()) % 101;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 50) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }
`},
}

// emittedFn returns the asm text of one emitted function: from its label to
// the next `__fn_` label.
func emittedFn(t *testing.T, asm, fn string) string {
	t.Helper()
	label := "__fn_" + fn
	if !strings.Contains(asm, "\n"+label+":\n") {
		t.Fatalf("emitted asm has no %s:", label)
	}
	return functionListing(asm, label)
}

// The x86-64 leg runs each row: the exit is the interpreter's under the
// sanitizer, which stays silent.
func TestSelfHostKeptCallHolderX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range keptCallHolderCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureEnv(t, runner, driverBin, []byte(tc.src),
				[]string{"PATH=/usr/bin:/bin", "FERN_STRICT_IR=1", "FERN_SANITIZE=1"})
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, "kch_"+tc.name, string(asm))
			stderr, code := runCaptureStderrExit(t, runner, bin)
			if code != want {
				t.Fatalf("%s exited %d under the sanitizer, want %d (interp oracle; 124 = fatal sanitizer check, 99 = rc underflow)", tc.name, code, want)
			}
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "fern-sanitizer:") && !strings.HasPrefix(line, "fern-sanitizer: leak") {
					t.Errorf("%s: %s", tc.name, line)
				}
			}
		})
	}
}
