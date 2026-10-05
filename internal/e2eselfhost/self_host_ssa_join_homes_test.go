package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// joinHomeProg is an else-if chain of twenty arms inside a loop, each arm
// adding to one of twenty loop-carried sums, more than x86-64 has registers
// for. At each merge down the chain, the sums the arm left alone arrive as the
// loop header's phis, which live through the whole body. A merge phi that
// takes a home of its own copies every one of them on every arm's edge; one
// that shares the header phi's home copies nothing (ssa.chain_dead_at_def).
const joinHomeProg = `function f(ops: i32[]): i32 {
  let v0: i32 = 0; let v1: i32 = 0; let v2: i32 = 0; let v3: i32 = 0; let v4: i32 = 0;
  let v5: i32 = 0; let v6: i32 = 0; let v7: i32 = 0; let v8: i32 = 0; let v9: i32 = 0;
  let v10: i32 = 0; let v11: i32 = 0; let v12: i32 = 0; let v13: i32 = 0; let v14: i32 = 0;
  let v15: i32 = 0; let v16: i32 = 0; let v17: i32 = 0; let v18: i32 = 0; let v19: i32 = 0;
  let i: i32 = 0;
  while (i < ops.len()) {
    let k: i32 = ops[i];
    if (k == 0) { v0 = v0 + k; } else if (k == 1) { v1 = v1 + k; } else if (k == 2) { v2 = v2 + k; }
    else if (k == 3) { v3 = v3 + k; } else if (k == 4) { v4 = v4 + k; } else if (k == 5) { v5 = v5 + k; }
    else if (k == 6) { v6 = v6 + k; } else if (k == 7) { v7 = v7 + k; } else if (k == 8) { v8 = v8 + k; }
    else if (k == 9) { v9 = v9 + k; } else if (k == 10) { v10 = v10 + k; } else if (k == 11) { v11 = v11 + k; }
    else if (k == 12) { v12 = v12 + k; } else if (k == 13) { v13 = v13 + k; } else if (k == 14) { v14 = v14 + k; }
    else if (k == 15) { v15 = v15 + k; } else if (k == 16) { v16 = v16 + k; } else if (k == 17) { v17 = v17 + k; }
    else if (k == 18) { v18 = v18 + k; } else if (k == 19) { v19 = v19 + k; }
    i = i + 1;
  }
  return v0 + v1 + v2 + v3 + v4 + v5 + v6 + v7 + v8 + v9 + v10 + v11 + v12 + v13 + v14 + v15 + v16 + v17 + v18 + v19;
}
function main(): i32 { let xs: i32[] = [1, 2, 3, 19, 0, 7]; return f(xs); }
`

// Each arm of f's chain is its own sum and the jump: a load, the add and a
// store when the sum is spilled, at most three instructions.
func TestSelfHostSSAJoinPhiSharesTheHeaderHome(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "chain.fern")
	if err := os.WriteFile(src, []byte(joinHomeProg), 0o644); err != nil {
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
	lines := strings.Split(fn, "\n")
	test := regexp.MustCompile(`^\s*cmpq \$\d+, `)
	jne := regexp.MustCompile(`^\s*jne `)
	jmp := regexp.MustCompile(`^\s*jmp `)
	label := regexp.MustCompile(`^\s*\.L\w+:$`)
	arms := 0
	for i := 1; i < len(lines); i++ {
		if !jne.MatchString(lines[i]) || !test.MatchString(lines[i-1]) {
			continue
		}
		start := i + 1
		n := 0
		for i++; i < len(lines) && !jmp.MatchString(lines[i]); i++ {
			if strings.TrimSpace(lines[i]) != "" && !label.MatchString(lines[i]) {
				n++
			}
		}
		if i == len(lines) {
			break
		}
		arms++
		if n > 3 {
			t.Errorf("an arm runs %d instructions before its jump, want at most 3:\n%s", n, strings.Join(lines[start:i+1], "\n"))
		}
	}
	if arms < 19 {
		t.Fatalf("found %d arms in f, want at least 19:\n%s", arms, fn)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin, "-O")
		if _, code := h.runProduced(t, tg, bin); code != 32 {
			t.Errorf("%s: exit %d, want 32", tg.target, code)
		}
	}
}
