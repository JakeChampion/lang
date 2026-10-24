package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostMapVerbsIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "mmc")

	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	cases := []struct {
		name      string
		src       string
		wantClone string
	}{
		{
			name:      "i32_keys",
			wantClone: "__mapm_merge__i32__i32",
			src: `import "core/map";
function main(): i32 {
    let a: Map[i32, i32] = map_new(4);
    a = a.insert(1, 10);
    a = a.insert(2, 20);
    let b: Map[i32, i32] = map_new(4);
    b = b.insert(2, 99);
    b = b.insert(3, 30);
    let m: Map[i32, i32] = a.merge(b);
    if (m.len() != 3) { return 1; }
    if (m.get_or(1, 0) != 10) { return 2; }
    if (m.get_or(2, 0) != 99) { return 3; }
    let r: (Map[i32, i32], i32) = m.get_or_insert(5, 50);
    if (r.1 != 50) { return 5; }
    if (r.0.get_or(5, 0) != 50) { return 6; }
    // chained map op directly on the merge result (no intermediate var)
    if (a.merge(b).get_or(3, 0) != 30) { return 7; }
    return 0;
}`,
		},
		{
			name:      "string_keys",
			wantClone: "__mapm_merge__string__i32",
			src: `import "core/map";
function main(): i32 {
    let a: Map[string, i32] = map_new(4);
    a = a.insert("x", 1);
    let b: Map[string, i32] = map_new(4);
    b = b.insert("y", 2);
    let m: Map[string, i32] = a.merge(b);
    if (m.get_or("x", 0) != 1) { return 1; }
    if (m.get_or("y", 0) != 2) { return 2; }
    return 0;
}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog := filepath.Join(dir, "mapverbs_"+tc.name+".fern")
			if err := os.WriteFile(prog, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write program: %v", err)
			}
			if got := strings.TrimSpace(runDriverDecide(t, mmc, prog, stdlibRoot)); got != "ir" {
				t.Fatalf("map-verbs program routed %q, want \"ir\" (generic map methods must monomorphise)", got)
			}
			asm, err := exec.Command(mmc, prog, stdlibRoot).Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("self-host compile failed: %v", err)
			}
			if !strings.Contains(string(asm), tc.wantClone) {
				t.Fatalf("asm missing the monomorphised clone %q (the fold didn't fire)", tc.wantClone)
			}
			bin := buildBin(t, gcc, dir, "mapverbs_"+tc.name, string(asm))
			rc := exec.Command(bin)
			_ = rc.Run()
			if code := rc.ProcessState.ExitCode(); code != 0 {
				t.Errorf("map-verbs IR program exited %d, want 0 (generic map method miscompiled)", code)
			}
		})
	}
}
