package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMapStructFieldIRX86_64 verifies that a struct with a Map field
// (`m: Map[string, i32]`) is admitted to the IR path and that the map round-trips
// through the field. Maps are leak-only on the IR path (the exit sweep tracks
// local_is_arr, not local_map_type — a map box is never freed), so a map-typed
// field leaks with the struct like a string / Option / tuple / enum field: no RC,
// no aliasing bail. Reading the field into a `var got: Map[K, V] = c.m` local
// re-marks the map type from the annotation so get_or dispatches as a map op.
//
// f builds mm{"a": 3}, stores it in Cache{m: mm, n: 4}, reads c.m back, and
// returns c.m["a"] + c.n = 3 + 4 = 7. Without map-field support Cache is not
// leaf-safe and the whole module bails to the ~35 KB AST runtime; with it the IR
// output is small — so the size check proves admission, the exit code the
// round-trip.
func TestSelfHostMapStructFieldIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	prog := `struct Cache { m: Map[string, i32], n: i32 }
function f(): i32 {
    var mm: Map[string, i32] = map_new(0);
    mm = mm.insert("a", 3);
    var c: Cache = Cache { m: mm, n: 4 };
    var got: Map[string, i32] = c.m;
    return got.get_or("a", 0) + c.n;
}
function main(): i32 { return f(); }`
	asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
	// The cap distinguishes IR admission from the ~35 KB AST-runtime bail. It
	// sits comfortably below that floor with margin for legitimate IR growth:
	// map-helper additions on the IR path (e.g. the struct/enum-key eq dispatch
	// in #4037) push this module's IR output a little each time, and a too-tight
	// cap turns into a false failure on an unrelated merge. 30000 still fails
	// loudly on a real bail (which lands near 35 KB) without that brittleness.
	if len(asm) == 0 || len(asm) > 33000 {
		t.Fatalf("asm is %d bytes — expected IR output (with map helpers); the map-field module likely bailed to the AST runtime", len(asm))
	}
	progBin := buildBin(t, gcc, dir, "map_struct_field", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 7 {
		t.Errorf("exit %d, want 7 (c.m[\"a\"] + c.n)", code)
	}
}
