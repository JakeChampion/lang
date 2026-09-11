// `statfs` (#9062) end to end on every backend that provides it.
//
// The numbers are whatever filesystem the runner is on, so the probe is
// handed the ones the harness read through Go and compares them. That is the
// check with teeth: every way this helper can be wrong produces a
// plausible-looking record. Reading Linux's `struct statfs` one word early
// puts f_type in `block_size` and f_blocks in `blocks_free`; taking
// `blocks_free` for `blocks_avail` hides the superuser reserve that `df`
// exists to show; and Darwin's u32 f_bsize loaded as 64 bits drags f_iosize
// in beside it.
//
// The block size and the two limits are compared exactly, since none of them
// moves under a mounted filesystem. The counts are compared for ORDER
// instead — blocks get used while the test runs — but the nesting
// (available <= free <= total) breaks long before the bounds do when a record
// is read at the wrong offset.
//
// wasm has no leg: neither preview has a volume interface, so the `fsinfo`
// capability withholds the builtin.
package e2e

import (
	"fmt"
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/platforms"
)

// statfsProbe returns the probe source for `dir` plus the directory to run it
// in. Each failing step returns its own code.
func statfsProbe(t *testing.T, dir string) string {
	t.Helper()
	blockSize, nameMax, pathMax := hostFsFacts(t, dir)
	return fmt.Sprintf(`function main(): i32 {
    match (statfs(%[1]q)) {
        Ok(fs) => {
            if (fs.block_size != (%[2]d as i64)) { return 1; }
            if (fs.name_max != (%[3]d as i64)) { return 2; }
            if (fs.path_max != (%[4]d as i64)) { return 3; }
            if (fs.blocks < (1 as i64)) { return 4; }
            if (fs.blocks_free > fs.blocks) { return 5; }
            if (fs.blocks_avail > fs.blocks_free) { return 6; }
            if (fs.files_free > fs.files) { return 7; }
        },
        Err(_) => { return 8; }
    }
    // A path that does not resolve is an Err, not a zero-filled record.
    match (statfs(%[5]q)) {
        Ok(_) => { return 9; },
        Err(_) => {}
    }
    return 0;
}
`, dir, blockSize, nameMax, pathMax, dir+"/no-such-directory-here/x")
}

func TestX86_64Statfs(t *testing.T) {
	src := statfsProbe(t, t.TempDir())
	if code, out := compileRunX86_64WithSetup(t, src, nil); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see statfsProbe)\n%s", code, out)
	}
}

func TestArm64Statfs(t *testing.T) {
	out, code := compileAndRunArm64(t, statfsProbe(t, t.TempDir()))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see statfsProbe)\n%s", code, out)
	}
}

func TestArm64SSAStatfs(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()
	bin := compileArm64SSA(t, fern, statfsProbe(t, dir), os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, dir, os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see statfsProbe)\n%s", code, stderr)
	}
}

func TestInterpStatfs(t *testing.T) {
	if code := runInterpExit(t, statfsProbe(t, t.TempDir())); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see statfsProbe)", code)
	}
}

// Both wasm worlds refuse it. A zero-filled record would claim a filesystem
// with no blocks and no name length — a measurement a component never took —
// so the honest answer is the compile-time refusal.
func TestWASMStatfsRefused(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 {
    match (statfs(".")) { Ok(_) => { return 0; }, Err(_) => { return 1; } }
}
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 {
			t.Errorf("%s accepted statfs; it has no volume to measure", target)
			continue
		}
		if vs[0].Builtin != "statfs" || vs[0].Capability != "fsinfo" {
			t.Errorf("%s refused %q on %q, want statfs on fsinfo", target, vs[0].Builtin, vs[0].Capability)
		}
	}
}
