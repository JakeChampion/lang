package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scanSetBlocksSrc scans, for every byte value, a 72-byte string holding that
// byte at one position, from offsets that leave several whole blocks, one, or
// none ahead of the byte-at-a-time tail, against a constant set with
// members in both halves of the byte range and at both ends of a nibble row.
// The sum of the found indexes moves if any one answer is wrong, and the last
// three scans cover an empty string, a start past the end and a negative one.
const scanSetBlocksSrc = `import "std/i32";
// Bytes 0..31, '"', '\\', 0x7f, 0x80, 0xc3 and 0xff are members.
const S: u8[] = [
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  0u8, 0u8, 1u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8,
  1u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 1u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8,
];

// For every byte value b, a string of n plain bytes with b at position p,
// scanned from ` + "`" + `from` + "`" + `: the sum over all of them of the index found, so a
// single wrong answer moves it.
function main(): i32 {
  let total: i32 = 0;
  let b: i32 = 0;
  while (b < 256) {
    let bs: u8[] = [];
    let p: i32 = 0;
    while (p < 72) {
      bs = bs.append(97u8);
      p = p + 1;
    }
    let pos: i32 = 0;
    while (pos < 72) {
      let t: u8[] = bs.with(pos, b as u8);
      let s: string = string_from_bytes_unchecked(t);
      let from: i32 = 0;
      while (from < 72) {
        total = total + __scan_set(s, from, S) * (b + 1);
        from = from + 7;
      }
      pos = pos + 3;
    }
    b = b + 1;
  }
  print(total.to_string() + " " + __scan_set("", 0, S).to_string() + " " + __scan_set("abc", 5, S).to_string() + " " + __scan_set("ab\\cd", 0 - 3, S).to_string());
  return 0;
}
`

const scanSetBlocksWant = "620532873 0 3 2\n"

// A scan over a constant set classifies sixteen bytes at a time: pshufb on
// x86-64, tbl on arm64. A set only known at run time keeps the table loop.
func TestScanSetBlocksOnlyForAConstantSet(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	constant := write("const.fern", scanSetBlocksSrc)
	runtime := write("runtime.fern", "function main(): i32 { let set: u8[] = [0u8, 1u8]; set = set.append(1u8); return __scan_set(\"abc\", 0, set); }\n")
	for _, tc := range []struct{ target, kernel string }{{"x86-64-linux", "pshufb"}, {"arm64-linux", "tbl v"}} {
		for _, src := range []string{constant, runtime} {
			asm := src + "." + tc.target + ".s"
			if out, err := exec.Command(buildLangBinForInterp(t), "-target", tc.target, "-emit", "asm", "-o", asm, src).CombinedOutput(); err != nil {
				t.Fatalf("%s emit: %v\n%s", tc.target, err, out)
			}
			text, err := os.ReadFile(asm)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Contains(string(text), tc.kernel)
			if want := src == constant; got != want {
				t.Errorf("%s %s: block kernel emitted = %v, want %v", tc.target, filepath.Base(src), got, want)
			}
		}
	}
}

func TestInterpScanSetBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(scanSetBlocksSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != scanSetBlocksWant {
		t.Fatalf("exit %d, printed %q, want %q\nstderr: %s", code, out, scanSetBlocksWant, errOut)
	}
}

func TestX86_64ScanSetBlocks(t *testing.T) {
	out, code := compileAndRunX86_64(t, scanSetBlocksSrc)
	if code != 0 || out != scanSetBlocksWant {
		t.Fatalf("exit %d, printed %q, want %q", code, out, scanSetBlocksWant)
	}
}

func TestArm64ScanSetBlocks(t *testing.T) {
	out, code := compileAndRunArm64(t, scanSetBlocksSrc)
	if code != 0 || out != scanSetBlocksWant {
		t.Fatalf("exit %d, printed %q, want %q", code, out, scanSetBlocksWant)
	}
}

func TestWasmScanSetBlocks(t *testing.T) {
	if out := compileAndRunWasmCapture(t, scanSetBlocksSrc); out != scanSetBlocksWant {
		t.Fatalf("printed %q, want %q", out, scanSetBlocksWant)
	}
}
