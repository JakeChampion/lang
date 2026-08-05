package e2eharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// nativeLinkX86 is the default assemble+link for driver builds and huge
// CachedLink inputs. This gate proves it is behaviour-equivalent to the
// gcc toolchain path it replaced: the same emitted asm, linked both ways,
// runs with identical stdout and exit code. (The driver-scale input is
// exercised end-to-end by every TestSelfHost* build; this covers the
// contract with a fast case that also runs on toolchain-less shards up
// to the gcc half.)
func TestNativeLinkX86MatchesGccLink(t *testing.T) {
	t.Parallel()
	// env() is load-bearing here: the natively-linked drivers read
	// FERN_CACHE_DIR / FERN_SELFHOST_NO_REUSE via env(), and the native
	// assembler once mis-encoded __fern_env's `cmp byte ptr [rdi], 61`
	// ('=' scan) as a dword compare, so env() always returned None on the
	// native path only (TestEncodeAluImmSize pins the encoding; this pins
	// the behaviour end-to-end).
	src := `function main(): i32 {
    var parts: string[] = ["fern", "native", "link"];
    var joined: string = "";
    for p in parts {
        joined = joined + p + ".";
    }
    print(joined);
    var total: i32 = 0;
    for i in 0..10 {
        total = total + i;
    }
    if (joined.len() > 0) {
        total = total + 3;
    }
    match (env("FERN_NATIVELINK_PROBE")) {
        Some(v) => { if (v == "on") { total = total + 7; } },
        None => { }
    }
    return total; // 45 + 3 + 7
}
`
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	nativeBin := filepath.Join(dir, "prog_native")
	if err := nativeLinkX86(asm, nativeBin); err != nil {
		t.Fatalf("nativeLinkX86: %v", err)
	}

	gcc, runner := X86_64Tooling(t) // skips when no gcc / runner for this host
	run := func(bin string) (string, int) {
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
		}
		cmd.Env = append(os.Environ(), "FERN_NATIVELINK_PROBE=on")
		out, _ := cmd.CombinedOutput()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatalf("%s did not exit normally (output: %q)", bin, out)
		}
		return string(out), cmd.ProcessState.ExitCode()
	}

	asmPath := filepath.Join(dir, "prog.s")
	gccBin := filepath.Join(dir, "prog_gcc")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", gccBin).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}

	gccOut, gccCode := run(gccBin)
	natOut, natCode := run(nativeBin)
	if natCode != gccCode || natOut != gccOut {
		t.Fatalf("native-linked binary diverged from gcc-linked:\n gcc: exit=%d out=%q\n native: exit=%d out=%q",
			gccCode, gccOut, natCode, natOut)
	}
	if gccCode != 55 || !strings.Contains(gccOut, "fern.native.link.") {
		t.Fatalf("unexpected program behaviour: exit=%d out=%q", gccCode, gccOut)
	}
}

// The arm64 sibling of TestNativeLinkX86MatchesGccLink: the same program
// (env() included — see the x86 test's comment for why it is load-bearing)
// emitted by the arm64 backend, linked by nativeLinkArm64 and by aarch64
// gcc, must run with identical stdout and exit code. Skips without the
// aarch64 toolchain; CI's arm64 lane runs it natively.
func TestNativeLinkArm64MatchesGccLink(t *testing.T) {
	t.Parallel()
	gcc, qemu := Arm64Tooling(t) // skips when the aarch64 toolchain is absent
	src := `function main(): i32 {
    var parts: string[] = ["fern", "native", "link"];
    var joined: string = "";
    for p in parts {
        joined = joined + p + ".";
    }
    print(joined);
    var total: i32 = 0;
    for i in 0..10 {
        total = total + i;
    }
    if (joined.len() > 0) {
        total = total + 3;
    }
    match (env("FERN_NATIVELINK_PROBE")) {
        Some(v) => { if (v == "on") { total = total + 7; } },
        None => { }
    }
    return total; // 45 + 3 + 7
}
`
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := arm64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	nativeBin := filepath.Join(dir, "prog_native")
	if err := nativeLinkArm64(asm, nativeBin); err != nil {
		t.Fatalf("nativeLinkArm64: %v", err)
	}
	gccBin := BuildBinArm64(t, gcc, dir, "prog_gcc_src", asm) // small asm → gcc path

	run := func(bin string) (string, int) {
		cmd := RunArm64Bin(qemu, bin)
		cmd.Env = append(os.Environ(), "FERN_NATIVELINK_PROBE=on")
		out, _ := cmd.CombinedOutput()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatalf("%s did not exit normally (output: %q)", bin, out)
		}
		return string(out), cmd.ProcessState.ExitCode()
	}
	gccOut, gccCode := run(gccBin)
	natOut, natCode := run(nativeBin)
	if natCode != gccCode || natOut != gccOut {
		t.Fatalf("arm64 native-linked binary diverged from gcc-linked:\n gcc: exit=%d out=%q\n native: exit=%d out=%q",
			gccCode, gccOut, natCode, natOut)
	}
	if gccCode != 55 || !strings.Contains(gccOut, "fern.native.link.") {
		t.Fatalf("unexpected program behaviour: exit=%d out=%q", gccCode, gccOut)
	}
}
