package e2eselfhost

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	nativearm64 "github.com/jakechampion/lang/internal/native/arm64"
	nativeelf "github.com/jakechampion/lang/internal/native/elf"
)

// The self-host's two GAS assemblers read back the escapes its own emitter
// writes, and until this test they read back only some of them.
//
// asmcore.escape_for_ascii spells every byte below 0x20, and 0x7f, as GAS's
// three-digit octal `\NNN`. Both in-process assemblers decoded `\0` as NUL and
// passed every other digit through as itself, so `"A\x01B"` was emitted as
// `.ascii "A\001B"` and assembled to `A`, NUL, `0`, `1`, `B` — then truncated
// at the length the emitter recorded, reading back as `A`, NUL, `0`. Two stray
// bytes per control byte, and everything after them in the literal shifted.
//
// Nothing caught it because nothing in the corpus puts a control byte in a
// literal: `\n`, `\t` and `\r` have their own one-letter escapes, and a high
// byte like `\xff` is written raw and always round-tripped. It surfaced from
// `-embed` (#7986), where an asset is arbitrary bytes by definition.
// docs/FEATURE-AUDIT.md carried a ✅ for `\xNN` on both self-host columns
// throughout.
//
// The x86-64 half runs the program, which is the strongest form available. The
// arm64 half compares assembled .rodata against internal/native/arm64 on the
// same GAS text — no qemu, and the layer the bug is actually in.

// escapeCases are literals whose bytes exercise the decoder. Each is written as
// Fern source and as the bytes it must produce.
var escapeCases = []struct {
	name string
	src  string
	want []byte
}{
	{"nul-then-letter", `A\x00B`, []byte{'A', 0, 'B'}},
	// The one that made the bug visible: a control byte whose octal digits are
	// not all zero, so a decoder that stops after `\0` leaves "1" behind.
	{"one-then-letter", `A\x01B`, []byte{'A', 1, 'B'}},
	{"del", `A\x7fB`, []byte{'A', 0x7f, 'B'}},
	{"just-under-space", `A\x1fB`, []byte{'A', 0x1f, 'B'}},
	{"run-of-controls", `\x00\x01\x02\x03\x04\x05\x06\x07`, []byte{0, 1, 2, 3, 4, 5, 6, 7}},
	// The named escapes, which were already right and must stay right.
	{"named-escapes", `A\tB\nC\rD`, []byte{'A', '\t', 'B', '\n', 'C', '\r', 'D'}},
	// High bytes are written raw rather than escaped, and always worked.
	{"high-byte", `A\xffB`, []byte{'A', 0xff, 'B'}},
	{"high-and-control", `\x80\x00\xff\x1b`, []byte{0x80, 0, 0xff, 0x1b}},
	// A REAL backslash followed by a digit. The escaped form is `\\0`, and a
	// decoder that reads octal greedily past the `\\` would turn it into NUL.
	{"escaped-backslash-then-digit", `A\\0B`, []byte{'A', '\\', '0', 'B'}},
	{"escaped-quote", `q\"w`, []byte{'q', '"', 'w'}},
}

// escapeProbeSource is a program that prints one byte value per line, so a
// wrong byte names itself instead of showing up as a garbled string.
func escapeProbeSource(lit string) string {
	return `function main(): i32 {
    var b: string = "` + lit + `";
    var i: i32 = 0;
    while (i < b.len()) {
        print(int_str(b[i] as i32));
        i = i + 1;
    }
    return 0;
}
function int_str(n: i32): string {
    if (n == 0) { return "0"; }
    var s: string = "";
    var v: i32 = n;
    while (v > 0) { s = digit(v % 10) + s; v = v / 10; }
    return s;
}
function digit(x: i32): string {
    if (x == 0) { return "0"; } if (x == 1) { return "1"; } if (x == 2) { return "2"; }
    if (x == 3) { return "3"; } if (x == 4) { return "4"; } if (x == 5) { return "5"; }
    if (x == 6) { return "6"; } if (x == 7) { return "7"; } if (x == 8) { return "8"; }
    return "9";
}
`
}

func TestSelfHostStringEscapesMatchNativeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("string-escape differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	for _, c := range escapeCases {
		t.Run(c.name, func(t *testing.T) {
			src := filepath.Join(dir, "esc_"+c.name+".fern")
			if err := os.WriteFile(src, []byte(escapeProbeSource(c.src)), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			out := t.TempDir()
			for _, b := range []struct {
				label string
				args  []string
				bin   string
			}{
				{"native", []string{"-target", "x86-64-linux", "-o", filepath.Join(out, "n"), src}, filepath.Join(out, "n")},
				{"self-host", []string{"-target", "x86-64-linux", "-o", filepath.Join(out, "s"), src, stdlib}, filepath.Join(out, "s")},
			} {
				cli := nativeBin
				if b.label == "self-host" {
					cli = driverBin
				}
				if o, err := exec.Command(cli, b.args...).CombinedOutput(); err != nil {
					t.Fatalf("%s compile failed: %v\n%s", b.label, err, o)
				}
				o, err := exec.Command(b.bin).Output()
				if err != nil {
					t.Fatalf("%s program failed: %v", b.label, err)
				}
				got := parseByteLines(t, string(o))
				if !bytes.Equal(got, c.want) {
					t.Errorf("%s: literal %q read back as % d, want % d", b.label, c.src, got, c.want)
				}
			}
		})
	}
}

// parseByteLines turns the probe's one-number-per-line output into bytes.
func parseByteLines(t *testing.T, out string) []byte {
	t.Helper()
	var bs []byte
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			t.Fatalf("probe printed %q, which is not a byte value", line)
		}
		bs = append(bs, byte(n))
	}
	return bs
}

// The arm64 half. arm64_native's decoder is a hand-mirrored copy of x86_native's
// — the two assemblers are deliberately import-free — and an untested mirror is
// how they drift, so it is compared against internal/native/arm64 rather than
// assumed to match its twin.
//
// The oracle is the same one TestSelfHostArm64AsmEncodingMatchesNative uses, for
// the same reason: internal/native/arm64 is what `bin/fern -target arm64-linux`
// runs in production and is itself gated against gcc's assembly of the same
// text, so "self-host agrees with native" transitively means "self-host agrees
// with gcc" without a cross-toolchain on this host.
func TestSelfHostArm64GasEscapesMatchNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bench := buildAsmBenchDriver(t, gcc)

	for _, c := range escapeCases {
		t.Run(c.name, func(t *testing.T) {
			// The escaped form the emitter would write, spelled here rather
			// than obtained from asmcore so this test pins the DECODER against
			// GAS's grammar and not against whatever the encoder currently does.
			esc := gasEscape(c.want)
			snippet := ".text\n.globl _start\n_start:\n    ret\n.data\n    .ascii \"" + esc + "\"\n"

			_, wantData, err := nativearm64.AssembleProgram(snippet, nativeelf.TextVAddr)
			if err != nil {
				t.Fatalf("native assembler rejected %q (the oracle must accept it): %v", snippet, err)
			}
			if !bytes.Equal(wantData, c.want) {
				t.Fatalf("the oracle itself decoded %q to % d, want % d — the escaping in this test is wrong", esc, wantData, c.want)
			}

			// The self-host pads .data out to an 8-byte boundary where the
			// native oracle returns it unpadded, so the comparison is over the
			// literal's own length. The padding is legitimate section layout;
			// what is under test is the bytes the decoder produced.
			got := assembleSelfHostData(t, bench, runner, snippet)
			if len(got) < len(c.want) {
				t.Fatalf("self-host arm64 assembled .ascii %q to only %d bytes, want at least %d: % d", esc, len(got), len(c.want), got)
			}
			if !bytes.Equal(got[:len(c.want)], c.want) {
				t.Errorf("self-host arm64 assembled .ascii %q to % d, want % d", esc, got[:len(c.want)], c.want)
			}
			for i, b := range got[len(c.want):] {
				if b != 0 {
					t.Errorf("self-host arm64 wrote %d past the literal at .data offset %d — that is not padding, the decoder emitted extra bytes: % d", b, len(c.want)+i, got)
					break
				}
			}
		})
	}
}

// gasEscape renders bytes the way asmcore.escape_for_ascii does: the five named
// escapes, three-digit octal below 0x20 and at 0x7f, everything else raw.
func gasEscape(bs []byte) string {
	var sb strings.Builder
	for _, b := range bs {
		switch {
		case b == '"':
			sb.WriteString(`\"`)
		case b == '\\':
			sb.WriteString(`\\`)
		case b == '\n':
			sb.WriteString(`\n`)
		case b == '\t':
			sb.WriteString(`\t`)
		case b == '\r':
			sb.WriteString(`\r`)
		case b < 0x20 || b == 0x7f:
			sb.WriteString(fmt.Sprintf(`\%03o`, b))
		default:
			sb.WriteByte(b)
		}
	}
	return sb.String()
}

// assembleSelfHostData feeds GAS text to the bench driver and returns the
// .rodata bytes it assembled. A refused line is a finding rather than a short
// answer, for the same reason assembleSelfHost treats one that way.
func assembleSelfHostData(t *testing.T, bin string, runner []string, snippet string) []byte {
	t.Helper()
	args := append(append([]string{}, runner...), bin, "-data")
	cmd := exec.Command(args[0], args[1:]...)
	if len(runner) == 0 {
		cmd = exec.Command(bin, "-data")
	}
	cmd.Stdin = strings.NewReader(snippet)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("self-host arm64 assembler failed: %v", err)
	}
	var bs []byte
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "unknown=") {
			t.Fatalf("self-host arm64 assembler refused a line: %s", line)
		}
		if !strings.HasPrefix(line, "data ") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 {
			t.Fatalf("malformed data line %q", line)
		}
		n, err := strconv.Atoi(f[2])
		if err != nil {
			t.Fatalf("malformed data line %q", line)
		}
		bs = append(bs, byte(n))
	}
	return bs
}
