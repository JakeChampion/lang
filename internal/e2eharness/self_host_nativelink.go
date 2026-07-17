// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3).
package e2eharness

import (
	"fmt"
	"os"

	nativeelf "github.com/jakechampion/lang/internal/native/elf"
	nativex86 "github.com/jakechampion/lang/internal/native/x86_64"
)

// nativeLinkMinAsmBytes is the size above which CachedLink routes a
// SELF-HOST-emitted `.s` through the in-process native assembler instead
// of gcc. Small program links stay on gcc/bfd — they're milliseconds and
// keep the external-toolchain path exercised by the bulk of the suite.
// Only the huge links (the stage-2 self-compile of the whole compiler,
// ~450 MB of asm) cross this line; on those, GNU `as` alone peaks ~4.7 GB
// RSS for ~36 s where the native assembler needs ~2.2 GB for ~25 s — and
// the native path never writes the `.s` to disk at all.
const nativeLinkMinAsmBytes = 8 << 20

// nativeLinkX86 assembles GAS-Intel-syntax asm text and wraps it in a
// static ELF executable entirely in-process — the same
// AssembleProgramWX + StaticExecutableDataX86WX pipeline `cmd/fern`
// uses by default for `-target x86-64` (linkNativeX86 in main.go). No
// external assembler or linker runs, so the multi-GB GNU `as` peak and
// the on-disk `.s` scratch both disappear. An unsupported instruction
// surfaces as a clear error (never a miscompile); callers fall back to
// the gcc path on error.
func nativeLinkX86(asm, binPath string) error {
	text, rodata, err := nativex86.AssembleProgramWX(asm, nativeelf.TextVAddrWX)
	if err != nil {
		return fmt.Errorf("native assemble: %w", err)
	}
	bin := nativeelf.StaticExecutableDataX86WX(text, rodata)
	if err := os.WriteFile(binPath, bin, 0o755); err != nil {
		return err
	}
	return nil
}

// nativeLinkWeightMB estimates the native assemble+link's peak RSS for a
// given asm size, for reservation against the build-memory budget. The
// assembler's working set is roughly linear in the input: measured
// ~2.2 GB of assembler structures + output buffers on a 470 MB driver
// `.s` (on top of the input string itself), so ~5 MB per MB of asm plus
// slack covers it.
func nativeLinkWeightMB(asmLen int) int {
	return 500 + 5*(asmLen>>20)
}
