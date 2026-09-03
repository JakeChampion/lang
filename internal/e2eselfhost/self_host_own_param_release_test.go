package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- `own` struct params: released at exit, donated with string / enum fields
// (#5342) -------------------------------------------------------------------
//
// Four leaks on one shape family, measured on x86-64 at 100 rounds
// (allocs/frees/live_bytes; native was clean on every row):
//
//	bump(p: P): P { return P { ...p, n: p.n + 1 }; }     P { s: string, n: i32 }
//	  called as bump(P { s: w(i), n: i })                 400/100  live 16800
//	bump(own p: P): P { … the same … }                    300/100  live 12000
//	bumpq(own p: Q): Q { … }, Q { e: E, n: i32 }         300/0    live 13600
//	bump(own p: N): N { … }, N { m: i32, n: i32 }        100/100  (reuse fired)
//
// Every row was the caller never releasing `q`: a call result built by a
// functional update `T { ...base, … }` earned no strict-fresh credit, though
// every field the spread CARRIES reaches the new box counted (the base copy
// retains a nested-struct or enum field, and a string field where the type
// ROUTES field reclaim; a scalar carries nothing).
// return_value_is_strictfresh_struct now admits it on exactly that condition
// (spread_carried_fields_counted), and a bare `return p` of an `own` param on
// the frame-fresh terms (own_param_ret_is_frame_fresh).
//
// The borrowed row's argument TEMP still leaks — see borrowed_spread_string for
// why widening stash_fresh_struct_arg to reach it is unsound today.
//
// The enum row's callee never released `p` at all: reuse was refused for the
// enum field and a param was never exit-swept. own_struct_param_release_rows_of
// now credits an `own` struct param the frame still holds at exit ("OWNREL:" —
// deep; "OWNRELB:" — box-only where a field may have been copied out
// uncounted), and the own-update family admits enum fields.
//
// Two bugs surfaced under the new coverage and are pinned here too:
//
//   - borrowable_params_of marked an `own` param borrowable whenever the body
//     did not "consume" it, so the caller stashed and FREED a fresh argument the
//     callee had taken — a double ownership the scalar control masked only
//     because `q` was never released. An `own` position is a move.
//   - the own-update family admitted a string override on a type that does
//     not ROUTE field reclaim; the reuse arm then freed the old string while
//     the caller's construction — whose retain is gated on routing — had
//     taken no share. `unrouted_string_own_update` exited 77 on the parent
//     with allocation churn between the free and the read. FERN_SANITIZE
//     reported nothing: the quarantine stops the recycling that exposes it.
//
// Every row is gated on `__rc_underflow_count()` and runs a second leg under
// FERN_SANITIZE=1. Every want was confirmed against BOTH oracles: `bin/fern
// -interp` and the native x86-64 backend agreed on each.
type ownParamReleaseCase struct {
	name      string
	src       string
	want      int
	balance   bool
	wantFrees int64 // asserted exactly on every row that does not set balance
}

const ownParamReleaseHead = `import "std/i32";
struct P { s: string, n: i32 }
@noinline
function w(i: i32): string { return "s-a-wide-payload-past-any-inline-threshold-" + i.to_string(); }
`

func ownParamReleaseMain(body string) string {
	return "\nfunction main(): i32 { var x: i32 = 0; var i: i32 = 0; " +
		"while (i < 100) { " + body + " i = i + 1; } " +
		"if (__rc_underflow_count() != 0) { return 99; } return x % 83; }"
}

func ownParamReleaseCases() []ownParamReleaseCase {
	return []ownParamReleaseCase{
		{
			// THE BORROWED ROW, half closed. Was 400/100 live 16800 — the
			// argument temp and the call result both stranded; the result is
			// released now (400/200 live 12000), the temp is not.
			//
			// Freeing the temp needs stash_fresh_struct_arg to admit a
			// string-fielded type, and that widening is UNSOUND with what the
			// self-host can prove today: borrowability says the callee keeps no
			// reference to the BOX and nothing about a FIELD it hands out. It
			// passes every small-program gate here and segfaults gen1 of
			// TestSelfHostPerModuleEmitAllFixpointX86_64 — bisected to that one
			// predicate, with A/C/D of this change green in every combination.
			// So the row pins the residual leak rather than a balance: a HIGHER
			// free count here is that widening being attempted again.
			name: "borrowed_spread_string",
			src: ownParamReleaseHead + `@noinline
function bump(p: P): P { return P { ...p, n: p.n + 1 }; }` +
				ownParamReleaseMain(`var q: P = bump(P { s: w(i), n: i }); x = x + q.n + q.s.len();`),
			want: 78, wantFrees: 200,
		},
		{
			// THE OWN STRING ROW. Was 300/100 live 12000. The callee reused
			// the param's box already (the own-update family); `q` leaked.
			name: "own_spread_string",
			src: ownParamReleaseHead + `@noinline
function bump(own p: P): P { return P { ...p, n: p.n + 1 }; }` +
				ownParamReleaseMain(`var q: P = bump(P { s: w(i), n: i }); x = x + q.n + q.s.len();`),
			want: 78, balance: true,
		},
		{
			// THE OWN ENUM ROW. Was 300/0 live 13600: reuse refused for the
			// enum field, the param never released, `q` never released.
			name: "own_spread_enum",
			src: `enum E { A(i32), B(i32) }
struct Q { e: E, n: i32 }
@noinline
function bumpq(own p: Q): Q { return Q { ...p, n: p.n + 1 }; }` +
				ownParamReleaseMain(`var q: Q = bumpq(Q { e: A(i), n: i }); x = x + q.n; match (q.e) { A(v) => { x = x + v; }, B(v) => { x = x + v * 2; } }`),
			want: 40, balance: true,
		},
		{
			// The scalar control, which read 100/100 on the parent by accident:
			// the caller freed the argument the callee had reused as its
			// result, and the result was then read after the free.
			name: "own_scalar_control",
			src: `struct N { m: i32, n: i32 }
@noinline
function bump(own p: N): N { return N { ...p, n: p.n + 1 }; }` +
				ownParamReleaseMain(`var q: N = bump(N { m: i, n: i }); x = x + q.n + q.m;`),
			want: 40, balance: true,
		},
		{
			// The borrowed scalar control. Was 200/100: the argument temp was
			// released, `q` was not.
			name: "borrowed_scalar_control",
			src: `struct N { m: i32, n: i32 }
@noinline
function bump(p: N): N { return N { ...p, n: p.n + 1 }; }` +
				ownParamReleaseMain(`var q: N = bump(N { m: i, n: i }); x = x + q.n + q.m;`),
			want: 40, balance: true,
		},
		{
			// An `own` param the callee only READS: nothing donated, so the
			// exit release is the only thing that frees it.
			name: "own_read_only",
			src: ownParamReleaseHead + `@noinline
function sink(own p: P): i32 { return p.n + p.s.len(); }` +
				ownParamReleaseMain(`x = x + sink(P { s: w(i), n: i });`),
			want: 61, balance: true,
		},
		{
			// Passed on to another `own` position: the first frame's credit is
			// refused (a non-borrowable argument is a move) and the second
			// frame releases. One owner at every point.
			name: "own_passed_on",
			src: ownParamReleaseHead + `@noinline
function sink(own p: P): i32 { return p.n + p.s.len(); }
@noinline
function pass_on(own p: P): i32 { var k: i32 = p.n; return sink(p) + k; }` +
				ownParamReleaseMain(`x = x + pass_on(P { s: w(i), n: i });`),
			want: 31, balance: true,
		},
		{
			// A VOID callee that falls off its end. The implicit exit went
			// through the backend's default epilogue with no sweep at all —
			// void functions leaked every local there, own params included.
			name: "own_void_fallthrough",
			src: ownParamReleaseHead + `@noinline
function sink_void(own p: P): void { var k: i32 = p.n; if (k < 0) { return; } }` +
				ownParamReleaseMain(`sink_void(P { s: w(i), n: i }); x = x + i;`),
			want: 53, balance: true,
		},
		{
			// `return p` hands the moved-in box straight back: the callee's
			// release is refused (struct_returned_bare) and the caller's
			// binding earns the strict-fresh credit instead.
			name: "own_returned_bare",
			src: ownParamReleaseHead + `@noinline
function id(own p: P): P { if (p.n < 0) { return P { ...p, n: 0 }; } return p; }` +
				ownParamReleaseMain(`var q: P = id(P { s: w(i), n: i }); x = x + q.n + q.s.len();`),
			want: 61, balance: true,
		},
		{
			// REFUSED reuse (a borrowed string as the override) over a type
			// with an ARRAY field: the spread copies `xs` uncounted into the
			// result, so the param's exit release must stay BOX-ONLY
			// ("OWNRELB:"). The string it carried leaks; nothing dangles.
			name: "own_array_spread_refused_reuse",
			src: `import "std/i32";
struct A { xs: i32[], s: string, n: i32 }
@noinline
function w(i: i32): string { return "s-a-wide-payload-past-any-inline-threshold-" + i.to_string(); }
@noinline
function relabel(own p: A, t: string): A { return A { ...p, s: t }; }` +
				ownParamReleaseMain(`var t: string = w(i + 1); var q: A = relabel(A { xs: [i, i + 1], s: w(i), n: i }, t); x = x + q.n + q.s.len() + q.xs[1] + t.len();`),
			want: 60, wantFrees: 300,
		},
		{
			// The own-update string override over a COUNTED share: the
			// argument literal took `h.s` with a retain (P routes), so the
			// reuse arm's rc-aware free only decs and `h` keeps its string.
			name: "field_read_share_own_override",
			src: ownParamReleaseHead + `struct H { s: string, k: i32 }
@noinline
function bump(own p: P): P { return P { ...p, s: "override-payload-wide-enough-to-heap-" + w(p.n), n: p.n + 1 }; }` +
				ownParamReleaseMain(`var h: H = H { s: w(i), k: i }; var q: P = bump(P { s: h.s, n: i }); x = x + q.n + q.s.len() + h.s.len();`),
			want: 51, wantFrees: 600,
		},
		{
			// The same with the call result SPREAD again in the caller —
			// the shape that first exposed the bogus post-call free of an
			// `own` argument (exit 99 with the type gate alone widened).
			name: "call_result_spread_again",
			src: ownParamReleaseHead + `struct H { s: string, k: i32 }
@noinline
function bump(own p: P): P { return P { ...p, s: "override-payload-wide-enough-to-heap-" + w(p.n), n: p.n + 1 }; }` +
				ownParamReleaseMain(`var h: H = H { s: w(i), k: i }; var q: P = bump(P { s: h.s, n: i }); var z: P = P { ...q, n: 0 }; x = x + q.n + q.s.len() + h.s.len() + z.n;`),
			want: 51, wantFrees: 600,
		},
		{
			// THE USE-AFTER-FREE. `get` returns a field named `s`, which the
			// routing scan reads as unsafe for EVERY `s` field, so neither P
			// nor H routes and the argument literal takes no retain on `h.s`.
			// The parent still admitted `bump` to the own-update family and
			// freed `h.s` under `h`; `churn` recycles the box before the read.
			// Parent: exit 77 (h.s.len() moved). Native: 12.
			name: "unrouted_string_own_update_refused",
			src: ownParamReleaseHead + `struct H { s: string, k: i32 }
@noinline
function bump(own p: P): P { return P { ...p, s: "override-payload-wide-enough-to-heap-" + w(p.n), n: p.n + 1 }; }
@noinline
function get(x: P): string { return x.s; }
@noinline
function churn(i: i32): i32 { var a: string = w(i) + w(i + 1); var b: string = w(i + 2) + w(i + 3); return a.len() + b.len(); }` +
				ownParamReleaseMain(`var h: H = H { s: w(i), k: i }; var want: i32 = h.s.len(); var q: P = bump(P { s: h.s, n: i }); x = x + churn(i); if (h.s.len() != want) { return 77; } if (h.s[0] != b's') { return 78; } var g: string = get(q); x = x + q.n + g.len() + h.s.len();`),
			want: 12, wantFrees: 1600,
		},
		{
			// A self-update followed by a return-position update, with a
			// field bound out before either: the bind is counted, so the
			// first update's free only decs.
			name: "self_update_then_return_update",
			src: ownParamReleaseHead + `@noinline
function bump(own p: P): P {
    var s2: string = p.s;
    p = P { ...p, s: "override-payload-wide-enough-to-heap-" + w(p.n) };
    return P { ...p, n: p.n + s2.len() };
}` +
				ownParamReleaseMain(`var q: P = bump(P { s: w(i), n: i }); x = x + q.n + q.s.len();`),
			want: 34, wantFrees: 500,
		},
		{
			// The LOCAL self-overwrite family with an enum field bound out
			// first: the same counted-bind argument the own admission rests
			// on, witnessed on the family that already shipped.
			name: "enum_field_bind_then_local_override",
			src: `enum E { A(i32), B(i32) }
struct Q { e: E, n: i32 }
@noinline
function round(i: i32): i32 {
    var d: Q = Q { e: A(i), n: i };
    var e2: E = d.e;
    var c: Q = Q { ...d, e: B(i + 1) };
    var r: i32 = c.n;
    match (e2) { A(v) => { r = r + v; }, B(v) => { r = r + v * 2; } }
    match (c.e) { A(v) => { r = r + v; }, B(v) => { r = r + v * 3; } }
    return r;
}` +
				ownParamReleaseMain(`x = x + round(i);`),
			want: 67, balance: true,
		},
		{
			// A fresh literal at a COUNTED-RETAIN position: the callee appends
			// it into a container it returns, so the box is shared after the
			// call and the temp's release must skip the field walk (the
			// rc==1 gate) and dec the box only.
			name: "counted_position_array_field_temp",
			src: `struct H { id: i32, xs: i32[] }
struct Hold { items: H[], n: i32 }
@noinline
function keep(h: H, k: i32): Hold { var items: H[] = []; items = items.append(h); return Hold { items: items, n: k }; }` +
				ownParamReleaseMain(`var hd: Hold = keep(H { id: i, xs: [i, i + 1, i + 2] }, i); x = x + hd.n + hd.items[0].xs.len() + hd.items[0].xs[2];`),
			want: 25, wantFrees: 300,
		},
		{
			// A callee that RETURNS a field of its param is not borrowable,
			// so the widened temp stash must not fire on its argument: `g`
			// aliases the literal's string and reads it after churn.
			name: "borrowed_field_returning_callee_temp",
			src: ownParamReleaseHead + `@noinline
function get(x: P): string { return x.s; }
@noinline
function churn(i: i32): i32 { var a: string = w(i) + w(i + 1); var b: string = w(i + 2) + w(i + 3); return a.len() + b.len(); }` +
				ownParamReleaseMain(`var g: string = get(P { s: w(i), n: i }); x = x + churn(i); if (g[0] != b's') { return 78; } x = x + g.len();`),
			want: 52, wantFrees: 1100,
		},
	}
}

// TestSelfHostOwnParamReleaseX86_64 — every admitted row balances at
// live_bytes 0 with no rc underflow, on the census leg and again under the
// quarantining allocator; every refused row keeps its exact free count.
func TestSelfHostOwnParamReleaseX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range ownParamReleaseCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "ownrel_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 77/78 = a read "+
					"through a freed box)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if tc.balance {
				if live != 0 || allocs != frees {
					t.Errorf("%s: %s — must balance at live_bytes 0 (native does)", tc.name, summary)
				}
			} else if frees != tc.wantFrees {
				t.Errorf("%s: %s — refused row's frees moved (want exactly %d). A "+
					"HIGHER count is the refusal breaking down: a box released "+
					"while something else still holds it", tc.name, summary, tc.wantFrees)
			}

			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "ownrel_san_"+tc.name, sanAsm)
			sanErr, sanExit := hevRun(t, runner, sanBin)
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}
		})
	}
}

// TestSelfHostOwnParamReleaseWasmIR — the wasm sibling. Exit codes only: an
// over-release moves no byte count on any backend.
func TestSelfHostOwnParamReleaseWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping own-param release wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range ownParamReleaseCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "ownrel_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("own-param release wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostOwnParamReleaseIRArm64 — the arm64 sibling under qemu.
func TestSelfHostOwnParamReleaseIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range ownParamReleaseCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "ownrel_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("own-param release arm64 IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
