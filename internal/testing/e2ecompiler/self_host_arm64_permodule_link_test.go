package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostPerModuleArm64LeafOnlyLinkRun is the regression guard for #4305:
// in an arm64 per-module build, a runtime helper's string-literal references
// must use the same per-module-namespaced label as their definitions
// (str_lit_label → .S<ns>_<idx>; byte-identical .S<idx> on the merged path,
// where str_ns==""). A mismatch fails a leaf-only program — whose library
// modules emit no bare .S<idx> to satisfy the reference by accident — at link
// time with "undefined reference to .S0".
//
// This drives that shape: a main → mid → leaf tree with NO library string
// literals, whose entry emits the whole runtime (arr_str_join / str_lines pull
// in the .S0/.S1 constants). It must link with the aarch64 cross gcc and run to
// exit 42, natively or under qemu-aarch64.
func TestSelfHostPerModuleArm64LeafOnlyLinkRun(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)

	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_modload_run.fern", "arm64linkdriver")

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "leaf.fern"),
		[]byte("pub function leaf_val(): i32 { return 40; }\n"), 0o644); err != nil {
		t.Fatalf("write leaf.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(proj, "mid.fern"), []byte(
		"import \"./leaf\";\npub function mid_val(): i32 { return leaf.leaf_val() + 2; }\n"), 0o644); err != nil {
		t.Fatalf("write mid.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(proj, "main.fern"), []byte(
		"import \"./mid\";\nfunction main(): i32 { return mid.mid_val(); }\n"), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
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
	if err != nil || n < 3 {
		t.Fatalf("-per-module-count = %q (n=%d), want >=3", drive("-per-module-count"), n)
	}
	var needArgs []string
	for _, ln := range strings.Split(drive("-per-module-needs"), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			needArgs = append(needArgs, "-extra-need", s)
		}
	}

	var objs []string
	for i := 0; i < n; i++ {
		unit := drive(append([]string{"-per-module-emit", strconv.Itoa(i)}, needArgs...)...)
		if len(unit) == 0 {
			t.Fatalf("module %d emitted 0 bytes", i)
		}
		// No bare `.S<idx>` reference may survive — every string reference must be
		// namespaced (this is the #4305 fix; a bare ref is what failed to link).
		for _, ln := range strings.Split(unit, "\n") {
			ln = strings.TrimSpace(ln)
			if strings.HasPrefix(ln, "adrp ") && strings.Contains(ln, ", .S") {
				lbl := ln[strings.Index(ln, ", .S")+2:]
				if !strings.Contains(lbl, "_") { // ".S<ns>_<idx>" has an underscore; bare ".S<idx>" does not
					t.Errorf("module %d: bare string-literal reference %q survived (#4305)", i, lbl)
				}
			}
		}
		p := filepath.Join(proj, "u"+strconv.Itoa(i)+".s")
		if err := os.WriteFile(p, []byte(unit), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		objs = append(objs, p)
	}

	bin := filepath.Join(proj, "prog")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", bin)...)
	if lout, err := exec.Command(armgcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("per-module arm64 link failed (#4305 regression — undefined .S<idx>): %v\n%s", err, lout)
	}
	rcmd := runArm64Bin(qemu, bin)
	_ = rcmd.Run()
	if code := rcmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("per-module arm64 leaf-only program exited %d, want 42", code)
	}
}
