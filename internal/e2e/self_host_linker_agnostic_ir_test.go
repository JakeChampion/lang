package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostLinkerAgnosticIRX86_64 is the regression gate for issue #4081:
// the self-host x86-64 backend must emit freestanding `-static -nostdlib
// -no-pie` programs that link CORRECTLY under any linker, not just GNU bfd.
//
// Two assumptions used to make the output bfd-only:
//
//  1. The heap was a multi-GiB static `.bss` reservation (`__fern_heap`)
//     addressed via 64-bit-absolute `movabs`, relying on bfd's default .bss
//     ordering. It is now an mmap'd arena (like the native Go backend + the
//     self-host arm64 backend), so the base comes from the kernel and no
//     linker-layout assumption survives.
//
//  2. `g[0][0].len()` on a nested `string[][]` mis-dispatched to `arr_len`
//     (read offset 0 — the string box's DATA POINTER) instead of `str_len`
//     (offset 8 — the length). Summing data pointers makes the result depend
//     on where the linker places `.rodata`: the issue's repro returned 5 under
//     bfd (rodata low bytes 0x00+0x02+0x03) but 253 under lld (0xa8+0xaa+0xab).
//     `expr_is_strarr` now recognises `m[i]` of a string[][] as a string[], so
//     the nested string element's `.len()` reads offset 8.
//
// The gate compiles a string-heavy program ONCE with the self-host driver, then
// links the SAME `.s` with every available linker (bfd always, lld/mold when
// present) and asserts they all produce the SAME, CORRECT exit code.
func TestSelfHostLinkerAgnosticIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		// Comparing linkers is about the x86-64 ELF layout; run it natively so
		// the result reflects the binary, not a qemu transition. (The bug is
		// host-independent, but a native run keeps the gate simple + fast.)
		t.Skip("linker-agnostic gate runs only natively (multi-linker link+run)")
	}
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// The exact issue #4081 repro: nested string boxes, `.len()` on each
		// element. 2 + 1 + 2 = 5.
		{"issue-4081-repro",
			`function main(): i32 { var g: string[][] = [["ab", "c"], ["de"]]; return g[0][0].len() + g[0][1].len() + g[1][0].len(); }`, 5},
		// Deeper: every cell of a 2x2-ish ragged string grid, lengths chosen so
		// the sum (4+3+2+1 = 10) is NOT a coincidence of rodata layout.
		{"string-grid-len",
			`function main(): i32 { var g: string[][] = [["abcd", "efg"], ["hi", "j"]]; return g[0][0].len() + g[0][1].len() + g[1][0].len() + g[1][1].len(); }`, 10},
		// A nested string element bound to a local, then `.len()` off the local —
		// exercises the `var r = g[i][j]` rebind tracking the string kind.
		{"nested-rebind-len",
			`function main(): i32 { var g: string[][] = [["xyz", "w"], ["uv"]]; var r: string = g[0][0]; return r.len(); }`, 3},
	}

	// Discover the available linkers. bfd is gcc's default (always present);
	// lld + mold are opportunistic — each present one must agree with bfd.
	type linker struct {
		name string
		flag string // gcc -fuse-ld=… flag, "" for the default (bfd)
	}
	linkers := []linker{{"bfd", ""}}
	if _, err := exec.LookPath("ld.lld"); err == nil {
		linkers = append(linkers, linker{"lld", "-fuse-ld=lld"})
	}
	if _, err := exec.LookPath("mold"); err == nil {
		linkers = append(linkers, linker{"mold", "-fuse-ld=mold"})
	}
	if len(linkers) < 2 {
		t.Skip("no alternative linker (ld.lld / mold) on PATH; nothing to cross-check")
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			asmPath := filepath.Join(dir, tc.name+".s")
			if err := os.WriteFile(asmPath, asm, 0o644); err != nil {
				t.Fatalf("write %s asm: %v", tc.name, err)
			}
			for _, ln := range linkers {
				binPath := filepath.Join(dir, tc.name+"."+ln.name)
				args := []string{"-static", "-nostdlib", "-no-pie"}
				if ln.flag != "" {
					args = append(args, ln.flag)
				}
				args = append(args, asmPath, "-o", binPath)
				if out, err := exec.Command(gcc, args...).CombinedOutput(); err != nil {
					t.Fatalf("%s: link with %s failed: %v\n%s", tc.name, ln.name, err, out)
				}
				cmd := exec.Command(binPath)
				_ = cmd.Run()
				if code := cmd.ProcessState.ExitCode(); code != tc.exit {
					t.Errorf("%s linked with %s: exit %d, want %d (linker-dependent layout bug — #4081)",
						tc.name, ln.name, code, tc.exit)
				}
			}
		})
	}
}
