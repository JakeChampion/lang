package e2ecompiler

import (
	"fmt"
	"os/exec"
	"testing"
)

type allocDiffCase struct {
	name     string
	decls    string
	n        int
	cliff    bool
	bumpKB   int
	maxRatio int
}

const leakShrinkFactor = 2

var allocDiffCases = []allocDiffCase{
	{
		name: "append-threaded-through-call",
		decls: `function step(acc: i32[], v: i32): i32[] { return acc.append(v); }
function churn(n: i32): i32 {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < n) { a = step(a, i); i = i + 1; }
    return a.len();
}`,
		n:        400,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
	{
		name: "record-threaded-through-loop-phi",
		decls: `struct State { values: i32[], names: string[] }
struct Value { s: State, id: i32 }
@noinline function define(s: State, x: i32): Value {
    let n: i32 = s.values.len();
    return Value { s: State { ...s, values: s.values.append(x) }, id: n };
}
@noinline function walk(s: State, n: i32): State {
    let i: i32 = 0;
    while (i < n) { let v: Value = define(s, i); s = v.s; i = i + 1; }
    return s;
}
function churn(n: i32): i32 {
    let s: State = State { values: [], names: [] };
    let i: i32 = 0;
    while (i < n) { s = walk(s, 3); i = i + 1; }
    return s.values.len();
}`,
		n:        100,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
	{
		name: "append-into-element",
		decls: `function churn(n: i32): i32 {
    let out: i32[][] = [[], [], [], []];
    let i: i32 = 0;
    while (i < n) {
        let b: i32 = i % 4;
        out = out.with(b, out[b].append(i));
        i = i + 1;
    }
    return out[0].len() + out[3].len();
}`,
		n:        400,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
	{
		name: "with-through-borrowed-param",
		decls: `function fill(buf: i32[], n: i32): i32[] {
    let i: i32 = 0;
    while (i < n) { buf = buf.with(i, i * 2); i = i + 1; }
    return buf;
}
function churn(n: i32): i32 {
    let b: i32[] = [];
    let i: i32 = 0;
    while (i < n) { b = b.append(0); i = i + 1; }
    b = fill(b, n);
    return b[n - 1];
}`,
		n:        80,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
	{
		name: "fresh-array-per-iteration",
		decls: `function churn(n: i32): i32 {
    let i: i32 = 0;
    let s: i32 = 0;
    while (i < n) {
        let row: i32[] = [i, i + 1, i + 2];
        s = (s + row[0]) % 251;
        i = i + 1;
    }
    return s;
}`,
		n:        400,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
	{
		name: "struct-with-array-field",
		decls: `struct Row { xs: i32[], k: i32 }
function mk(k: i32): Row { return Row { xs: [k, k + 1, k + 2], k: k }; }
function churn(n: i32): i32 {
    let i: i32 = 0;
    let s: i32 = 0;
    while (i < n) {
        let r: Row = mk(i);
        s = (s + r.xs[1] + r.k) % 251;
        i = i + 1;
    }
    return s;
}`,
		n:        400,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
	{
		name: "loop-push-behind-guard-clause",
		decls: `struct Val { kind: i32, kids: i32[] }
function churn(n: i32): i32 {
    let vals: Val[] = [];
    let total: i32 = 0;
    for i in 0..n {
        if (i == 9999) { return 12345; }
        let v = Val { kind: i, kids: [] };
        vals = vals.append(v);
        total = (total + vals.len()) % 251;
    }
    return total;
}`,
		n:        200,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
	{
		name: "tuple-in-struct-field",
		decls: `struct Hold { t: (i32, i32[]), n: i32 }
function churn(n: i32): i32 {
    let i: i32 = 0;
    let s: i32 = 0;
    while (i < n) {
        let k: (i32, i32[]) = (i, [i, i + 1]);
        let h: Hold = Hold { t: k, n: i };
        s = (s + h.n + h.t.1[1]) % 251;
        i = i + 1;
    }
    return s;
}`,
		n:        200,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
	{
		name: "option-array-alias-match",
		decls: `function churn(n: i32): i32 {
    let i: i32 = 0;
    let s: i32 = 0;
    while (i < n) {
        let src: Option[i32[]] = Some([i, i + 1]);
        let x: Option[i32[]] = src;
        match (x) { Some(xs) => { s = (s + xs.len()) % 251; }, None => {} }
        match (src) { Some(ys) => { s = (s + ys[0]) % 251; }, None => {} }
        i = i + 1;
    }
    return s;
}`,
		n:        200,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
	{
		name: "option-array-struct-field",
		decls: `struct H { o: Option[i32[]], n: i32 }
function churn(n: i32): i32 {
    let i: i32 = 0;
    let s: i32 = 0;
    while (i < n) {
        let h: H = H { o: Some([i, i + 1]), n: i };
        match (h.o) { Some(xs) => { s = (s + xs.len() + h.n) % 251; }, None => {} }
        i = i + 1;
    }
    return s;
}`,
		n:        200,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
	{
		name: "enum-rc-payload-per-iteration",
		decls: `enum E { Full(i32[]), None }
function churn(n: i32): i32 {
    let i: i32 = 0;
    let s: i32 = 0;
    while (i < n) {
        let e: E = E.Full([i, i + 1, i + 2]);
        match (e) { E.Full(xs) => { s = (s + xs.len() + xs[0]) % 251; }, E.None => {} }
        i = i + 1;
    }
    return s;
}`,
		n:        200,
		cliff:    false,
		bumpKB:   0,
		maxRatio: 8,
	},
}

func (c allocDiffCase) bumpSrc() string {
	return fmt.Sprintf(`%s
function main(): i32 {
    let w: i32 = churn(%d);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(%d);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (w != x) { return 251; }
    let kb: i32 = (b2 - b1) / 1024;
    if (kb > 240) { return 252; }
    return kb;
}
`, c.decls, c.n, c.n)
}

func (c allocDiffCase) cliffSrc() string {
	return fmt.Sprintf(`%s
function main(): i32 {
    let w: i32 = churn(%d);
    if (w < 0) { return 251; }
    let n: i32 = __arr_push_shared_count();
    if (n > 240) { return 240; }
    return n;
}
`, c.decls, c.n)
}

func allocRatio(recorded, measured int) int {
	if measured <= recorded {
		return 0
	}
	if recorded < 1 {
		recorded = 1
	}
	return measured / recorded
}

func TestSelfHostAllocDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	selfHostExit := func(t *testing.T, label, src string) int {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(src))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", label)
		}
		bin := buildBin(t, gcc, dir, label, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}

	for _, tc := range allocDiffCases {
		t.Run(tc.name, func(t *testing.T) {
			cliff := selfHostExit(t, tc.name+"-cliff", tc.cliffSrc())
			if cliff >= 250 {
				t.Fatalf("cliff probe self-check failed (%d); 251 means the churn returned a negative value", cliff)
			}
			if (cliff != 0) != tc.cliff {
				t.Errorf("rc==1 append cliff: crossed it %d time(s), recorded %v — the compiler now "+
					"copies a buffer it used to mutate in place, or the reverse, which is the O(n) vs "+
					"O(n²) split; re-record `cliff` only if the change is intended", cliff, tc.cliff)
			}

			kb := selfHostExit(t, tc.name+"-bump", tc.bumpSrc())
			if kb == 251 {
				t.Fatal("the two churns returned different values — the probe is not measuring identical work")
			}
			if kb == 252 {
				t.Fatal("per-churn growth exceeded the 240 KB the exit code can carry; lower this case's n")
			}
			ratio := allocRatio(tc.bumpKB, kb)

			t.Logf("n=%d  bump: %d KB per churn (recorded %d KB, %dx, bound %dx)  cliff: %d (recorded %v)",
				tc.n, kb, tc.bumpKB, ratio, tc.maxRatio, cliff, tc.cliff)
			if ratio > tc.maxRatio {
				t.Errorf("per-churn allocation grew %dx past the recorded figure: %d KB now, %d KB "+
					"recorded (bound %dx). A regression, unless the growth is intended, in which "+
					"case re-record `bumpKB` from the line above", ratio, kb, tc.bumpKB, tc.maxRatio)
			}
			if tc.bumpKB > 0 && kb*leakShrinkFactor < tc.bumpKB {
				t.Errorf("the recorded leak shrank from %d KB to %d KB per churn (below a %dth of the "+
					"figure): a fix has landed, whole or partial. Re-record `bumpKB` from the line "+
					"above, and if the leak is gone, retire the leak note on the row",
					tc.bumpKB, kb, leakShrinkFactor)
			}
		})
	}
}
