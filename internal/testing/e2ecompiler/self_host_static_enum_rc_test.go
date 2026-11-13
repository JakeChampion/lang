package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Color has no payload, so each of its values is a static object. Mark has
// a payload variant, so its values are counted boxes.
const staticEnumProgram = `import "core/mem";
enum Color { Red, Black }
enum Mark { Plain, Tagged(string) }
enum Tree { Node(Color, Tree, i32, Tree), Leaf }
struct Pen { color: Color, mark: Mark, width: i32 }

@noinline
function paint(t: Tree, c: Color): Tree {
  match (t) {
    Node(_, l, k, r) => { return Node(c, l, k, r); },
    Leaf => { return Leaf; }
  }
}

@noinline
function shade(t: Tree): Color {
  match (t) {
    Node(c, _, _, _) => { return c; },
    Leaf => { return Black; }
  }
}

@noinline
function remark(p: Pen, m: Mark): Pen {
  return Pen { ...p, mark: m };
}

@noinline
function red(c: Color): boolean {
  match (c) {
    Red => { return true; },
    Black => { return false; }
  }
}

function count_red(cs: Color[]): i32 {
  let n = 0;
  for c in cs {
    if (red(c)) { n += 1; }
  }
  return n;
}

function main(): i32 {
  let t = Node(Red, Node(Black, Leaf, 1, Leaf), 2, Leaf);
  let u = paint(t, Black);
  if (red(shade(u))) { return 1; }
  if (!red(shade(t))) { return 2; }
  let cs: Color[] = [Red, Black, Red];
  cs = cs.append(shade(t));
  cs = cs.with(1, Red);
  if (count_red(cs) != 4) { return 3; }
  let o: Option[Color] = Some(Black);
  if let Some(c) = o {
    if (red(c)) { return 4; }
  }
  let p = Pen { color: Red, mark: Tagged("x"), width: 2 };
  let q = remark(p, Plain);
  if (!red(q.color) || q.width != 2) { return 5; }
  let pair = (Black, "two");
  if (red(pair.0)) { return 6; }
  let cell: Cell[Color] = cell_new(Red);
  cell.set(Black);
  if (red(cell.get())) { return 7; }
  print("ok");
  return 0;
}
`

// TestSelfHostStaticEnumHoldsNoCount checks that a value of an enum whose
// variants hold no field is not counted, and that an enum with a payload
// variant still is.
func TestSelfHostStaticEnumHoldsNoCount(t *testing.T) {
	h := selfHostCLIForHost(t)
	src := filepath.Join(t.TempDir(), "static_enum.fern")
	if err := os.WriteFile(src, []byte(staticEnumProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	paint := certifierDumpOf(t, h, src, "paint")
	if !strings.Contains(paint, "st_at 8 ") || strings.Contains(paint, "__fern_rc_inc v1\n") {
		t.Errorf("paint must store its Color parameter with no retain:\n%s", paint)
	}
	if strings.Contains(paint, "ld_at 8 rc") {
		t.Errorf("paint must not read the old Color field to release it:\n%s", paint)
	}
	if !strings.Contains(paint, "ld_at 16 rc v0") {
		t.Errorf("paint must still count the Tree children it copies:\n%s", paint)
	}
	shade := certifierDumpOf(t, h, src, "shade")
	if !strings.Contains(shade, "ld_at 8 v0") || strings.Contains(shade, "__fern_rc_inc") {
		t.Errorf("shade must read the Color field as a word, with no retain:\n%s", shade)
	}
	remark := certifierDumpOf(t, h, src, "remark")
	if !strings.Contains(remark, "__sem_release_Mark") {
		t.Errorf("remark must still release the Mark field it replaces:\n%s", remark)
	}

	if out, err := exec.Command(buildLangBinForInterp(t), "-interp", src).CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("interpreter: %v\n%s", err, out)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, src, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d (1-7 name the check in main that failed): %s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
