package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var constAggBlockLabel = regexp.MustCompile(`(?m)^\.K\d+:$`)

var constAggCases = []struct {
	name     string
	src      string
	expected int
	allocs   int
	kBlocks  int
}{
	{"nullary-ctors",
		`struct TypeI32 { is_char: boolean, width: i32, unsigned: boolean } struct TypeString { tag: i32 } type Type = TypeI32 | TypeString; function t_i32(): Type { return TypeI32 { is_char: false, width: 32, unsigned: false }; } function t_u64(): Type { return TypeI32 { is_char: false, width: 64, unsigned: true }; } function t_str(): Type { return TypeString { tag: 0 }; } function w(t: Type): i32 { match (t) { TypeI32(v) => { return v.width; }, TypeString(_) => { return 7; } } return 0; } function main(): i32 { let a: Type = t_i32(); let b: Type = t_u64(); let c: Type = t_str(); return (w(a) + w(b) + w(c)) % 200; }`,
		103, 0, 3},
	{"hot-loop-zero-allocs",
		`struct TypeI32 { is_char: boolean, width: i32, unsigned: boolean } struct TypeString { tag: i32 } type Type = TypeI32 | TypeString; function t_i32(): Type { return TypeI32 { is_char: false, width: 32, unsigned: false }; } function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { let t: Type = t_i32(); match (t) { TypeI32(v) => { acc = (acc + v.width) % 1000; }, TypeString(_) => { acc = acc + 1; } } i = i + 1; } return acc % 100; }`,
		0, 0, 1},
	{"identical-literals-intern",
		`struct P { a: i32, b: i32 } function one(): P { return P { a: 1, b: 2 }; } function two(): P { return P { a: 1, b: 2 }; } function three(): P { return P { a: 9, b: 9 }; } function main(): i32 { return (one().a + two().b + three().a) % 200; }`,
		12, 0, 2},
	{"update-does-not-write-through",
		`struct P { a: i32, b: boolean } function mk(): P { return P { a: 5, b: true }; } function main(): i32 { let p: P = mk(); let q: P = P { ...p, a: p.a + 1 }; let r: P = mk(); let t: i32 = 0; if (p.b) { t = t + 1; } if (q.b) { t = t + 2; } return (p.a * 10 + q.a + r.a + t) % 200; }`,
		64, 1, 1},
	{"churn-does-not-corrupt",
		`struct P { a: i32, b: i32 } function mk(): P { return P { a: 1, b: 2 }; } function main(): i32 { let p: P = mk(); let i: i32 = 0; while (i < 100000) { p = P { ...p, a: (p.a + p.b) % 977 }; i = i + 1; } let fresh: P = mk(); return (p.a % 100) * 2 + fresh.a * 10 + fresh.b; }`,
		198, 1, 1},
	{"constant-into-container",
		`struct P { a: i32, b: i32 } function mk(): P { return P { a: 5, b: 9 }; } function main(): i32 { let ps: P[] = []; let i: i32 = 0; while (i < 4) { ps = ps.append(mk()); i = i + 1; } let s: i32 = 0; let j: i32 = 0; while (j < ps.len()) { s = s + ps[j].a + ps[j].b; j = j + 1; } return s % 200; }`,
		56, 1, 2},
	{"reuse-shape-all-constant",
		`struct P { x: i32, y: i32 } function main(): i32 { let cond: i32 = 1; let r: i32 = 0; if (cond > 0) { let a: P = P { x: 10, y: 20 }; let s: i32 = a.x + a.y; let b: P = P { x: 3, y: 4 }; r = s + b.x + b.y; } return r; }`,
		37, 0, 2},
	{"folded-field-admitted",
		`struct P { a: i32, b: i32 } function mk(): P { return P { a: 0 - 3, b: 0x10 }; } function main(): i32 { let p: P = mk(); return (p.a + p.b + 100) % 200; }`,
		113, 0, 1},
	{"wide-field-admitted",
		`struct W { n: i64, k: i32 } function mk(): W { return W { n: 5000000000, k: 3 }; } function main(): i32 { let w: W = mk(); if (w.n > 4000000000) { return w.k + 10; } return 0; }`,
		13, 0, 1},
	{"f64-field-admitted",
		`struct F { x: f64, k: i32 } function mk(): F { return F { x: 2.5, k: 1 }; } function main(): i32 { let f: F = mk(); if (f.x > 2.0 && f.x < 3.0) { return f.k + 20; } return 0; }`,
		21, 0, 1},
	{"nested-wide-records",
		`struct D { sec: i64, nsec: i64 } struct T { a: D, b: D } function mk(): T { return T { a: D { sec: 10 as i64, nsec: 0 }, b: D { sec: 30 as i64, nsec: 0 } }; } @noinline function keep(t: T): i64 { return t.a.sec + t.b.sec + t.a.nsec; } function main(): i32 { return keep(mk()) as i32; }`,
		40, 0, 3},
}

func TestSelfHostConstAggregateIRX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range constAggCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			if got := len(constAggBlockLabel.FindAllString(asm, -1)); got != tc.kBlocks {
				t.Errorf("%s: %d static-constant blocks emitted, want %d — the const_struct admission moved, or interning stopped collapsing identical literals", tc.name, got, tc.kBlocks)
			}
			progBin := buildBin(t, gcc, dir, tc.name, asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
			if got := leakcheckAllocs(t, string(out)); got != tc.allocs {
				t.Errorf("%s: %d allocations, want %d — a constant that stopped being static reads as extra allocs; one that wrongly became static reads as fewer", tc.name, got, tc.allocs)
			}
		})
	}
}

func leakcheckAllocs(t *testing.T, out string) int {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "leakcheck: allocs=") {
			continue
		}
		var allocs, frees, live int64
		if _, err := fmtSscan(line, &allocs, &frees, &live); err != nil {
			t.Fatalf("unparsable leakcheck line %q: %v", line, err)
		}
		return int(allocs)
	}
	t.Fatalf("no leakcheck summary in output:\n%s", out)
	return -1
}
