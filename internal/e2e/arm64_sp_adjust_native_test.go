package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	nativearm64 "github.com/jakechampion/lang/internal/native/arm64"
	nativeelf "github.com/jakechampion/lang/internal/native/elf"
)

// TestArm64LargeFrameNativeAsm guards issue #3598 through the PURE-GO native
// assembler specifically. A function whose frame exceeds the 4095-byte add/sub
// immediate range adjusts SP with the REGISTER form (`sub sp, sp, x16` in the
// prologue, `add sp, sp, x16` in the epilogue). Register 31 means SP only in
// the EXTENDED-register encoding; the native assembler had emitted the
// shifted-register form, where 31 is XZR — so `sub sp, sp, x16` silently
// assembled as `sub xzr, xzr, x16` (`neg xzr, x16`), a no-op. The frame was
// never allocated, the operand-stack push/pops overran the locals, and the
// arm64-built self-host compiler segfaulted layout-sensitively.
//
// TestArm64LargeFrame exercises the same source but compiles via gcc by
// default (which encodes the register form correctly on its own), so it does
// not catch this. Here we drive the native assemble+link path explicitly.
func TestArm64LargeFrameNativeAsm(t *testing.T) {
	_, qemu := arm64Tooling(t)
	src := arm64LargeFrameSrc() // 800 i64 locals (~6400B frame) summed; returns 0 iff correct

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
	asm, err := arm64codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	// The large frame must use the register-form SP adjust, else the test
	// would pass trivially via the immediate form and not exercise the fix.
	if !strings.Contains(asm, "sub sp, sp, x") {
		t.Fatalf("expected a register-form frame adjust (sub sp, sp, x..) for a >4095B frame; got none")
	}

	text, rodata, err := nativearm64.AssembleProgram(asm, nativeelf.TextVAddr)
	if err != nil {
		t.Fatalf("native assemble: %v", err)
	}
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(binPath, nativeelf.StaticExecutableData(text, rodata), 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	cmd := runArm64Bin(qemu, binPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("large-frame arm64 native-asm exit = %d, want 0 (non-zero => frame not allocated / locals corrupted)", code)
	}
}
