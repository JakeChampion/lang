// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3).
package e2eharness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	nativearm64 "github.com/jakechampion/lang/internal/native/arm64"
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
// uses by default for `-target x86-64-linux` (linkNativeX86 in main.go). No
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

// nativeLinkArm64 is the arm64 sibling of nativeLinkX86: assemble
// GAS-flavoured arm64 asm and wrap it in a static ELF executable
// entirely in-process — the same pipeline `cmd/fern` uses by default for
// `-target arm64-linux` (linkNative in main.go), but entry-aware: the
// SELF-HOST arm64 emitter defines `_start` after other functions, so the
// entry point must be resolved from the label rather than assumed to be
// .text's first instruction. Callers fall back to the aarch64 gcc path
// on error.
func nativeLinkArm64(asm, binPath string) error {
	text, rodata, entryOff, err := nativearm64.AssembleProgramWXEntry(asm, nativeelf.TextVAddrWX, "_start")
	if err != nil {
		return fmt.Errorf("native assemble (arm64): %w", err)
	}
	bin := nativeelf.StaticExecutableDataWXEntry(text, rodata, entryOff)
	if err := os.WriteFile(binPath, bin, 0o755); err != nil {
		return err
	}
	return nil
}

// NativeLinkArm64 assembles+links arm64 asm into binPath entirely
// in-process, with no aarch64 toolchain — for tests that must run on a
// host without a cross gcc. BuildBinArm64 is the general entry point;
// this one is the pure-Go path on its own.
func NativeLinkArm64(asm, binPath string) error { return nativeLinkArm64(asm, binPath) }

// nativeLinkWeightMB estimates the native assemble+link's peak RSS for a
// given asm size, for reservation against the build-memory budget. The
// assembler's working set is roughly linear in the input: measured
// ~2.2 GB of assembler structures + output buffers on a 470 MB driver
// `.s` (on top of the input string itself), so ~5 MB per MB of asm plus
// slack covers it.
func nativeLinkWeightMB(asmLen int) int {
	return 500 + 5*(asmLen>>20)
}

// BuildBinArm64 assembles+links arm64 asm into dir/name and returns its
// path — the arm64 sibling of BuildBin. Small programs (the overwhelming
// majority) go to the aarch64 gcc toolchain exactly as before (note: no
// `-no-pie` — some aarch64 gcc builds reject it). HUGE asm — the aarch64
// stage-2 self-compile (~450 MB) and the Go-arm64-emitted driver in the
// native-mmc equivalence test — goes through the in-process native arm64
// assembler first, under a build-memory reservation and the soft heap
// cap: the gcc path on those inputs ran GNU `as` at multi-GB RSS with no
// reservation at all. Assembler errors fall back to gcc unchanged.
func BuildBinArm64(t *testing.T, gcc, dir, name, asm string) string {
	t.Helper()
	binPath := filepath.Join(dir, name)
	if len(asm) >= nativeLinkMinAsmBytes {
		if err := withBuildMemory(nativeLinkWeightMB(len(asm)), func() error {
			return withEmitMemLimit(func() error {
				return nativeLinkArm64(asm, binPath)
			})
		}); err == nil {
			return binPath
		}
	}
	asmPath := filepath.Join(dir, name+".s")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write %s asm: %v", name, err)
	}
	gccLink := func() error {
		if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			return fmt.Errorf("gcc %s: %v\n%s", name, err, out)
		}
		return nil
	}
	// A big-asm gcc link (the native path failed, or a future consumer
	// bypasses it) runs GNU `as` at multi-GB RSS — reserve its estimated
	// peak so it can't stack with a concurrent heavy build. Small links
	// stay unreserved (milliseconds, a few MB).
	var err error
	if len(asm) >= nativeLinkMinAsmBytes {
		err = withBuildMemory(gccBigLinkWeightMB(len(asm)), gccLink)
	} else {
		err = gccLink()
	}
	if err != nil {
		t.Fatal(err)
	}
	return binPath
}

// gccBigLinkWeightMB estimates GNU `as`+ld's peak RSS for a big `.s` —
// measured ~4.7 GB on a 470 MB x86-64 driver `.s`, i.e. ~10 MB per MB of
// asm plus slack.
func gccBigLinkWeightMB(asmLen int) int {
	return 500 + 10*(asmLen>>20)
}
