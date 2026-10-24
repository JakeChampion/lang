package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const freshStructArgProlog = "struct Op { a: i32, b: i32 }\n" +
	"struct St { n: i32 }\n" +
	"struct Box { o: Op, k: i32 }\n" +
	"function mkop(i: i32): Op { return Op { a: i, b: i + 1 }; }\n" +
	"function (s: St) count(o: Op): St { return St { n: s.n + o.a }; }\n" +
	"function countf(s: St, o: Op): St { return St { n: s.n + o.a }; }\n" +
	"@noinline function keepf(o: Op): Op { return o; }\n" +
	"function wrapf(o: Op, k: i32): Box { return Box { o: o, k: k }; }\n"

func freshStructArgSrc(body string, rounds int) string {
	return freshStructArgProlog +
		"function main(): i32 { let st: St = St { n: 0 }; let i: i32 = 0; " +
		"while (i < " + fmt.Sprint(rounds) + ") { " + body + " i = i + 1; } " +
		"if (__rc_underflow_count() != 0) { return 99; } return st.n % 83; }"
}

type freshStructArgCase struct {
	name string
	body string
}

func wantFor(rounds int) int {
	sum := rounds * (rounds - 1) / 2
	return sum % 83
}

func freshStructArgCases() []freshStructArgCase {
	return []freshStructArgCase{
		{name: "method_fresh_call_arg", body: "st = st.count(mkop(i));"},
		{name: "method_struct_lit_arg", body: "st = st.count(Op { a: i, b: i });"},
		{name: "free_fn_fresh_call_arg", body: "st = countf(st, mkop(i));"},
		{name: "free_fn_struct_lit_arg", body: "st = countf(st, Op { a: i, b: i });"},
		{name: "bound_first", body: "let o: Op = mkop(i); st = st.count(o);"},
	}
}

func TestSelfHostFreshStructArgX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range freshStructArgCases() {
		t.Run(tc.name, func(t *testing.T) {
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

func TestSelfHostFreshStructArgRefusedX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	refused := []struct{ name, body string }{
		{"callee_returns_arg", "st = st.count(keepf(mkop(i)));"},
		{"callee_wraps_arg", "let bx: Box = wrapf(mkop(i), i); st = St { n: st.n + bx.o.a };"},
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

func TestSelfHostFreshStructArgWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping fresh struct arg wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range freshStructArgCases() {
		t.Run(tc.name, func(t *testing.T) {
			src := freshStructArgSrc(tc.body, 100)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
