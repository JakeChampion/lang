package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func writeConcatFixture(t *testing.T, dir string) (string, int) {
	t.Helper()
	return writeConcatFixtureShape(t, dir, true)
}

func writeFlatConcatFixture(t *testing.T, dir string) (string, int) {
	t.Helper()
	return writeConcatFixtureShape(t, dir, false)
}

func writeConcatFixtureShape(t *testing.T, dir string, chained bool) (string, int) {
	t.Helper()
	const nMod, nFn = 7, 100
	proj := filepath.Join(dir, "concatproj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var imports, calls strings.Builder
	want := 0
	for m := 0; m < nMod; m++ {
		var lib strings.Builder
		if chained {
			for f := 0; f < nFn-1; f++ {
				fmt.Fprintf(&lib, "pub function m%d_f%d(x: i32): i32 { return m%d_f%d(x) + 1; }\n", m, f, m, f+1)
			}
			fmt.Fprintf(&lib, "pub function m%d_f%d(x: i32): i32 { let e: IoError = NotFound(\"p\"); match (e) { NotFound(_) => { return x; }, _ => { return 0 - 1; } } }\n", m, nFn-1)
			want += nFn
		} else {
			for f := 0; f < nFn; f++ {
				fmt.Fprintf(&lib, "pub function m%d_f%d(x: i32): i32 { return x + %d; }\n", m, f, m*nFn+f)
			}
			want += 1 + m*nFn
		}
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("lib%d.fern", m)), []byte(lib.String()), 0o644); err != nil {
			t.Fatalf("write lib%d: %v", m, err)
		}
		fmt.Fprintf(&imports, "import \"./lib%d\";\n", m)
		if m > 0 {
			calls.WriteString(" + ")
		}
		fmt.Fprintf(&calls, "lib%d.m%d_f0(1)", m, m)
	}
	entry := fmt.Sprintf("%s\nfunction main(): i32 {\n    let t: i32 = %s;\n    if (t == %d) { return 0; }\n    return 1;\n}\n",
		imports.String(), calls.String(), want)
	entryPath := filepath.Join(proj, "entry.fern")
	if err := os.WriteFile(entryPath, []byte(entry), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	return entryPath, nMod
}

func assertConcatProduced(t *testing.T, asm []byte) {
	t.Helper()
	if !regexp.MustCompile(`(?m)^\.S[A-Za-z_][A-Za-z0-9_]*_[0-9]+:`).Match(asm) {
		t.Fatalf("expected per-unit string pools (.S<ns>_<idx>) — the per-module concat; " +
			"got none, so the over-budget rescue did not engage")
	}
	if regexp.MustCompile(`(?m)^\.S[0-9]+:`).Match(asm) {
		t.Errorf("found whole-program string labels (.S<idx>) — this routed the merged " +
			"path, not the per-module concat")
	}
}

func buildConcatDriver(t *testing.T, gcc string) (string, string) {
	t.Helper()
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_modload_run.fern")
	return dir, buildSelfHostBin(t, gcc, dir, "drivers/asm_modload_run.fern", "mmr")
}

func TestSelfHostPerModuleConcatX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir, mmr := buildConcatDriver(t, gcc)
	entryPath, nMod := writeConcatFixture(t, dir)

	cnt, err := exec.Command(mmr, entryPath, "-per-module-count").Output()
	if err != nil {
		t.Fatalf("-per-module-count failed: %v", err)
	}
	if got := strings.TrimSpace(string(cnt)); got != fmt.Sprint(nMod+1) {
		t.Fatalf("-per-module-count = %q, want %d (entry + %d libs)", got, nMod+1, nMod)
	}

	asm, err := exec.Command(mmr, entryPath).Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("over-budget concat emit failed: %v (len=%d)", err, len(asm))
	}
	assertConcatProduced(t, asm)

	bin := buildBin(t, gcc, dir, "concat_prog", string(asm))
	rc := exec.Command(bin)
	_ = rc.Run()
	if code := rc.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("per-module concat program exited %d, want 0 (cross-unit calls miscompiled)", code)
	}
}

func TestSelfHostAsmLoadRunConcatX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "mmc")
	entryPath, _ := writeConcatFixture(t, dir)
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	asm, err := exec.Command(mmc, entryPath, stdlibRoot).Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("over-budget concat emit failed: %v (len=%d)", err, len(asm))
	}
	assertConcatProduced(t, asm)

	bin := buildBin(t, gcc, dir, "load_concat_prog", string(asm))
	rc := exec.Command(bin)
	_ = rc.Run()
	if code := rc.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("per-module concat program exited %d, want 0 (cross-unit calls miscompiled)", code)
	}
}

func TestSelfHostPerModuleConcatArm64(t *testing.T) {
	hostGcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the self-host driver is built for x86-64-linux, so it must run on a native x86-64 host")
	}
	armGcc, qemu := arm64Tooling(t)
	dir, mmr := buildConcatDriver(t, hostGcc)
	entryPath, _ := writeConcatFixture(t, dir)

	asm, err := exec.Command(mmr, entryPath, "-target", "arm64-linux").Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("arm64 over-budget concat emit failed: %v (len=%d)", err, len(asm))
	}
	assertConcatProduced(t, asm)

	bin := buildBin(t, armGcc, dir, "concat_prog_arm64", string(asm))
	rc := runArm64Bin(qemu, bin)
	_ = rc.Run()
	if code := rc.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("arm64 per-module concat program exited %d, want 0 (cross-unit calls miscompiled)", code)
	}
}
