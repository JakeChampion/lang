package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- The string[] credit, keyed on the binding rather than the name (#7253) ----
//
// `slot_is_reclaimable_strarr` resolved "SARR:" / "SARRB:" through
// reclaim_slot_name, so the credit was keyed by the source NAME and a name has no
// scope. Two `var v: string[]` in sibling `if` arms are two slots under one key:
// the arm that binds a FRESH array earns the credit, and the arm that binds a bare
// ALIAS inherits it and hands its buffer to __fern_str_arr_free — a buffer someone
// else still owns.
//
//	self-host 99 (rc underflow)   native 34   interp 34
//
// with `allocs=255 frees=255 live_bytes=0`, which is the trap: a doubly-released
// block goes back to the freelist, so the byte count is clean and only
// `__rc_underflow()` dissents. This is the same defect #7272 fixed for the tuple
// classes and #7292 for "STR:", one class over.
//
// What isolates it is a one-word rename. `param_rename` below is `param_alias`
// with the second local called `u`, and nothing else changed: it was ALREADY
// correct before the fix. After the fix `param_alias` matches it byte for byte,
// which is the assertion at the bottom of the x86-64 runner — the colliding
// program becomes indistinguishable from the program that never collided.
//
// The residual `live_bytes` those two share (64, and 104 for the struct-field
// shape) is NOT this bug and is not fixed here: it is #7259, a struct/param
// string[] whose type the whole-program strarrfld scan refuses to admit, and it
// measures identically before and after this change. That is why the leak
// assertion here is the pairwise one rather than `allocs == frees` — asserting a
// balance these shapes do not have would mean either encoding #7259's byte count
// as correct or dropping the case.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the native
// x86-64 backend agreed on each — never read off the self-host run under test.

type strarrKeyCase struct {
	name string
	src  string
	want int
}

const strarrW = "function w(a: string): string { return a + \"!\"; }\n" +
	"function mk(): string[] { var a: string[] = [w(\"p\"), w(\"q\")]; return a; }\n"

const strarrMain = "\nfunction main(): i32 { var b: string[] = [w(\"a\"), w(\"b\")]; " +
	"var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(b, i); i = i + 1; } " +
	"if (__rc_underflow() != 0) { return 99; } return t % 83; }"

func strarrKeyCases() []strarrKeyCase {
	return []strarrKeyCase{
		{
			// The repro, with the alias taken from a PARAMETER so the shape carries
			// nothing but this bug.
			name: "param_alias",
			src: strarrW + `function round(base: string[], i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var v: string[] = mk();  t = t + v.len(); }
    if (i % 2 == 1) { var v: string[] = base;  t = t + v.len(); }
    return t;
}` + strarrMain,
			want: 34,
		},
		{
			// The same program under a one-word rename: no shared key, so it was
			// correct before the fix and pins that the fix did not get its result by
			// denying the credit outright.
			name: "param_rename",
			src: strarrW + `function round(base: string[], i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var v: string[] = mk();  t = t + v.len(); }
    if (i % 2 == 1) { var u: string[] = base;  t = t + u.len(); }
    return t;
}` + strarrMain,
			want: 34,
		},
		{
			// The alias read off a STRUCT FIELD instead of a parameter — the shape
			// the bug was found in, and a different collector path to the same key.
			name: "struct_field_alias",
			src: `struct H { xs: string[] }
` + strarrW + `function round(h: H, i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var v: string[] = mk();  t = t + v.len(); }
    if (i % 2 == 1) { var v: string[] = h.xs;  t = t + v.len(); }
    return t;
}
function main(): i32 { var h: H = H { xs: [w("a"), w("b")] }; var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(h, i); i = i + 1; } if (__rc_underflow() != 0) { return 99; } return t % 83; }`,
			want: 34,
		},
		{
			// Only the FRESH arm. The credit must still be granted — a site key that
			// resolved to nothing would leak here rather than over-release, and this
			// is the case that catches it: it balances exactly.
			name: "fresh_only",
			src: strarrW + `function round(base: string[], i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var v: string[] = mk();  t = t + v.len(); }
    return t;
}` + strarrMain,
			want: 17,
		},
		{
			// Only the ALIAS arm — no fresh binding anywhere, so no credit exists to
			// inherit and nothing may be released.
			name: "alias_only",
			src: strarrW + `function round(base: string[], i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 1) { var v: string[] = base;  t = t + v.len(); }
    return t;
}` + strarrMain,
			want: 17,
		},
		{
			// The scalar-array control: i32[] takes a different predicate entirely
			// and was correct throughout, so a change that disturbed the shared
			// machinery instead of this one credit would show here.
			name: "scalar_arr_unaffected",
			src: `function mkI(n: i32): i32[] { var a: i32[] = [n, n + 1, n + 2]; return a; }
function round(base: i32[], i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var v: i32[] = mkI(i); t = t + v.len(); }
    if (i % 2 == 1) { var v: i32[] = base;   t = t + v.len(); }
    return t;
}
function main(): i32 { var b: i32[] = [7, 8, 9]; var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(b, i); i = i + 1; } if (__rc_underflow() != 0) { return 99; } return t % 83; }`,
			want: 51,
		},
	}
}

// TestSelfHostStrArrSlotKeyX86_64 — each string[] binding resolves the credit its
// own binding earned.
//
// The exit code is the load-bearing assertion: an over-release does not move
// live_bytes, so `__rc_underflow()` is the only thing that separates a correct
// compiler from one that frees a live buffer. `fresh_only` carries the opposite
// direction — it balances exactly, so a site key resolving to no credit fails
// there instead of passing quietly.
func TestSelfHostStrArrSlotKeyX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	summaries := map[string]string{}
	for _, tc := range strarrKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "strarrkey_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: an aliasing string[] "+
					"local inherited a same-named sibling's SARR: credit and freed a live "+
					"buffer)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			summaries[tc.name] = summary
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path. "+
					"Every string here goes through w(); a constant-folded one is an "+
					"immortal literal (rc = -1) and measures nothing", tc.name)
			}
			// The two shapes that must balance: no aliasing local is involved, so
			// #7259 does not apply and a denied credit shows up as a leak.
			if tc.name == "fresh_only" || tc.name == "scalar_arr_unaffected" {
				if live != 0 || allocs != frees {
					t.Errorf("%s: %s — must balance at live_bytes 0; a site key that "+
						"resolves to no credit leaks here, which the exit code cannot show",
						tc.name, summary)
				}
			}
		})
	}

	// The rename is the proof. param_rename never collided (distinct names) and was
	// correct before this change; param_alias differs from it by one identifier. If
	// the fix is right they are now indistinguishable, residual #7259 bytes included.
	if a, b := summaries["param_alias"], summaries["param_rename"]; a != "" && b != "" && a != b {
		t.Errorf("param_alias and param_rename must measure identically once the credit "+
			"is keyed on the binding — the programs differ only by one identifier:\n"+
			"  param_alias  %s\n  param_rename %s", a, b)
	}
}

// TestSelfHostStrArrSlotKeyWasmIR — the wasm sibling. Exit codes only: an
// over-release moves no byte count on any backend, so the code is the whole signal.
func TestSelfHostStrArrSlotKeyWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping string[] slot-key wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range strarrKeyCases() {
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
			watFile := filepath.Join(dir, "strarrkey_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("string[] slot-key wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostStrArrSlotKeyIRArm64 — the arm64 sibling under qemu.
func TestSelfHostStrArrSlotKeyIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strarrKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "strarrkey_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("string[] slot-key arm64 IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
