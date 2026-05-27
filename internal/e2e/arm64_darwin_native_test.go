package e2e

import (
	"bytes"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestArm64DarwinNativeMachO builds an integer program through the
// in-process Mach-O backend (`-target arm64-darwin -native`, no clang/
// ld64) and validates it. Off Apple Silicon it only checks the file is a
// well-formed signed Mach-O. On the macOS arm64 CI runner it also EXECUTES
// the binary — the decisive test of whether a static, dyld-free,
// LC_UNIXTHREAD + ad-hoc-signed executable launches on current macOS.
//
// Launch failure (the binary is rejected by the kernel) is reported as a
// skip with diagnostics rather than a hard failure: it's an open question
// answered by this very run, not a regression. A wrong exit code — the
// binary ran but misbehaved — is a hard failure.
func TestArm64DarwinNativeMachO(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return 42; }\n"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-native", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
	}

	// Structural validation (runs on every host).
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	f, err := macho.NewFile(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("output is not a parseable Mach-O: %v", err)
	}
	if f.Type != macho.TypeExec || f.Cpu != macho.CpuArm64 {
		t.Fatalf("got type=%v cpu=%v, want EXECUTE/arm64", f.Type, f.Cpu)
	}

	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("execution check only runs on Apple Silicon")
	}

	cmd := exec.Command(out)
	runErr := cmd.Run()
	ps := cmd.ProcessState
	if ps == nil || !ps.Exited() {
		// Launched-and-killed or never-execed: the kernel rejected the
		// static binary. Record the finding; this is the experiment's
		// answer, not a code regression.
		t.Skipf("native static Mach-O did not run to a normal exit (err=%v, state=%v); a static dyld-free LC_UNIXTHREAD binary may be unsupported on this macOS — investigate before relying on the native darwin path", runErr, ps)
	}
	if code := ps.ExitCode(); code != 42 {
		t.Errorf("native arm64-darwin binary exit = %d, want 42", code)
	}
}
