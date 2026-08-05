package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An aggregate literal whose every field is a compile-time scalar literal is a
// CONSTANT, and #6149 measured the checker's nullary type constructors —
// `t_i32() { return TypeI32 { is_char: false, width: 32, unsigned: false }; }`
// and friends — freshly heap-allocating one on every call, ~21% of every
// allocation the self-host compiler makes, each box leaked. They now lower to
// `const_struct`, which the register backends place in static data.
//
// The instrument is allocation COUNT (`FERN_LEAKCHECK=1`, read at emit time by
// the self-host x86-64 runtime), plus the emitted `leaq .K…` sites. Neither
// `__heap_bump_bytes()` nor the append-cliff counter can see this: the boxes are
// freelist-recycled and no appends are involved.
//
// The soundness cases are the ones to keep: a static box is SHARED by every
// evaluation, so anything that could write through it has to be shown not to.
// The block's rc word is the immortal sentinel, which makes
// `__fern_rc_is_unique` answer 0, so a record update or an FBIP reuse targeting
// a constant degrades to a fresh allocation — `update-does-not-write-through`
// and `churn-does-not-corrupt` are that proof, and they would read wrong (not
// merely slow) if the degradation stopped working.
var constAggCases = []struct {
	name     string
	src      string
	expected int
	allocs   int // FERN_LEAKCHECK allocation count
	kBlocks  int // `.K<n>:` label definitions — distinct static constants EMITTED
}{
	// The idiom itself: three nullary constructors, called once each. Zero
	// allocations, three interned blocks.
	{"nullary-ctors",
		`struct TypeI32 { is_char: boolean, width: i32, unsigned: boolean } struct TypeString { tag: i32 } type Type = TypeI32 | TypeString; function t_i32(): Type { return TypeI32 { is_char: false, width: 32, unsigned: false }; } function t_u64(): Type { return TypeI32 { is_char: false, width: 64, unsigned: true }; } function t_str(): Type { return TypeString { tag: 0 }; } function w(t: Type): i32 { match (t) { TypeI32(v) => { return v.width; }, TypeString(_) => { return 7; } } return 0; } function main(): i32 { var a: Type = t_i32(); var b: Type = t_u64(); var c: Type = t_str(); return (w(a) + w(b) + w(c)) % 200; }`,
		103, 0, 3},
	// The allocation contract at scale: 100 calls, still zero allocations. This
	// is the shape the issue measured (552 calls to one constructor).
	{"hot-loop-zero-allocs",
		`struct TypeI32 { is_char: boolean, width: i32, unsigned: boolean } struct TypeString { tag: i32 } type Type = TypeI32 | TypeString; function t_i32(): Type { return TypeI32 { is_char: false, width: 32, unsigned: false }; } function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { var t: Type = t_i32(); match (t) { TypeI32(v) => { acc = (acc + v.width) % 1000; }, TypeString(_) => { acc = acc + 1; } } i = i + 1; } return acc % 100; }`,
		0, 0, 1},
	// INTERNING: the same literal from two constructors is ONE BLOCK, even
	// though three call sites reference a constant. Without dedup the whole
	// point (one address for 552 calls) is lost — which is why this asserts on
	// the label DEFINITIONS, not on the `leaq .K…` references.
	{"identical-literals-intern",
		`struct P { a: i32, b: i32 } function one(): P { return P { a: 1, b: 2 }; } function two(): P { return P { a: 1, b: 2 }; } function three(): P { return P { a: 9, b: 9 }; } function main(): i32 { return (one().a + two().b + three().a) % 200; }`,
		12, 0, 2},
	// SOUNDNESS: a record update whose base is a constant must not write through
	// the shared block — `r` reads the constant again afterwards and must still
	// see a=5. p.a*10 + q.a + r.a + (b flags) = 50 + 6 + 5 + 3.
	{"update-does-not-write-through",
		`struct P { a: i32, b: boolean } function mk(): P { return P { a: 5, b: true }; } function main(): i32 { var p: P = mk(); var q: P = P { ...p, a: p.a + 1 }; var r: P = mk(); var t: i32 = 0; if (p.b) { t = t + 1; } if (q.b) { t = t + 2; } return (p.a * 10 + q.a + r.a + t) % 200; }`,
		64, 1, 1},
	// SOUNDNESS at scale: 100k updates threaded off a constant, then the
	// constant is read again. A single write-through would corrupt `fresh`.
	{"churn-does-not-corrupt",
		`struct P { a: i32, b: i32 } function mk(): P { return P { a: 1, b: 2 }; } function main(): i32 { var p: P = mk(); var i: i32 = 0; while (i < 100000) { p = P { ...p, a: (p.a + p.b) % 977 }; i = i + 1; } var fresh: P = mk(); return (p.a % 100) * 2 + fresh.a * 10 + fresh.b; }`,
		198, 100000, 1},
	// A constant moved into a container: the array owns a pointer to the shared
	// block, and the exit sweep must not free it (the sentinel is what stops it).
	{"constant-into-container",
		`struct P { a: i32, b: i32 } function mk(): P { return P { a: 5, b: 9 }; } function main(): i32 { var ps: P[] = []; var i: i32 = 0; while (i < 4) { ps = ps.append(mk()); i = i + 1; } var s: i32 = 0; var j: i32 = 0; while (j < ps.len()) { s = s + ps[j].a + ps[j].b; j = j + 1; } return s % 200; }`,
		56, 2, 1},
	// NOT admitted: a field value that is not a literal keeps the whole literal
	// on struct_make. `0 - 3` is a binary expression, not a unary minus, so this
	// is also the control that the admission is syntactic and narrow.
	{"non-literal-field-not-admitted",
		`struct P { a: i32, b: i32 } function mk(): P { return P { a: 0 - 3, b: 0x10 }; } function main(): i32 { var p: P = mk(); return (p.a + p.b + 100) % 200; }`,
		113, 1, 0},
	// NOT admitted: a wide (i64) field. The static block writes one word per
	// field, so a field whose box slot is 8 bytes of a different kind stays on
	// struct_make until the encoding widens.
	{"wide-field-not-admitted",
		`struct W { n: i64, k: i32 } function mk(): W { return W { n: 5000000000, k: 3 }; } function main(): i32 { var w: W = mk(); if (w.n > 4000000000) { return w.k + 10; } return 0; }`,
		13, 1, 0},
}

// TestSelfHostConstAggregateIRX86_64 compiles each case through the self-hosted
// x86-64 driver, asserting the exit code, the emitted static-constant sites, and
// the runtime allocation count.
func TestSelfHostConstAggregateIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range constAggCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			if got := strings.Count(asm, "\n.K"); got != tc.kBlocks {
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

// leakcheckAllocs pulls the alloc count out of the FERN_LEAKCHECK=1 exit
// summary, through the same parser the rc-trace suite uses.
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
