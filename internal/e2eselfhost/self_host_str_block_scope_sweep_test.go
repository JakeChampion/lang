package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A BLOCK-SCOPED fresh string local is swept (#7292) ----------------------
//
// The "STR:" credit was keyed on the source NAME, and `retire_locals` renames a
// block-scoped local's slot to "!retired!<name>" once its block ends. The exit
// sweep's string loop looked the name up with an exact match, so it missed every
// such slot and released nothing at all:
//
//	`{ var s: string = w("ab"); … }`   allocs=200 frees=0     3200 / 100 rounds
//	the same in a `while` body         allocs=600 frees=400   3200 / 100 rounds
//	the same at FUNCTION scope         allocs=200 frees=200   0
//
// against 0 on native and interp for all three. The loop row is the same defect:
// its rebind store supersedes each iteration's box, so only the FINAL one strands
// and it reads as the n-1-of-n signature. The plain block is what shows the shape
// — nothing is released, so the count is not "one short", it is all of them.
//
// The obvious fix — resolve through `reclaim_slot_name`, as #6127 did for the
// struct class — closes every row above and OVER-RELEASES: a second `var s` in a
// sibling block shares the one name-keyed credit while holding a bare alias, and
// the exact-match miss was accidentally shielding that collision. The two are one
// defect seen from opposite ends, so no lookup change alone can be correct.
//
// What works is keying the credit on the BINDING SITE (`name@line:col`, #7298's
// mechanism): the key lives on the slot, so the rename cannot hide it, and two
// same-named bindings resolve to their own credit. Entry-zeroing needs no part in
// it — the prologue zeroes the whole body slot range on every backend — but
// untaken_branch below pins that a never-written slot stays safe to sweep.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the native
// x86-64 backend agreed on each — never read off the self-host run under test.

const strBlockW = "function w(a: string): string { return a + \"!\"; }\n"

const strBlockMain = "\nfunction main(): i32 { var x: i32 = 0; var r: i32 = 0; " +
	"while (r < 100) { x = x + round(r); r = r + 1; } " +
	"if (__rc_underflow() != 0) { return 99; } return x % 83; }"

type strBlockCase struct {
	name string
	src  string
	want int
}

func strBlockCases() []strBlockCase {
	return []strBlockCase{
		{
			// A plain `{ }` block — the clearest form. Released by nothing before.
			name: "plain_block",
			src: strBlockW + `function round(i: i32): i32 {
    var acc: i32 = 0;
    { var s: string = w("ab"); acc = acc + s.len(); }
    return acc + i;
}` + strBlockMain,
			want: 21,
		},
		{
			// A `while` body, the issue's own repro. The rebind store already freed
			// the superseded boxes, so this row moves from n-1-of-n to n-of-n.
			name: "loop_body",
			src: strBlockW + `function round(i: i32): i32 {
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 3) { var s: string = w("ab"); acc = acc + s.len(); k = k + 1; }
    return acc + i;
}` + strBlockMain,
			want: 40,
		},
		{
			// An `if` arm — the same rename, reached through different control flow.
			name: "if_arm",
			src: strBlockW + `function round(i: i32): i32 {
    var acc: i32 = 0;
    if (i % 3 == 0) { var s: string = w("abc"); acc = acc + s.len(); }
    else { var s: string = w("de"); acc = acc + s.len(); }
    return acc + i;
}` + strBlockMain,
			want: 55,
		},
		{
			// FUNCTION scope — never renamed, so it was always swept. Pinned because
			// a fix that changed the exact-match lookup itself, rather than adding a
			// scoped variant beside it, would move this row too.
			name: "function_scope_unchanged",
			src: strBlockW + `function round(i: i32): i32 {
    var s: string = w("ab");
    return s.len() + i;
}` + strBlockMain,
			want: 21,
		},
		{
			// The block does not run on every call, so on the other calls the slot
			// never holds a string. The sweep now considers that slot, so it must
			// read null rather than stack garbage — which the prologue's zeroing of
			// the whole body range gives it. A crash or an underflow here, not a
			// leak, is what a regression in that looks like.
			name: "untaken_branch",
			src: strBlockW + `function round(i: i32): i32 {
    var acc: i32 = 0;
    if (i % 2 == 0) { var s: string = w("ab"); acc = acc + s.len(); }
    return acc + i;
}` + strBlockMain,
			want: 37,
		},
		{
			// A returned accumulator is MOVED OUT to the caller and must NOT be
			// swept. The move-on-return keeps set resolves a slot from a live AST
			// name reference, so a retired slot can never appear in it. Sweeping it
			// here would free the box just handed back.
			name: "move_on_return_not_swept",
			src: strBlockW + `function build(n: i32): string { var acc: string = w("a"); return acc; }
function round(i: i32): i32 { var s: string = build(i); return s.len() + i; }` + strBlockMain,
			want: 4,
		},
	}
}

// TestSelfHostStrBlockScopeSweepX86_64 — a block-scoped string local is released
// exactly once.
//
// `allocs == frees` at `live_bytes == 0` is the leak assertion. The exit code
// carries the two over-release directions, which no byte count would show: a
// swept-but-returned box (the caller reads freed memory) and a swept-but-never-
// assigned slot (stack garbage handed to __fern_str_free).
func TestSelfHostStrBlockScopeSweepX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strBlockCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "strblock_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the sweep claimed a "+
					"box that was moved out, or a slot that never held one)", tc.name, exit, tc.want)
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
				t.Fatalf("%s allocated nothing — the probe is not exercising the path. "+
					"Every string here comes from w(), never a fold-able literal", tc.name)
			}
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — the box is allocated per round, so a missing release "+
					"scales with the loop", tc.name, summary)
			}
		})
	}
}

// TestSelfHostStrBlockScopeSweepWasmIR — the wasm sibling. Exit codes only.
func TestSelfHostStrBlockScopeSweepWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping block-scoped string sweep wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range strBlockCases() {
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
			watFile := filepath.Join(dir, "strblock_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("block-scoped string sweep wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostStrBlockScopeSweepIRArm64 — the arm64 sibling under qemu.
func TestSelfHostStrBlockScopeSweepIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strBlockCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "strblock_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
