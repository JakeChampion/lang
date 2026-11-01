package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const mergeChainProg = `@noinline function f(ops: i32[]): i32 {
  let v0: i32 = 0;
  let v1: i32 = 0;
  let v2: i32 = 0;
  let v3: i32 = 0;
  let v4: i32 = 0;
  let v5: i32 = 0;
  let v6: i32 = 0;
  let v7: i32 = 0;
  let v8: i32 = 0;
  let v9: i32 = 0;
  let v10: i32 = 0;
  let v11: i32 = 0;
  let v12: i32 = 0;
  let v13: i32 = 0;
  let v14: i32 = 0;
  let v15: i32 = 0;
  let v16: i32 = 0;
  let v17: i32 = 0;
  let v18: i32 = 0;
  let v19: i32 = 0;
  let stack: i32[] = [];
  let seen: i32[] = [];
  let n: i32 = 0;
  let i: i32 = 0;
  while (i < ops.len()) {
    let k: i32 = ops[i];
    if (k == 0) { v0 = v0 + k; stack = stack.append(k); n = n + 0; seen = seen.append(n); } else if (k == 1) { v1 = v1 + k; stack = stack.append(k); n = n + 1; seen = seen.append(n); } else if (k == 2) { v2 = v2 + k; stack = stack.append(k); n = n + 2; seen = seen.append(n); } else if (k == 3) { v3 = v3 + k; stack = stack.append(k); n = n + 3; seen = seen.append(n); } else if (k == 4) { v4 = v4 + k; stack = stack.append(k); n = n + 4; seen = seen.append(n); } else if (k == 5) { v5 = v5 + k; stack = stack.append(k); n = n + 5; seen = seen.append(n); } else if (k == 6) { v6 = v6 + k; stack = stack.append(k); n = n + 6; seen = seen.append(n); } else if (k == 7) { v7 = v7 + k; stack = stack.append(k); n = n + 7; seen = seen.append(n); } else if (k == 8) { v8 = v8 + k; stack = stack.append(k); n = n + 8; seen = seen.append(n); } else if (k == 9) { v9 = v9 + k; stack = stack.append(k); n = n + 9; seen = seen.append(n); } else if (k == 10) { v10 = v10 + k; stack = stack.append(k); n = n + 10; seen = seen.append(n); } else if (k == 11) { v11 = v11 + k; stack = stack.append(k); n = n + 11; seen = seen.append(n); } else if (k == 12) { v12 = v12 + k; stack = stack.append(k); n = n + 12; seen = seen.append(n); } else if (k == 13) { v13 = v13 + k; stack = stack.append(k); n = n + 13; seen = seen.append(n); } else if (k == 14) { v14 = v14 + k; stack = stack.append(k); n = n + 14; seen = seen.append(n); } else if (k == 15) { v15 = v15 + k; stack = stack.append(k); n = n + 15; seen = seen.append(n); } else if (k == 16) { v16 = v16 + k; stack = stack.append(k); n = n + 16; seen = seen.append(n); } else if (k == 17) { v17 = v17 + k; stack = stack.append(k); n = n + 17; seen = seen.append(n); } else if (k == 18) { v18 = v18 + k; stack = stack.append(k); n = n + 18; seen = seen.append(n); } else if (k == 19) { v19 = v19 + k; stack = stack.append(k); n = n + 19; seen = seen.append(n); }
    i = i + 1;
  }
  return v0 + v1 + v2 + v3 + v4 + v5 + v6 + v7 + v8 + v9 + v10 + v11 + v12 + v13 + v14 + v15 + v16 + v17 + v18 + v19 + stack.len() + seen.len() + n;
}
function main(): i32 { let xs: i32[] = [1, 2, 3, 19, 0, 7]; return f(xs); }
`

func ssaMergeChainShape(fn string) (int, int) {
	lines := strings.Split(fn, "\n")
	// A merge block's label is the function's block number. A helper's
	// return label, such as an append's, belongs to the arm before the chain,
	// and so do the moves before the chain's first label: a jump to the
	// physical fallthrough is not emitted, so nothing separates them.
	label := regexp.MustCompile(`^\.Lssa_f_[0-9]+:$`)
	mov := regexp.MustCompile(`^\s*movq `)
	labels, moves, bestLabels, bestMoves := 0, 0, 0, 0
	finish := func() {
		if labels > bestLabels || labels == bestLabels && moves > bestMoves {
			bestLabels, bestMoves = labels, moves
		}
		labels, moves = 0, 0
	}
	for _, line := range lines {
		switch {
		case label.MatchString(line):
			labels++
		case mov.MatchString(line):
			if labels > 0 {
				moves++
			}
		default:
			finish()
		}
	}
	finish()
	return bestLabels, bestMoves
}

func TestSSAMergeChainShapeExcludesKernelMoves(t *testing.T) {
	chain := ".Lssa_f_24:\n    movq %r13, %r8\n.Lssa_f_23:\n    movq %r14, %r9\n.Lssa_f_22:\n"
	prefix := strings.Repeat("    movq -216(%rbp), %r8\n", 3) + ".Lssa_f_apush1069e:\n" + strings.Repeat("    movq %rax, %r14\n", 4)
	for _, tc := range []struct {
		name, listing string
		labels, moves int
	}{
		{"chain", chain, 3, 2},
		{"fallthrough kernel prefix", prefix + chain, 3, 2},
		{"extra chain moves", chain + strings.Repeat("    movq %r8, %r13\n", 9), 3, 11},
		{"equal chain with more moves", chain + "    nop\n" + chain + strings.Repeat("    movq %r8, %r13\n", 9), 3, 11},
		{"kernel separates chains", ".Lssa_f_25:\n    movq %r8, %r13\n.Lssa_f_apush12e:\n" + chain, 3, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			labels, moves := ssaMergeChainShape(tc.listing)
			if labels != tc.labels || moves != tc.moves {
				t.Fatalf("labels=%d moves=%d, want labels=%d moves=%d", labels, moves, tc.labels, tc.moves)
			}
		})
	}
}

func TestSelfHostSSAMergeChainMovesNothing(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "chain.fern")
	if err := os.WriteFile(src, []byte(mergeChainProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "chain.s")
	if combined, err := exec.Command(h.cli, "-O", "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	fn := functionListing(string(asm), "__fn_f")
	if fn == "" {
		t.Fatal("__fn_f not in the listing")
	}
	bestLabels, bestMoves := ssaMergeChainShape(fn)
	if bestLabels < 15 {
		t.Fatalf("the longest run of merge blocks in f spans %d labels, want at least 15:\n%s", bestLabels, fn)
	}
	if bestMoves > 8 {
		t.Errorf("the merge chain of f holds %d moves, want at most 8:\n%s", bestMoves, fn)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin, "-O")
		if _, code := h.runProduced(t, tg, bin); code != 76 {
			t.Errorf("%s: exit %d, want 76", tg.target, code)
		}
	}
}
