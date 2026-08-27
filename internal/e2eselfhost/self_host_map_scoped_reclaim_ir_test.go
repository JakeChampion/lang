package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mapScopedReclaimCases pin the map reclaim credit's BINDING-SITE key (#7253
// step 1). The credit used to be keyed on the variable NAME, read off
// LocalInfo.slot_name — which retire_locals renames to "!retired!<name>" when
// the declaring block exits. So a map declared inside an `if` or a loop body
// resolved no credit at the function-exit sweep and was never freed at all,
// while the identical map one indentation level out was flat.
//
// Measured on x86-64 and arm64 at 62 pages over the 2000-round steady window
// (82 on wasm) for each of the three scoped shapes below, against 0 for the
// function-scope control and 0 on the interpreter and native oracles.
//
// The site key lives on the SLOT and survives the rename, so all three shapes
// now free at exit; a loop-declared map additionally frees the prior
// iteration's box at each rebind, because the two loop-reinit sites read the
// same predicate. The string-column case is here because "MAPVS:"/"MAPKS:" are
// derived from the same collector entry as "MAP:" — leaving a column on the
// name key would silently downgrade a block-scoped map's deep release to the
// shallow free, which the byte counts alone would read as partial progress.
//
// Exit 0 is correct throughout: the leak cases return the steady-window page
// delta, so a surviving leak exits with its own size.
var mapScopedReclaimCases = []struct {
	name string
	src  string
}{
	// The map moved into an `if` body. Nothing else differs from the control.
	{"block-scoped-if-flat", `function build(n: i32): i32 {
    var r: i32 = 0;
    if (n >= 0) {
        var m: Map[i32, i32] = Map { 1: n, 2: n + 1 };
        if (m.has(1)) { r = r + 1; }
        if (m.has(2)) { r = r + 1; }
    }
    return r;
}

function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + build(i); i = i + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + build(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (acc != 4400) { return 90; }
    return (s2 - s1) / 4096;
}
`},
	// Declared in a loop body, so the slot is rebound three times per call: the
	// two loop-reinit sites free the prior iteration's box and the exit sweep
	// frees the last. Both read slot_is_reclaimable_map, so a split verdict
	// between them would double-free rather than leak.
	{"loop-declared-flat", `function build(n: i32): i32 {
    var r: i32 = 0;
    var k: i32 = 0;
    while (k < 3) {
        var m: Map[i32, i32] = Map { 1: n + k, 2: n + k + 1 };
        if (m.has(1)) { r = r + 1; }
        if (m.has(2)) { r = r + 1; }
        k = k + 1;
    }
    return r;
}

function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + build(i); i = i + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + build(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (acc != 13200) { return 90; }
    return (s2 - s1) / 4096;
}
`},
	// Block-scoped with fresh string KEYS and VALUES, so the deep-column
	// release ("MAPKS:" + "MAPVS:" -> __fern_map_free_kvs) has to resolve on
	// the same key as the base credit.
	{"block-scoped-string-columns-flat", `function build(n: i32): i32 {
    var r: i32 = 0;
    if (n >= 0) {
        var m: Map[string, string] = Map { "k" + "1": "v" + "1", "k" + "2": "v" + "2" };
        if (m.has("k1")) { r = r + 1; }
        if (m.has("k2")) { r = r + 1; }
    }
    return r;
}

function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + build(i); i = i + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + build(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (acc != 4400) { return 90; }
    return (s2 - s1) / 4096;
}
`},
	// The control the three leak cases are differential against: the same map
	// at function scope, which was always freed and must stay so.
	{"function-scope-control-flat", `function build(n: i32): i32 {
    var m: Map[i32, i32] = Map { 1: n, 2: n + 1 };
    var r: i32 = 0;
    if (m.has(1)) { r = r + 1; }
    if (m.has(2)) { r = r + 1; }
    return r;
}

function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + build(i); i = i + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + build(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (acc != 4400) { return 90; }
    return (s2 - s1) / 4096;
}
`},
	// The over-release direction the site key is what forbids: two `m` in
	// sibling `if` arms, one a fresh literal (credited) and one a bare alias of
	// the caller's map (not credited). Under a name key the two slots share one
	// verdict; here the alias arm must resolve its own site, find no credit, and
	// leave `base` alone for main to keep using.
	{"sibling-alias-no-over-release", `function round(base: Map[i32, i32], i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var m: Map[i32, i32] = Map { 1: i, 2: i + 1 }; if (m.has(1)) { t = t + 1; } }
    if (i % 2 == 1) { var m: Map[i32, i32] = base; if (m.has(1)) { t = t + 2; } }
    return t;
}

function main(): i32 {
    var b: Map[i32, i32] = Map { 1: 7, 2: 8 };
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { t = t + round(b, i); i = i + 1; }
    if (__fern_rc_underflow_get() != 0) { return 99; }
    if (!b.has(2)) { return 91; }
    if (t != 300) { return 92; }
    return 0;
}
`},
}

const mapScopedReclaimWant = "want 0 (a page count = the scoped map still leaks; 90 = wrong value; 91/92 = the aliased map was freed under its owner; 99 = over-release)"

// TestSelfHostMapScopedReclaimIRX86_64 runs each case through the self-hosted
// x86-64 IR driver, pinned to the "ir" path.
func TestSelfHostMapScopedReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range mapScopedReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "mscoped_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s exited %d, %s", tc.name, code, mapScopedReclaimWant)
			}
		})
	}
}

// TestSelfHostMapScopedReclaimIRArm64 is the arm64 leg, run under qemu.
func TestSelfHostMapScopedReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapScopedReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "mscoped_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s exited %d, %s", tc.name, code, mapScopedReclaimWant)
			}
		})
	}
}

// TestSelfHostMapScopedReclaimIRWasm is the wasm leg, where the same three
// shapes measured 82 pages before the site key.
func TestSelfHostMapScopedReclaimIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host scoped-map wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range mapScopedReclaimCases {
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
			watFile := filepath.Join(dir, "mapscoped_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			out, runErr := run.CombinedOutput()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q: %v\n%s", tc.name, runErr, out)
			}
			if code := run.ProcessState.ExitCode(); code != 0 {
				t.Errorf("scoped-map wasm IR %q = %d, %s\n%s", tc.name, code, mapScopedReclaimWant, out)
			}
		})
	}
}
