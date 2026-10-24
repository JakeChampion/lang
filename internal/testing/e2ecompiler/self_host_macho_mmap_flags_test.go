package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func movzX3(imm16 uint32) []byte {
	w := 0xD2800000 | (imm16 << 5) | 3
	return []byte{byte(w), byte(w >> 8), byte(w >> 16), byte(w >> 24)}
}

func hasMovzImm(raw []byte, imm16 uint32) bool {
	for rd := uint32(0); rd <= 30; rd++ {
		w := 0xD2800000 | (imm16 << 5) | rd
		if bytes.Contains(raw, []byte{byte(w), byte(w >> 8), byte(w >> 16), byte(w >> 24)}) {
			return true
		}
	}
	return false
}

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
	src := "function main(): i32 { let s: string = \"a\" + \"b\"; let w = open_writer(s); return 0; }\n"
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	darwinFlags := movzX3(0x1002)
	linuxFlags := movzX3(0x4022)

	const darwinOTrunc = 1537

	for _, tc := range []struct {
		target      string
		want        []byte
		notWant     []byte
		openWant    bool
		openPresent uint32
	}{
		{"arm64-darwin", darwinFlags, linuxFlags, true, darwinOTrunc},
		{"arm64-linux", linuxFlags, darwinFlags, false, darwinOTrunc},
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
			if got := hasMovzImm(raw, tc.openPresent); got != tc.openWant {
				t.Errorf("-target %s: openat O_TRUNC translation (movz xN, #%d) present = %v, want %v", tc.target, tc.openPresent, got, tc.openWant)
			}
		})
	}
}
