package e2e

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func statfsProbe(t *testing.T, dir string) string {
	t.Helper()
	blockSize, nameMax, pathMax := hostFsFacts(t, dir)
	fsType, fsid, fragSize := hostFsIdentity(t, dir)
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
            if (fs.fs_type != %[6]di64) { return 10; }
            if (fs.fsid != %[7]di64) { return 11; }
            if (fs.frag_size != %[8]di64) { return 12; }
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
`, dir, blockSize, nameMax, pathMax, dir+"/no-such-directory-here/x", fsType, fsid, fragSize)
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

func TestInterpStatfs(t *testing.T) {
	if code := runInterpExit(t, statfsProbe(t, t.TempDir())); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see statfsProbe)", code)
	}
}

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
