package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

const mapBoxedInsertSrc = `import "core/map";
import "std/i32";
struct P { a: i32, b: string }
function main(): i32 {
  let m: Map[string, P] = map_new(4);
  let i: i32 = 0;
  while (i < 50) {
    m = m.insert("k" + (i % 7).to_string(), P { a: i, b: "v" + i.to_string() });
    i = i + 1;
  }
  let t: i32 = 0;
  for k in m.keys() {
    match (m.get(k)) {
      Some(p) => { t = t + p.a + p.b.len(); },
      None => {}
    }
  }
  print(m.len().to_string() + " " + t.to_string());
  return 0;
}
`

const mapBoxedInsertWant = "7 343\n"

var (
	lookupCall = regexp.MustCompile(`(?m)^\s+(call|bl)\s+\S*__map_lookup_val`)
	putCall    = regexp.MustCompile(`(?m)^\s+(call|bl)\s+\S*__map_put_`)
)

func TestMapBoxedInsertLooksUpOnce(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(mapBoxedInsertSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		asm := filepath.Join(dir, target+".s")
		if out, err := exec.Command(buildLangBinForInterp(t), "-target", target, "-emit", "asm", "-o", asm, src).CombinedOutput(); err != nil {
			t.Fatalf("%s emit: %v\n%s", target, err, out)
		}
		text, err := os.ReadFile(asm)
		if err != nil {
			t.Fatal(err)
		}
		if lookupCall.Match(text) {
			t.Errorf("%s: an insert still reads the superseded value with a separate lookup", target)
		}
		if !putCall.Match(text) {
			t.Errorf("%s: the insert does not go through __map_put_*", target)
		}
	}
}

func TestInterpMapBoxedInsert(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(mapBoxedInsertSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != mapBoxedInsertWant {
		t.Fatalf("exit %d, printed %q, want %q\nstderr: %s", code, out, mapBoxedInsertWant, errOut)
	}
}

func TestX86_64MapBoxedInsert(t *testing.T) {
	out, code := compileAndRunX86_64(t, mapBoxedInsertSrc)
	if code != 0 || out != mapBoxedInsertWant {
		t.Fatalf("exit %d, printed %q, want %q", code, out, mapBoxedInsertWant)
	}
}

func TestArm64MapBoxedInsert(t *testing.T) {
	out, code := compileAndRunArm64(t, mapBoxedInsertSrc)
	if code != 0 || out != mapBoxedInsertWant {
		t.Fatalf("exit %d, printed %q, want %q", code, out, mapBoxedInsertWant)
	}
}

func TestWasmMapBoxedInsert(t *testing.T) {
	if out := compileAndRunWasmCapture(t, mapBoxedInsertSrc); out != mapBoxedInsertWant {
		t.Fatalf("printed %q, want %q", out, mapBoxedInsertWant)
	}
}
