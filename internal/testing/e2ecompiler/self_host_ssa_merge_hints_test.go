package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// mergeHintProg is an else-if chain inside a loop over sixteen values, more
// than x86-64 has registers for. At each merge down the chain, the operand
// from the paths that leave a value alone is the loop header's phi, live
// through the whole body, so a merge phi hinted at it never shares a home and
// the arm's fresh sums went to the frame instead (ssa.phi_mates).
const mergeHintProg = `@noinline function carry(n: i64, x: i64): i64 {
    let a: i64 = 1i64; let b: i64 = 2i64; let c: i64 = 3i64; let d: i64 = 4i64;
    let e: i64 = 5i64; let f: i64 = 6i64; let g: i64 = 7i64; let h: i64 = 8i64;
    let p: i64 = 9i64; let q: i64 = 10i64; let r: i64 = 11i64; let s: i64 = 12i64;
    let t: i64 = 13i64; let u: i64 = 14i64; let w: i64 = 15i64; let y: i64 = 16i64;
    let i: i64 = 0i64;
    while (i < n) {
        let k: i64 = (i + x) % 5i64;
        if (k == 0i64) {
            a = a + x; b = b + a; c = c + b; d = d + c;
        } else if (k == 1i64) {
            e = e + x; f = f + e; g = g + f; h = h + g;
        } else if (k == 2i64) {
            p = p + x; q = q + p; r = r + q; s = s + r;
        } else if (k == 3i64) {
            t = t + x; u = u + t; w = w + u; y = y + w;
        } else {
            a = a + 1i64; y = y + 1i64;
        }
        i = i + 1i64;
    }
    return a + b + c + d + e + f + g + h + p + q + r + s + t + u + w + y;
}
function main(): i32 { return (carry(1000i64, 3i64) % 200i64) as i32; }
`

// No arm of carry's chain stores a sum to the frame and reads it straight
// back.
func TestSelfHostSSAMergePhiHintsADyingOperand(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "carry.fern")
	if err := os.WriteFile(src, []byte(mergeHintProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "carry.s")
	if combined, err := exec.Command(h.cli, "-O", "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	fn := functionListing(string(asm), "__fn_carry")
	if fn == "" {
		t.Fatal("__fn_carry not in the listing")
	}
	lines := strings.Split(fn, "\n")
	test := regexp.MustCompile(`^\s*cmpq \$\d+, `)
	jne := regexp.MustCompile(`^\s*jne `)
	jmp := regexp.MustCompile(`^\s*jmp `)
	store := regexp.MustCompile(`^\s*movq %\w+, (-?\d+)\(%rbp\)$`)
	load := regexp.MustCompile(`^\s*movq (-?\d+)\(%rbp\), %\w+$`)
	arms := 0
	for i := 1; i < len(lines); i++ {
		if !jne.MatchString(lines[i]) || !test.MatchString(lines[i-1]) {
			continue
		}
		arms++
		for i++; i < len(lines) && !jmp.MatchString(lines[i]); i++ {
			s := store.FindStringSubmatch(lines[i])
			if s == nil || i+1 == len(lines) {
				continue
			}
			if l := load.FindStringSubmatch(lines[i+1]); l != nil && l[1] == s[1] {
				t.Errorf("an arm stores to %s(%%rbp) and reads it straight back:\n%s\n%s\n\n%s", s[1], lines[i], lines[i+1], fn)
			}
		}
	}
	if arms != 4 {
		t.Fatalf("found %d arms in carry, want the four tested ones:\n%s", arms, fn)
	}
	if !regexp.MustCompile(`(?m)^\s*movq %\w+, -\d+\(%rbp\)$`).MatchString(fn) {
		t.Fatalf("carry spills nothing, so the check proves nothing:\n%s", fn)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin, "-O")
		if _, code := h.runProduced(t, tg, bin); code != 86 {
			t.Errorf("%s: exit %d, want 86", tg.target, code)
		}
	}
}

// evictionProg is ssa.prune_mark, whose values sharing a register include one
// still live after one whose interval ended: they are not ordered by where
// their intervals end. An eviction spills every one still live, not only those
// before the first that ended (ssa.still_holds).
const evictionProg = `struct Inst { result: i32, args: i32[] }
struct Blk { insts: Inst[] }
struct Fn { nvals: i32, blocks: Blk[] }

function zeros(n: i32): i32[] {
  let out: i32[] = [];
  let i: i32 = 0;
  while (i < n) {
    out = out.append(0);
    i = i + 1;
  }
  return out;
}

@noinline function prune_mark(f: Fn, work: i32[]): i32[] {
  let live: i32[] = zeros(f.nvals);
  let def_b: i32[] = zeros(f.nvals);
  let def_k: i32[] = zeros(f.nvals);
  let bi: i32 = 0;
  while (bi < f.blocks.len()) {
    let k: i32 = 0;
    while (k < f.blocks[bi].insts.len()) {
      let r: i32 = f.blocks[bi].insts[k].result;
      def_b = def_b.with(r, bi + 1);
      def_k = def_k.with(r, k);
      k = k + 1;
    }
    bi = bi + 1;
  }
  let at: i32 = 0;
  while (at < work.len()) {
    let v: i32 = work[at];
    at = at + 1;
    if (v < 0 || v >= f.nvals || live[v] == 1) {
      continue;
    }
    live = live.with(v, 1);
    if (def_b[v] == 0) {
      continue;
    }
    for a in f.blocks[def_b[v] - 1].insts[def_k[v]].args {
      work = work.append(a);
    }
  }
  return live;
}

function main(): i32 {
  let b0: Blk = Blk { insts: [Inst { result: 0, args: [] }, Inst { result: 1, args: [0] }, Inst { result: 2, args: [1, 0] }] };
  let b1: Blk = Blk { insts: [Inst { result: 3, args: [2] }, Inst { result: 4, args: [] }, Inst { result: 5, args: [3, 4] }, Inst { result: 6, args: [] }] };
  let live: i32[] = prune_mark(Fn { nvals: 8, blocks: [b0, b1] }, [5]);
  let s: i32 = 0;
  let i: i32 = 0;
  while (i < live.len()) {
    s = s * 2 + live[i];
    i = i + 1;
  }
  return s;
}
`

// Values 0 to 5 are reachable from 5 and 6 and 7 are not: 0b11111100.
func TestSelfHostSSAEvictionSpillsEveryLiveSharer(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prune.fern")
	if err := os.WriteFile(src, []byte(evictionProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		for _, opt := range [][]string{nil, {"-O"}} {
			bin := filepath.Join(dir, tg.target+strings.Join(opt, "")+".bin")
			h.compileWith(t, tg, src, bin, opt...)
			if _, code := h.runProduced(t, tg, bin); code != 252 {
				t.Errorf("%s %v: exit %d, want 252", tg.target, opt, code)
			}
		}
	}
}
