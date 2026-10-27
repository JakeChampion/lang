package e2ecompiler

import (
	"path/filepath"
	"regexp"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const knownVariantSrc = `enum Expr { Empty, Val(i32), Neg(Expr) }

@noinline
function vals(own a: Expr, own b: Expr): i32 {
  match (a) {
    Val(x) => {
      match (b) {
        Val(y) => {
          return x + y;
        },
        _ => {
          return x;
        }
      }
    },
    _ => {
      return 0;
    }
  }
}

@noinline
function val(n: i32): Expr {
  return Val(n);
}

@noinline
function neg(n: i32): Expr {
  return Neg(Val(n));
}

@noinline
function empty_case(own a: Expr): i32 {
  match (a) {
    Empty => { return 0; },
    _ => { return 1; }
  }
}

function main(): i32 {
  let bad: i32 = 0;
  if (vals(val(3), val(4)) != 7 || vals(val(3), neg(1)) != 3 || vals(neg(2), val(5)) != 0) {
    bad = bad + 1;
  }
  let a: Expr = val(8);
  let b: Expr = neg(9);
  let held: Expr[] = [a, b];
  if (vals(a, b) != 8 || held.len() != 2) {
    bad = bad + 2;
  }
  let c: Expr = val(1);
  let d: Expr = val(2);
  let held2: Expr[] = [c, d];
  if (vals(c, d) != 3 || held2.len() != 2) {
    bad = bad + 4;
  }
  if (empty_case(Empty) != 0 || empty_case(val(1)) != 1) {
    bad = bad + 8;
  }
  return bad;
}`

var semReleaseExprCall = regexp.MustCompile(`(?m)^\s+(?:call|bl) __fn___sem_release_Expr(?:\.r)?$`)

func TestSelfHostKnownVariantDropX86_64(t *testing.T) {
	emit, run := x86ShapeHarness(t)
	asm := emit(t, knownVariantSrc)
	if got := len(semReleaseExprCall.FindAllString(fnText(t, asm, "vals"), -1)); got != 3 {
		t.Errorf("vals: %d calls of the release helper, want 3: the drops of a known Val went through the helper", got)
	}
	if got := len(semReleaseExprCall.FindAllString(fnText(t, asm, "empty_case"), -1)); got != 1 {
		t.Errorf("empty_case: %d release-helper calls, want 1 for the unknown-variant arm", got)
	}
	if got := run(t, "known_variant", asm); got != 0 {
		t.Errorf("exit = %d, want 0 (bits 1-4: nonempty values, bit 8: empty variant)", got)
	}
}

func TestSelfHostKnownVariantDropTargets(t *testing.T) {
	dir := t.TempDir()
	writeResolveProject(t, dir, map[string]string{"main.fern": knownVariantSrc})
	path := filepath.Join(dir, "main.fern")
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostFile(t, target, path, []string{"FERN_LEAKCHECK=1"})
			out, err := mvsDriverCommand(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("known-variant destruction: %v\n%s", err, out)
			}
			assertBalancedCensus(t, string(out))
		})
	}
}
