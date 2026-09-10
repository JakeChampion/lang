package e2eselfhost

import (
	"strings"
	"testing"
)

// --- A counted-share holder handed whole to a call keeps its box-only release -
//
// `var result: M = M { items: mod.items, funcs: fresh }` takes a counted share
// of `mod.items`, and the bind-site flip (mark_enum_arr_share) then grants the
// holder its deep drop. That drop walks `result.funcs`, a fresh buffer whose
// element boxes came out of another array by an element read — a move, no
// count — and the callee `rebuild(result)` hands them back inside its own
// result. The walk freed them under that result: every generation-2 self-host
// binary died in fn_sigs_for_borrow reading a FuncDecl body (#9016). The flip
// now leaves the holder box-only when a call keeps it (handed_to_kept_call).
//
// The second case is the value-form `.with` / `.append` on a closure-element
// array: the clone retained its elements through __fern_arr_inc_elems, and a
// closure element is a code address, not an rc-headed box (#9017).
//
// Both are pinned by the exit code against the interpreter and by the
// sanitizer staying silent: a use-after-free or an over-release is a
// `fern-sanitizer:` line and exit 124, and the interpreter never reaches
// either. A leak line is not a finding here: the holder's box-only release is
// the sound role, and what it leaves behind is the price of the alias.

type keptCallHolderCase struct {
	name string
	// fn is the function whose emitted asm must not carry `absent`.
	fn, absent string
	src        string
}

var keptCallHolderCases = []keptCallHolderCase{
	{"kept_call_holder", "lift", "__struct_drop_M", `enum E { A(i32), B }
struct F { body: E[], n: i32 }
struct M { funcs: F[], items: E[], k: i32 }
function touch(f: F): F { if (f.n < 0) { return F { body: [], n: 0 }; } return f; }
function stamp(fs: F[]): F[] {
    var out: F[] = [];
    var i: i32 = 0;
    while (i < fs.len()) { out = out.append(fs[i]); i = i + 1; }
    return out;
}
function rebuild(m: M): M {
    var fs: F[] = [];
    var i: i32 = 0;
    while (i < m.funcs.len()) { fs = fs.append(touch(m.funcs[i])); i = i + 1; }
    return M { ...m, funcs: fs };
}
function infer(m: M): M { return m; }
function lift(mod: M): M {
    var worklist: F[] = [];
    var i: i32 = 0;
    while (i < mod.funcs.len()) { var wf: F = mod.funcs[i]; worklist = worklist.append(wf); i = i + 1; }
    var newfuncs: F[] = [];
    var wi: i32 = 0;
    while (wi < worklist.len()) { var fd: F = worklist[wi]; newfuncs = newfuncs.append(fd); wi = wi + 1; }
    var result: M = M { funcs: stamp(newfuncs), items: mod.items, k: mod.k + 1 };
    return infer(rebuild(result));
}
function round(i: i32): i32 {
    var m: M = M { funcs: [F { body: [E.A(i), E.B], n: 1 }, F { body: [E.B], n: 2 }], items: [E.A(i + 1)], k: 0 };
    var r: M = lift(m);
    var v: i32 = 0;
    match (r.funcs[0].body[0]) { E.A(x) => { v = x; }, E.B => { v = 0 - 1; } }
    return (v + r.funcs[1].n + r.funcs[0].body.len() + r.items.len() + r.k + m.k) % 101;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 50) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }
`},
	{"closure_array_with", "round", "__fern_arr_inc_elems", `function round(i: i32): i32 {
    var v1: (i32) => i32 = ((x: i32) => x + 7);
    var s0: ((i32) => i32)[] = [v1, v1, ((y: i32) => y - 1), v1];
    var a0: ((i32) => i32)[] = s0;
    var w: ((i32) => i32)[] = s0.with(1, ((z: i32) => z + 10));
    var a: ((i32) => i32)[] = w.append(v1);
    return (s0[1](i) + w[1](i) + a[4](i) + a0[2](i) + a.len()) % 101;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 50) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }
`},
}

// emittedFn returns the asm text of one emitted function: from its label to
// the next `__fn_` label.
func emittedFn(t *testing.T, asm, fn string) string {
	t.Helper()
	label := "\n__fn_" + fn + ":\n"
	i := strings.Index(asm, label)
	if i < 0 {
		t.Fatalf("emitted asm has no %s", strings.TrimSpace(label))
	}
	body := asm[i+len(label):]
	if j := strings.Index(body, "\n__fn_"); j >= 0 {
		body = body[:j]
	}
	return body
}

// The x86-64 leg pins the emit and runs the result: the named function must
// not carry the release or the retain the bug emitted, and the exit is the
// interpreter's under the sanitizer, which stays silent.
func TestSelfHostKeptCallHolderX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range keptCallHolderCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureEnv(t, runner, driverBin, []byte(tc.src),
				[]string{"PATH=/usr/bin:/bin", "FERN_STRICT_IR=1", "FERN_SANITIZE=1"}, "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			if n := strings.Count(emittedFn(t, string(asm), tc.fn), tc.absent); n != 0 {
				t.Errorf("%s: %s carries %d call(s) of %s, want none", tc.name, tc.fn, n, tc.absent)
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
