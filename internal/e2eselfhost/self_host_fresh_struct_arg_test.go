package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A fresh STRUCT handed straight to a call argument is freed after the call
//
// `take(mk(i))` — a box nothing else can reach — leaked per EVALUATION. The
// certifier's self-host run put 6,654 of its 7,132 findings on a call result
// still held at the return, and the largest single shape under that was a
// struct produced by one call and handed to the next.
//
// Two independent gaps, one per axis, measured at 100 and 200 rounds:
//
//	argument           free callee     METHOD callee
//	Op { … } literal   200/200 clean   200 allocs / 100 frees
//	mk(i) call         200/100         200/100
//	bound to a var     clean           clean
//
// Exactly 2.0x per doubling on every leaking cell — unbounded, not a bounded
// per-object loss.
//
// The literal row was fixed for free callees by #7576 and never reached a
// method: `lower_call_struct_method` had the string stash and no struct one,
// and `lit_arg_callees_expr`'s method arm collected only `ExprString`, so
// `call_arg_borrowable` answered false at every method callee. The call row
// had no arm on either path — `lower_call_named`'s producer-call case admitted
// the "ARR:" / "STRARR:" registries only, never a struct producer.
//
// `stash_fresh_struct_arg` / `free_stashed_struct_args` now hold both shapes
// once, and all three call paths (free, primitive-receiver method, struct
// method) go through them.
//
// Every want below was confirmed against BOTH oracles — bin/fern -interp and
// the native x86-64 backend agreed on 53 for each — never read off the
// self-host run.

const freshStructArgProlog = "struct Op { a: i32, b: i32 }\n" +
	"struct St { n: i32 }\n" +
	"struct Box { o: Op, k: i32 }\n" +
	"function mkop(i: i32): Op { return Op { a: i, b: i + 1 }; }\n" +
	"function (s: St) count(o: Op): St { return St { n: s.n + o.a }; }\n" +
	"function countf(s: St, o: Op): St { return St { n: s.n + o.a }; }\n" +
	"function keepf(o: Op): Op { return o; }\n" +
	"function wrapf(o: Op, k: i32): Box { return Box { o: o, k: k }; }\n"

func freshStructArgSrc(body string, rounds int) string {
	return freshStructArgProlog +
		"function main(): i32 { var st: St = St { n: 0 }; var i: i32 = 0; " +
		"while (i < " + fmt.Sprint(rounds) + ") { " + body + " i = i + 1; } " +
		"if (__rc_underflow() != 0) { return 99; } return st.n % 83; }"
}

type freshStructArgCase struct {
	name string
	body string
}

// wantFor is the program's answer: the sum of the loop counter mod 83.
func wantFor(rounds int) int {
	sum := rounds * (rounds - 1) / 2
	return sum % 83
}

func freshStructArgCases() []freshStructArgCase {
	return []freshStructArgCase{
		// The self-host compiler's own dominant shape: a fresh struct from a
		// producer call handed to a method. `p.emit(ir.op_load_local(i))`.
		{name: "method_fresh_call_arg", body: "st = st.count(mkop(i));"},
		// The literal at a method callee — the half of #7576 that never
		// reached the method path.
		{name: "method_struct_lit_arg", body: "st = st.count(Op { a: i, b: i });"},
		// The producer call at a free callee: seeded by lit_arg_callees_expr
		// all along, with no arm at the lowering to consume the seed.
		{name: "free_fn_fresh_call_arg", body: "st = countf(st, mkop(i));"},
		// The cell #7576 fixed. Pinned so the extraction into the shared
		// helper cannot regress it.
		{name: "free_fn_struct_lit_arg", body: "st = countf(st, Op { a: i, b: i });"},
		// Bound to a local first — the one position that already worked, and
		// the one a stash firing twice would over-release.
		{name: "bound_first", body: "var o: Op = mkop(i); st = st.count(o);"},
	}
}

// TestSelfHostFreshStructArgX86_64 — the fresh struct temp at a borrowable
// argument position is released exactly once.
//
// `allocs == frees` at `live_bytes == 0` is the leak assertion; the exit code
// carries the over-release one, since a doubly-released block returns to the
// freelist and moves neither count.
func TestSelfHostFreshStructArgX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range freshStructArgCases() {
		t.Run(tc.name, func(t *testing.T) {
			// Both round counts: a leak here is per evaluation, so the
			// doubling is what separates it from a bounded per-object loss.
			for _, rounds := range []int{100, 200} {
				src := freshStructArgSrc(tc.body, rounds)
				asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
				progBin := buildBin(t, gcc, dir, fmt.Sprintf("freshstructarg_%s_%d", tc.name, rounds), asm)
				stderr, exit := hevRun(t, runner, progBin)
				if want := wantFor(rounds); exit != want {
					t.Fatalf("%s at %d rounds exited %d, want %d (99 = rc underflow: "+
						"the stash released a box a live binding still owns)", tc.name, rounds, exit, want)
				}
				summary := leakSummaryLine(stderr)
				if summary == "" {
					t.Fatalf("%s at %d rounds: no leakcheck summary", tc.name, rounds)
				}
				var allocs, frees, live int64
				if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
					t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
				}
				if allocs == 0 {
					t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
				}
				if live != 0 || allocs != frees {
					t.Errorf("%s at %d rounds: %s — the argument box is allocated per "+
						"evaluation, so a missing release scales with the loop", tc.name, rounds, summary)
				}
			}
		})
	}
}

// TestSelfHostFreshStructArgRefusedX86_64 — a callee that KEEPS the argument
// must not have it freed underneath.
//
// `keepf(o) -> o` hands the box straight back out and `wrapf(o, k) -> Box { o,
// k }` moves it into a field; releasing after either call frees a box the
// caller still reads. Both are correctly unborrowable, so no stash fires and
// both keep their prior safe leak — which is why this asserts the ANSWER and
// `__rc_underflow()` rather than leak counts. Removing the borrowability gate
// is what these two rows catch, and they catch it as a wrong answer or an
// underflow, not as a number.
func TestSelfHostFreshStructArgRefusedX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	refused := []struct{ name, body string }{
		{"callee_returns_arg", "st = st.count(keepf(mkop(i)));"},
		{"callee_wraps_arg", "var bx: Box = wrapf(mkop(i), i); st = St { n: st.n + bx.o.a };"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			src := freshStructArgSrc(tc.body, 100)
			asm := hevCompile(t, runner, driverBin, src, nil)
			progBin := buildBin(t, gcc, dir, "freshstructarg_refused_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if want := wantFor(100); exit != want {
				t.Errorf("%s exited %d, want %d (99 = rc underflow: the stash "+
					"admitted a callee that keeps the argument)", tc.name, exit, want)
			}
		})
	}
}

// TestSelfHostFreshStructArgWasmIR — the wasm sibling. Exit codes only: the
// leak counters are an x86-64 self-host backend feature.
func TestSelfHostFreshStructArgWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping fresh struct arg wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range freshStructArgCases() {
		t.Run(tc.name, func(t *testing.T) {
			src := freshStructArgSrc(tc.body, 100)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "freshstructarg_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got, want := rcmd.ProcessState.ExitCode(), wantFor(100); got != want {
				t.Errorf("fresh struct arg wasm IR %q = %d, want %d", tc.name, got, want)
			}
		})
	}
}
