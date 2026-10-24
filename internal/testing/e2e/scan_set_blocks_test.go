package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	for _, tc := range []struct{ target, kernel string }{{"x86-64-linux", "vpshufb %ymm"}, {"arm64-linux", "tbl v"}} {
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

const scanSetShapesSrc = `import "std/i32";
const RANGE: u8[] = [
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
];
const NOT_RANGE: u8[] = [
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
];
const LIST: u8[] = [
  1u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 1u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
];
const NOT_LIST: u8[] = [
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 0u8, 0u8, 1u8, 1u8, 0u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  0u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
];
const LIST_HIGH: u8[] = [
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8,
];
const RANGE_PLUS: u8[] = [
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  0u8, 0u8, 1u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
];
const NOT_RANGE_PLUS: u8[] = [
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 0u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8, 1u8, 1u8, 1u8, 0u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 0u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
];

// For every byte value b, a 72-byte string of plain bytes with b at position
// p, scanned from several offsets: the sum over all of them of the index found
// times b + 1, so a single wrong answer moves it.
function probe_range(plain: u8): i32 {
  let total: i32 = 0;
  let b: i32 = 0;
  while (b < 256) {
    let bs: u8[] = [];
    let p: i32 = 0;
    while (p < 72) {
      bs = bs.append(plain);
      p = p + 1;
    }
    let pos: i32 = 0;
    while (pos < 72) {
      let s: string = string_from_bytes_unchecked(bs.with(pos, b as u8));
      let from: i32 = 0;
      while (from < 72) {
        total = total + __scan_set(s, from, RANGE) * (b + 1);
        from = from + 7;
      }
      pos = pos + 3;
    }
    b = b + 1;
  }
  return total;
}

// For every byte value b, a 72-byte string of plain bytes with b at position
// p, scanned from several offsets: the sum over all of them of the index found
// times b + 1, so a single wrong answer moves it.
function probe_not_range(plain: u8): i32 {
  let total: i32 = 0;
  let b: i32 = 0;
  while (b < 256) {
    let bs: u8[] = [];
    let p: i32 = 0;
    while (p < 72) {
      bs = bs.append(plain);
      p = p + 1;
    }
    let pos: i32 = 0;
    while (pos < 72) {
      let s: string = string_from_bytes_unchecked(bs.with(pos, b as u8));
      let from: i32 = 0;
      while (from < 72) {
        total = total + __scan_set(s, from, NOT_RANGE) * (b + 1);
        from = from + 7;
      }
      pos = pos + 3;
    }
    b = b + 1;
  }
  return total;
}

// For every byte value b, a 72-byte string of plain bytes with b at position
// p, scanned from several offsets: the sum over all of them of the index found
// times b + 1, so a single wrong answer moves it.
function probe_list(plain: u8): i32 {
  let total: i32 = 0;
  let b: i32 = 0;
  while (b < 256) {
    let bs: u8[] = [];
    let p: i32 = 0;
    while (p < 72) {
      bs = bs.append(plain);
      p = p + 1;
    }
    let pos: i32 = 0;
    while (pos < 72) {
      let s: string = string_from_bytes_unchecked(bs.with(pos, b as u8));
      let from: i32 = 0;
      while (from < 72) {
        total = total + __scan_set(s, from, LIST) * (b + 1);
        from = from + 7;
      }
      pos = pos + 3;
    }
    b = b + 1;
  }
  return total;
}

// For every byte value b, a 72-byte string of plain bytes with b at position
// p, scanned from several offsets: the sum over all of them of the index found
// times b + 1, so a single wrong answer moves it.
function probe_not_list(plain: u8): i32 {
  let total: i32 = 0;
  let b: i32 = 0;
  while (b < 256) {
    let bs: u8[] = [];
    let p: i32 = 0;
    while (p < 72) {
      bs = bs.append(plain);
      p = p + 1;
    }
    let pos: i32 = 0;
    while (pos < 72) {
      let s: string = string_from_bytes_unchecked(bs.with(pos, b as u8));
      let from: i32 = 0;
      while (from < 72) {
        total = total + __scan_set(s, from, NOT_LIST) * (b + 1);
        from = from + 7;
      }
      pos = pos + 3;
    }
    b = b + 1;
  }
  return total;
}

// For every byte value b, a 72-byte string of plain bytes with b at position
// p, scanned from several offsets: the sum over all of them of the index found
// times b + 1, so a single wrong answer moves it.
function probe_list_high(plain: u8): i32 {
  let total: i32 = 0;
  let b: i32 = 0;
  while (b < 256) {
    let bs: u8[] = [];
    let p: i32 = 0;
    while (p < 72) {
      bs = bs.append(plain);
      p = p + 1;
    }
    let pos: i32 = 0;
    while (pos < 72) {
      let s: string = string_from_bytes_unchecked(bs.with(pos, b as u8));
      let from: i32 = 0;
      while (from < 72) {
        total = total + __scan_set(s, from, LIST_HIGH) * (b + 1);
        from = from + 7;
      }
      pos = pos + 3;
    }
    b = b + 1;
  }
  return total;
}

// For every byte value b, a 72-byte string of plain bytes with b at position
// p, scanned from several offsets: the sum over all of them of the index found
// times b + 1, so a single wrong answer moves it.
function probe_range_plus(plain: u8): i32 {
  let total: i32 = 0;
  let b: i32 = 0;
  while (b < 256) {
    let bs: u8[] = [];
    let p: i32 = 0;
    while (p < 72) {
      bs = bs.append(plain);
      p = p + 1;
    }
    let pos: i32 = 0;
    while (pos < 72) {
      let s: string = string_from_bytes_unchecked(bs.with(pos, b as u8));
      let from: i32 = 0;
      while (from < 72) {
        total = total + __scan_set(s, from, RANGE_PLUS) * (b + 1);
        from = from + 7;
      }
      pos = pos + 3;
    }
    b = b + 1;
  }
  return total;
}

// For every byte value b, a 72-byte string of plain bytes with b at position
// p, scanned from several offsets: the sum over all of them of the index found
// times b + 1, so a single wrong answer moves it.
function probe_not_range_plus(plain: u8): i32 {
  let total: i32 = 0;
  let b: i32 = 0;
  while (b < 256) {
    let bs: u8[] = [];
    let p: i32 = 0;
    while (p < 72) {
      bs = bs.append(plain);
      p = p + 1;
    }
    let pos: i32 = 0;
    while (pos < 72) {
      let s: string = string_from_bytes_unchecked(bs.with(pos, b as u8));
      let from: i32 = 0;
      while (from < 72) {
        total = total + __scan_set(s, from, NOT_RANGE_PLUS) * (b + 1);
        from = from + 7;
      }
      pos = pos + 3;
    }
    b = b + 1;
  }
  return total;
}

function main(): i32 {
  print("RANGE " + probe_range(97u8).to_string());
  print("NOT_RANGE " + probe_not_range(97u8).to_string());
  print("LIST " + probe_list(97u8).to_string());
  print("NOT_LIST " + probe_not_list(32u8).to_string());
  print("LIST_HIGH " + probe_list_high(97u8).to_string());
  print("RANGE_PLUS " + probe_range_plus(97u8).to_string());
  print("NOT_RANGE_PLUS " + probe_not_range_plus(95u8).to_string());
  return 0;
}
`

const scanSetShapesWant = "RANGE 623423763\nNOT_RANGE 520717059\nLIST 624837861\nNOT_LIST 510947244\nLIST_HIGH 623695437\nRANGE_PLUS 623002320\nNOT_RANGE_PLUS 518881518\n"

func TestScanSetShapes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(scanSetShapesSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path); code != 0 || out != scanSetShapesWant {
		t.Fatalf("interp: exit %d, printed %q, want %q\nstderr: %s", code, out, scanSetShapesWant, errOut)
	}
	if out, code := compileAndRunX86_64(t, scanSetShapesSrc); code != 0 || out != scanSetShapesWant {
		t.Errorf("x86-64: exit %d, printed %q, want %q", code, out, scanSetShapesWant)
	}
	if out, code := compileAndRunArm64(t, scanSetShapesSrc); code != 0 || out != scanSetShapesWant {
		t.Errorf("arm64: exit %d, printed %q, want %q", code, out, scanSetShapesWant)
	}
	if out := compileAndRunWasmCapture(t, scanSetShapesSrc); out != scanSetShapesWant {
		t.Errorf("wasm: printed %q, want %q", out, scanSetShapesWant)
	}
	for _, tc := range []struct{ target, rangeTest, nibble string }{{"x86-64-linux", "vpminub", "vpshufb"}, {"arm64-linux", "cmhs", "tbl v"}} {
		asm := path + "." + tc.target + ".s"
		if out, err := exec.Command(buildLangBinForInterp(t), "-target", tc.target, "-emit", "asm", "-o", asm, path).CombinedOutput(); err != nil {
			t.Fatalf("%s emit: %v\n%s", tc.target, err, out)
		}
		text, err := os.ReadFile(asm)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(text), tc.rangeTest) || strings.Contains(string(text), tc.nibble) {
			t.Errorf("%s: want the range test %q and no nibble lookup %q", tc.target, tc.rangeTest, tc.nibble)
		}
	}
}
