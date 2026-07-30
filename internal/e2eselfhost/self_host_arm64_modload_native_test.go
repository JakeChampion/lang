package e2eselfhost

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// arm64ImageSpan is the AArch64 b/bl reach in bytes: a signed 26-bit
// instruction offset, ±2^25 instructions = ±128 MB. A .text bigger than
// this needs the native assembler's branch veneers
// (internal/native/arm64/veneer.go) to link at all.
const arm64ImageSpan = 1 << 27

// TestSelfHostArm64ModloadNativeBuild builds the per-module orchestrator
// driver — the largest program in the tree — for arm64 through the CLI's
// default in-process pure-Go assembler and linker, then runs it.
//
// This is the lane that was missing when the self-host arm64 image passed
// the ±128 MB b/bl span in mid-2026 and the driver stopped assembling
// ("branch to ... spans 33891495 instructions"). CI was fully green on
// the commit that tipped it: the x86 self-host shards build this driver
// only as a host binary, and the aarch64 shards do not build it at all.
// Nothing needs an aarch64 toolchain to catch it — the assembler and
// linker are pure Go — so this test runs on every shard, and only the
// final execution check waits for qemu.
//
// It is a heavy build (a ~130 MB image, several GB of RSS), so it takes a
// reservation against the harness RAM budget the same way a cold driver
// build does.
func TestSelfHostArm64ModloadNativeBuild(t *testing.T) {
	fernBin := buildFernCLIBin(t)
	dir := writeSelfHostModloadProject(t)
	entry := filepath.Join(dir, "asm_modload_run.fern")
	orch := filepath.Join(dir, "orch_arm64")

	if err := withBuildMemoryMB(arm64ImageBuildMB, func() error {
		out, err := exec.Command(fernBin, "-target", "arm64", "-o", orch, entry).CombinedOutput()
		if err != nil {
			return &buildErr{err: err, out: out}
		}
		return nil
	}); err != nil {
		t.Fatalf("arm64 build of the per-module orchestrator failed: %v", err)
	}

	raw, err := os.ReadFile(orch)
	if err != nil {
		t.Fatalf("read image: %v", err)
	}
	f, err := elf.NewFile(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("output is not a parseable ELF: %v", err)
	}
	if f.Machine != elf.EM_AARCH64 || f.Type != elf.ET_EXEC {
		t.Fatalf("got machine=%v type=%v, want AARCH64/EXEC", f.Machine, f.Type)
	}
	var code uint64
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD && p.Flags&elf.PF_X != 0 {
			code = p.Memsz
		}
	}
	if code == 0 {
		t.Fatal("no executable PT_LOAD segment in the image")
	}
	// Not an assertion — a report. The span stopped being a ceiling when
	// veneers landed, but it is still the number that explains a jump in
	// image size, and it says whether this build is exercising the veneer
	// path or merely the ordinary one.
	if code > arm64ImageSpan {
		t.Logf("code segment %d bytes — %d past the ±128 MB b/bl span, so this build is veneered", code, code-arm64ImageSpan)
	} else {
		t.Logf("code segment %d bytes — %d below the ±128 MB b/bl span", code, arm64ImageSpan-code)
	}

	// An image that links proves nothing about a veneer: the branched-to
	// code has to actually run. Compile a program with the arm64-hosted
	// driver and check the asm it emits. Only this half needs an
	// emulator — the build above is the part that must run everywhere.
	t.Run("executes", func(t *testing.T) {
		qemu := qemuAarch64(t)
		prog := filepath.Join(dir, "prog.fern")
		if err := os.WriteFile(prog, []byte("function main(): i32 { return 42; }\n"), 0o644); err != nil {
			t.Fatalf("write program: %v", err)
		}
		cmd := runArm64Bin(qemu, orch, prog, "-target", "arm64")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		asm, err := cmd.Output()
		if err != nil {
			t.Fatalf("running the arm64 orchestrator: %v\n%s", err, stderr.String())
		}
		// Assembled and run, not just inspected: a veneer that lands
		// anywhere but its target still produces plausible-looking asm
		// from a compiler that crashed or wandered mid-emit.
		compiled := filepath.Join(dir, "prog.bin")
		if err := nativeLinkArm64(string(asm), compiled); err != nil {
			t.Fatalf("linking the orchestrator's output: %v", err)
		}
		run := runArm64Bin(qemu, compiled)
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 42 {
			t.Fatalf("program built by the arm64 orchestrator exited %d, want 42", got)
		}
	})
}

// qemuAarch64 returns the emulator that runs arm64 binaries, or "" on a
// host that runs them directly. Unlike arm64Tooling it does not demand a
// cross gcc: the pure-Go assembler and linker need no toolchain.
func qemuAarch64(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		return ""
	}
	for _, c := range []string{"qemu-aarch64", "qemu-aarch64-static"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	t.Skip("no qemu-aarch64 to run arm64 binaries")
	return ""
}

// arm64ImageBuildMB is this build's estimated peak RSS. The emit of a
// ~130 MB image runs without the harness's in-process soft heap cap (it
// is a `fern` subprocess), and was measured peaking at ~8.3 GB.
const arm64ImageBuildMB = 8500

// buildErr carries a failed build's combined output into the test's
// failure message.
type buildErr struct {
	err error
	out []byte
}

func (e *buildErr) Error() string { return e.err.Error() + "\n" + string(e.out) }

// buildFernCLIBin compiles cmd/fern to a temp binary. The Go build cache
// makes the repeated build essentially free.
func buildFernCLIBin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fern")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}
	return bin
}
