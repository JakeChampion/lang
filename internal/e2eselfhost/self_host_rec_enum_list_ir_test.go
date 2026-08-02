package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// recEnumListIRCases pin a RECURSIVE enum used as a multi-node heap data
// structure — a cons-list `enum List { Cons(i32, List), Nil }` whose `Cons`
// variant carries the enum type itself — on the self-host IR path (x86-64 +
// wasm). The existing recursive-enum coverage (self_host_rc_precise_drop's
// `Tree.Leaf(7)`) is a single shallow node whose match arm returns immediately;
// it never builds or traverses a multi-level chain. These cases exercise the
// distinct shape: a heap-boxed enum-payload CHAIN (each `Cons` boxes the next
// `List`), genuine deep structural recursion over that payload (a function that
// recurses on the enum value nested inside the enum), and the multi-node drop on
// function exit. All of it already lowers, so no compiler change — this is an
// observability pin against a regression to the AST fallback.
//
// Each case is routing-pinned to "ir" (asm_pathprobe_run) and oracle-checked
// against the interpreter; every result stays <= 120 (the wasm exit-code clamp,
// #2908).
const recEnumListIRPrelude = `enum List { Cons(i32, List), Nil }
function sum(l: List): i32 {
    match (l) {
        Cons(h, t) => { return h + sum(t); },
        Nil => { return 0; },
    }
}
function length(l: List): i32 {
    match (l) {
        Cons(h, t) => { return 1 + length(t); },
        Nil => { return 0; },
    }
}
function head_or(l: List, d: i32): i32 {
    match (l) {
        Cons(h, t) => { return h; },
        Nil => { return d; },
    }
}
`

var recEnumListIRCases = []struct {
	name string
	main string
	want int
}{
	// deep recursion summing a 3-node chain: 10 + 20 + 12 = 42.
	{"sum-3", `var l: List = Cons(10, Cons(20, Cons(12, Nil))); return sum(l);`, 42},
	// length of a 5-node chain.
	{"length-5", `var l: List = Cons(1, Cons(2, Cons(3, Cons(4, Cons(5, Nil))))); return length(l);`, 5},
	// sum over a 5-node chain: 1+2+3+4+5 = 15.
	{"sum-5", `var l: List = Cons(1, Cons(2, Cons(3, Cons(4, Cons(5, Nil))))); return sum(l);`, 15},
	// the Nil base case (empty list) returns 0; +3 keeps the exit code distinct.
	{"empty-sum", `var l: List = Nil; return sum(l) + 3;`, 3},
	// the Cons arm binds the head payload across the recursion boundary.
	{"head-or", `var l: List = Cons(7, Nil); return head_or(l, 99);`, 7},
	// the Nil arm returns the default.
	{"head-or-empty", `var l: List = Nil; return head_or(l, 42);`, 42},
}

func recEnumListIRSrc(mainBody string) string {
	return recEnumListIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostRecEnumListIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, with the routing pinned to the "ir" path.
func TestSelfHostRecEnumListIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range recEnumListIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(recEnumListIRSrc(tc.main))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostRecEnumListIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostRecEnumListIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host recursive-enum-list wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range recEnumListIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(recEnumListIRSrc(tc.main))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "rec_enum_list_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("rec-enum-list wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
