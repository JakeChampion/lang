package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// movzX3 returns the little-endian encoding of `movz x3, #imm16` — the
// instruction that loads the arena mmap's FLAGS argument.
//
//	MOVZ (64-bit) = 0xD2800000 | (imm16 << 5) | Rd, Rd = x3.
func movzX3(imm16 uint32) []byte {
	w := 0xD2800000 | (imm16 << 5) | 3
	return []byte{byte(w), byte(w >> 8), byte(w >> 16), byte(w >> 24)}
}

// TestSelfHostArm64DarwinMmapFlags pins #6042: the arena mmap's flag word is a
// per-OS constant, and nothing downstream translates it.
//
// `darwin_sysno` maps the mmap syscall NUMBER (222 -> 197) and `darwinize`
// rewrites `mov x8` -> `mov x16` and `svc #0` -> `svc #0x80`, but `#0x4022` is
// a plain immediate that passes through untouched. On XNU MAP_ANON is 0x1000,
// not Linux's 0x20, so 0x4022 asked for a FILE mapping with fd = -1: mmap
// failed, the `b.mi` took the .Lalloc_oom trap, and EVERY arm64-darwin binary
// the self-host compiler produced died "heap arena exhausted" (exit 137) on its
// first allocation — a lie about the cause, since the arena was never mapped.
//
// This asserts the emitted bytes on any host, which matters because the runtime
// half cannot run here: qemu speaks only the Linux ABI, and the one test that
// executes arm64-darwin output is Apple-Silicon-only. Checking BOTH targets from
// one build is what makes it a real gate rather than a restatement of the fix —
// a conditional that fired the wrong way, or not at all, changes exactly one of
// these two assertions.
func TestSelfHostArm64DarwinMmapFlags(t *testing.T) {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		t.Skip("cross-emit byte check; the native lane runs TestSelfHostArm64DarwinBuilds instead")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host CLI driver runs only natively (argv paths)")
	}

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	srcPath := filepath.Join(dir, "mmapflags.fern")
	// The concat forces the arena runtime in; open_writer pulls in
	// __fern_open_fd, whose openat FLAGS are the same class of per-OS constant
	// (irlower hands it Linux 577 / 1089 target-agnostically).
	src := "function main(): i32 { var s: string = \"a\" + \"b\"; var w = open_writer(s); return 0; }\n"
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	darwinFlags := movzX3(0x1002) // MAP_ANON | MAP_PRIVATE          (XNU)
	linuxFlags := movzX3(0x4022)  // MAP_PRIVATE|MAP_ANONYMOUS|MAP_NORESERVE

	// `movz x23, #1537` — O_WRONLY|O_CREAT|O_TRUNC on XNU, the translation
	// __fern_open_fd applies to the Linux 577 the IR hands it. Present only on
	// the darwin emit. (MOVZ 64-bit, Rd = x23.)
	darwinOTrunc := []byte{0x37, 0xC0, 0x80, 0xD2}

	for _, tc := range []struct {
		target  string
		want    []byte
		notWant []byte
		// openWant/openNotWant: the openat flag translation, checked the same
		// way — present on darwin, absent on linux.
		openWant    bool
		openPresent []byte
	}{
		{"arm64-darwin", darwinFlags, linuxFlags, true, darwinOTrunc},
		{"arm64", linuxFlags, darwinFlags, false, darwinOTrunc},
	} {
		tc := tc
		t.Run(tc.target, func(t *testing.T) {
			binPath := filepath.Join(dir, "mmapflags_"+tc.target+".bin")
			out, err := exec.Command(fernBin, "-target", tc.target, "-o", binPath, srcPath).CombinedOutput()
			if err != nil {
				t.Fatalf("self-host emit failed: %v\n%s", err, out)
			}
			raw, err := os.ReadFile(binPath)
			if err != nil {
				t.Fatalf("read bin: %v", err)
			}
			if !bytes.Contains(raw, tc.want) {
				t.Errorf("-target %s: emitted binary does not contain `movz x3, #<%s flags>` (% x)", tc.target, tc.target, tc.want)
			}
			if bytes.Contains(raw, tc.notWant) {
				t.Errorf("-target %s: emitted binary still contains the OTHER platform's mmap flags (% x)", tc.target, tc.notWant)
			}
			if got := bytes.Contains(raw, tc.openPresent); got != tc.openWant {
				t.Errorf("-target %s: openat O_TRUNC translation present = %v, want %v (% x)", tc.target, got, tc.openWant, tc.openPresent)
			}
		})
	}
}
