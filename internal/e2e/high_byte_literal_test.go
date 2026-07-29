package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// highByteProg prints the length and each byte of a string literal built from
// \xNN escapes spanning the whole 0..255 range, so any byte the pipeline
// mangles shows up as both a wrong value and a wrong length.
const highByteProg = `import "std/i32";
function main(): i32 {
    var s: string = "\x00\x41\x7f\x80\xc8\xef\xfe\xff";
    var out: string = s.len().to_string() + ":";
    var i: i32 = 0;
    while (i < s.len()) { out = out + (s[i] as i32).to_string() + " "; i = i + 1; }
    write(out + "\n");
    return 0;
}
`

const highByteWant = "8:0 65 127 128 200 239 254 255 \n"

// TestHighByteStringLiteralNativeAssembler pins that a string literal carrying
// bytes >= 0x80 survives the DEFAULT compile path — the in-process pure-Go
// assembler that `cmd/fern -target <x86-64|arm64>` uses with no external
// toolchain.
//
// It did not. The code generators write those bytes into the `.asciz` operand
// raw (escapeForGAS only escapes quotes, backslash and control bytes), which is
// exactly what GNU as wants; but the in-process assembler decoded the operand
// with strconv.Unquote, which treats a quoted string as UTF-8 TEXT. A lone 0x80
// is invalid UTF-8, so it came back as U+FFFD's three bytes (ef bf bd) — with
// no error — corrupting the data and desynchronising it from the .4byte length
// emitted beside it. The gcc fallback path assembled the same asm correctly, so
// the two disagreed silently, and only programs with non-ASCII literals could
// see it.
//
// The victim in this repo is the self-hosted wasm Component-Model framing:
// watbin.fern carries the component type/canon sections as binary blobs written
// as \xNN escapes, so a natively-assembled self-host CLI emitted components
// whose type section was corrupt (`wasm-tools validate` → "unexpected
// end-of-file"). That is a whole feature broken by a silent byte-level
// divergence, which is why this guards the pipeline and not just the decoder.
//
// The interpreter is the reference: it never routes literals through an
// assembler, so its bytes are the source of truth.
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
		// No -cc: the default in-process assembler + linker, the path the
		// bug lived on.
		if o, err := exec.Command(bin, "-target", "x86-64", "-o", outBin, src).CombinedOutput(); err != nil {
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
		if o, err := exec.Command(bin, "-target", "arm64", "-o", outBin, src).CombinedOutput(); err != nil {
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
