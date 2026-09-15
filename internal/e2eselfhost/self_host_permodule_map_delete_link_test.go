package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostPerModuleArm64MapDeleteLinks is the fixture #9353 asks for: the
// per-module link gate is VACUOUS for a whole family of runtime needs, and this
// is the shape that shows it.
//
// `ircore.all_runtime_need_roots()` calls itself "the closed set of runtime-need
// ROOT names — every string the codegen ever passes to s.need(…)", and its own
// comment says what a gap costs: "If a NEW .need(\"x\") root is added without
// listing it here, a module using x links against an undefined __fern_x — caught
// immediately by the per-module link test". Five names were missing, and the
// per-module link test did not catch them, because no existing fixture reaches
// one from a LIBRARY module.
//
// The mechanism, which is what this test pins rather than the five names:
//
//   - `-per-module-needs` returns the static root list (asm_modload_run.fern),
//     not an exact union — aggregating the real union re-emits every module and
//     OOMs a self-host-built driver (#3425).
//   - the driver applies those roots as extra_needs to the ENTRY unit only
//     (`if (parts[pm_index].is_entry) { extra = pm_extra; }`).
//   - so a non-entry unit emits `bl __fn___fern_map_delete` for `m.without(k)`
//     while the entry, seeded from a list that omitted `map_delete`, never
//     emits the body. Nothing defines it and the link fails.
//
// Hence the delete lives in `leaf.fern`, not in `main.fern`. Putting it in the
// entry would pass whether or not the root is listed, which is exactly how this
// went unnoticed.
func TestSelfHostPerModuleArm64MapDeleteLinks(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)

	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_modload_run.fern", "mapdellinkdriver")

	proj := t.TempDir()
	// A library module whose only unusual reach is the map delete. `without`
	// returns (Map, boolean); the boolean is what makes it a value-returning
	// delete rather than the removed in-place spelling.
	mustWrite(t, proj, "leaf.fern", `import "core/map";

pub function leaf_val(): i32 {
  var m: Map[string, i32] = map_new(4);
  m = m.insert("a", 40);
  m = m.insert("b", 99);
  var d: (Map[string, i32], boolean) = m.without("b");
  m = d.0;
  return m.get_or("a", 0) + m.len();
}
`)
	mustWrite(t, proj, "main.fern", `import "./leaf";

function main(): i32 { return leaf.leaf_val(); }
`)
	entry := filepath.Join(proj, "main.fern")

	drive := func(args ...string) string {
		t.Helper()
		out, err := runX86_64Bin(x86runner, driverBin, append([]string{entry, "-target", "arm64-linux"}, args...)...).Output()
		if err != nil {
			t.Fatalf("driver %v: %v", args, err)
		}
		return string(out)
	}

	n, err := strconv.Atoi(strings.TrimSpace(drive("-per-module-count")))
	if err != nil || n < 2 {
		t.Fatalf("-per-module-count = %q (n=%d), want >=2", drive("-per-module-count"), n)
	}

	needs := drive("-per-module-needs")
	var needArgs []string
	for _, ln := range strings.Split(needs, "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			needArgs = append(needArgs, "-extra-need", s)
		}
	}
	// The root list is the entry's whole seed, so map_delete being in it is the
	// precondition for the link below. Asserted directly: without this the
	// failure reads as a mysterious undefined symbol rather than a missing row.
	if !strings.Contains(needs, "map_delete") {
		t.Errorf("-per-module-needs does not list map_delete, so no unit will define __fern_map_delete (#9353)")
	}

	var objs []string
	sawCall := false
	for i := 0; i < n; i++ {
		unit := drive(append([]string{"-per-module-emit", strconv.Itoa(i)}, needArgs...)...)
		if len(unit) == 0 {
			t.Fatalf("module %d emitted 0 bytes", i)
		}
		if strings.Contains(unit, "bl __fn___fern_map_delete") {
			sawCall = true
		}
		objs = append(objs, mustWrite(t, proj, "u"+strconv.Itoa(i)+".s", unit))
	}
	// If nothing calls it the test proves nothing, so this is a gate on the
	// fixture itself, not on the compiler.
	if !sawCall {
		t.Fatal("no unit emitted a call to __fn___fern_map_delete — the fixture no longer reaches the map-delete runtime, so it cannot catch a missing root")
	}

	bin := filepath.Join(proj, "prog")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", bin)...)
	if lout, err := exec.Command(armgcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("per-module arm64 link failed — a runtime need reached from a library module has no definer (#9353): %v\n%s", err, lout)
	}
	rcmd := runArm64Bin(qemu, bin)
	_ = rcmd.Run()
	if code := rcmd.ProcessState.ExitCode(); code != 41 {
		t.Errorf("per-module arm64 map-delete program exited %d, want 41", code)
	}
	_ = os.Remove(bin)
}
