package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mapValueAliasRetainIRCases pin the retain a map insert owes an ALIASED array
// VALUE on the register backends (#6880).
//
// The register __fern_map_set stores the value pointer with no rc-inc of its
// own, while the exit dec-sweep releases EVERY array slot unconditionally. So
// `var a = [...]; m.insert(k, a);` left the map's value column naming a buffer
// the sweep freed, and the next allocation of that size class handed the block
// out again underneath the map — a read of the value then sees the recycled
// block's contents, and nothing reports an over-release because the map's alias
// was never counted at all. The lowering now records the verdict (op_map_set's
// vretain bit) and both register backends retain, mirroring the native oracle's
// emitMapSetValueRetain: alias shapes only, arrays only — a fresh value
// transfers its sole rc=1 to the map and retaining one would leak.
//
// Each program forces the recycle itself (a same-size-class array literal
// allocated between the insert and the read) rather than relying on a size
// class that happens to collide: as found, the `Map[string, string[]]` column
// in url_codec only misread once __fern_map_get's Option box moved to a 32-byte
// class, and reads clean at 40. Exit 0 is correct; each nonzero code names the
// check that failed.
//
// The x86-64 and arm64 legs are the ones that fail without the retain (three of
// the four cases each, the fourth being the fresh-value control). The wasm leg
// is a PARITY gate, not a failing-before one: $__fern_map_set already retained
// every `vis` value it was not told to consume, which is the divergence this
// closes.
var mapValueAliasRetainIRCases = []struct {
	name string
	src  string
}{
	// The reduced #6880 shape: a helper builds the value array and returns the
	// map, so the helper's exit sweep is what freed the stored buffer.
	{"helper-insert", `function put(m: Map[string, i32[]], k: string, a: i32, b: i32): Map[string, i32[]] {
    var arr: i32[] = [a, b];
    return m.insert(k, arr);
}

function main(): i32 {
    var m: Map[string, i32[]] = Map {};
    m = put(m, "k", 3, 4);
    var junk: i32[] = [7, 9];
    match (m.get("k")) {
        Some(v) => {
            if (v.len() != 2) { return 2; }
            if (v[0] != 3) { return 3; }
            if (v[1] != 4) { return 4; }
        },
        None => { return 1; }
    }
    if (junk[0] != 7) { return 5; }
    return 0;
}
`},
	// Loop-scoped value locals — std/url's query_parse shape, where each round's
	// array is freed at the iteration's sweep and the next round recycles it.
	{"loop-scoped-value", `function main(): i32 {
    var m: Map[i32, i32[]] = Map {};
    var i: i32 = 0;
    while (i < 6) {
        var arr: i32[] = [i, i + 100];
        m = m.insert(i, arr);
        i = i + 1;
    }
    var j: i32 = 0;
    while (j < 6) {
        match (m.get(j)) {
            Some(v) => {
                if (v.len() != 2) { return 2; }
                if (v[0] != j) { return 3; }
                if (v[1] != j + 100) { return 4; }
            },
            None => { return 1; }
        }
        j = j + 1;
    }
    return 0;
}
`},
	// The reported column type: Map[string, string[]].
	{"string-array-value", `function put(m: Map[string, string[]], k: string, v: string): Map[string, string[]] {
    var a: string[] = [v];
    return m.insert(k, a);
}

function main(): i32 {
    var m: Map[string, string[]] = Map {};
    m = put(m, "k", "hello");
    var junk: string[] = ["zz"];
    match (m.get("k")) {
        Some(v) => {
            if (v.len() != 1) { return 2; }
            if (v[0] != "hello") { return 3; }
        },
        None => { return 1; }
    }
    if (junk[0] != "zz") { return 4; }
    return 0;
}
`},
	// Control: a FRESH value literal takes no retain — its sole rc=1 moves into
	// the map — and must still read back correctly. Passes either side of the
	// fix, which is its job.
	{"fresh-value-control", `function main(): i32 {
    var m: Map[i32, i32[]] = Map {};
    m = m.insert(1, [3, 4]);
    var junk: i32[] = [7, 9];
    match (m.get(1)) {
        Some(v) => {
            if (v[0] != 3) { return 2; }
            if (v[1] != 4) { return 3; }
        },
        None => { return 1; }
    }
    if (junk[0] != 7) { return 4; }
    return 0;
}
`},
}

// TestSelfHostMapValueAliasRetainIRX86_64 runs each case through the self-hosted
// x86-64 IR driver, pinned to the "ir" path.
func TestSelfHostMapValueAliasRetainIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range mapValueAliasRetainIRCases {
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
			progBin := buildBin(t, gcc, dir, "mvar_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s exited %d, want 0 — the map's value column read back wrong", tc.name, code)
			}
		})
	}
}

// TestSelfHostMapValueAliasRetainIRArm64 is the arm64 leg: same programs, the
// arm64 map_set emission, run under qemu.
func TestSelfHostMapValueAliasRetainIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapValueAliasRetainIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "mvar_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s exited %d, want 0 — the map's value column read back wrong", tc.name, code)
			}
		})
	}
}

// TestSelfHostMapValueAliasRetainIRWasm is the parity leg: $__fern_map_set has
// always retained a `vis` value, so these pass either side of the fix and pin
// that the register backends now agree with it.
func TestSelfHostMapValueAliasRetainIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map-value-alias-retain wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range mapValueAliasRetainIRCases {
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
			watFile := filepath.Join(dir, "mapvaluealias_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			out, runErr := run.CombinedOutput()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q: %v\n%s", tc.name, runErr, out)
			}
			if code := run.ProcessState.ExitCode(); code != 0 {
				t.Errorf("map-value-alias-retain wasm IR %q = %d, want 0\n%s", tc.name, code, out)
			}
		})
	}
}
