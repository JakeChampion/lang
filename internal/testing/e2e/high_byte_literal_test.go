package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const highByteProg = `import "std/i32";
function main(): i32 {
    let s: string = "\x00\x41\x7f\u{80}\u{e9}\u{FFFE}\u{10FFFF}";
    let out: string = s.len().to_string() + ":";
    let i: i32 = 0;
    while (i < s.len()) { out = out + (s[i] as i32).to_string() + " "; i = i + 1; }
    write(out + "\n");
    return 0;
}
`

const highByteWant = "14:0 65 127 194 128 195 169 239 191 190 244 143 191 191 \n"

func TestHighByteStringLiteralNativeAssembler(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(highByteProg), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	t.Run("interp", func(t *testing.T) {
		out, err := exec.Command(bin, "-interp", src).Output()
		if err != nil {
			t.Fatalf("interp: %v", err)
		}
		if string(out) != highByteWant {
			t.Errorf("interp = %q, want %q", out, highByteWant)
		}
	})

	t.Run("x86-64-native-assembler", func(t *testing.T) {
		if _, err := exec.LookPath("qemu-x86_64"); err != nil && !isX86Host() {
			t.Skip("no way to run x86-64 binaries on this host")
		}
		outBin := filepath.Join(dir, "prog.x86")
		if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", outBin, src).CombinedOutput(); err != nil {
			t.Fatalf("x86-64 build: %v\n%s", err, o)
		}
		got := runMaybeEmulated(t, outBin, "qemu-x86_64", isX86Host())
		if got != highByteWant {
			t.Errorf("x86-64 = %q, want %q", got, highByteWant)
		}
	})

	t.Run("arm64-native-assembler", func(t *testing.T) {
		qemu := arm64QemuOrEmpty(t)
		outBin := filepath.Join(dir, "prog.arm64")
		if o, err := exec.Command(bin, "-target", "arm64-linux", "-o", outBin, src).CombinedOutput(); err != nil {
			t.Fatalf("arm64 build: %v\n%s", err, o)
		}
		got := runMaybeEmulated(t, outBin, qemu, qemu == "")
		if got != highByteWant {
			t.Errorf("arm64 = %q, want %q", got, highByteWant)
		}
	})
}

func runMaybeEmulated(t *testing.T, binPath, emulator string, native bool) string {
	t.Helper()
	var cmd *exec.Cmd
	if native {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(emulator, binPath)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run %s: %v", filepath.Base(binPath), err)
	}
	return string(out)
}

func isX86Host() bool {
	out, err := exec.Command("uname", "-m").Output()
	if err != nil {
		return false
	}
	m := strings.TrimSpace(string(out))
	return m == "x86_64" || m == "amd64"
}
