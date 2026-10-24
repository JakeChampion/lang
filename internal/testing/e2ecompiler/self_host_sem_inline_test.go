package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

const semInlineProgram = `struct Range { lo: i32, hi: i32 }
function span(r: Range): (i32, i32) { return (r.lo, r.hi - r.lo); }
@noinline function kept_span(r: Range): (i32, i32) { return (r.lo, r.hi - r.lo); }
function pick(c: boolean, a: Range, b: Range): (i32, i32) {
    let r: Range = a;
    if (c) { r = b; }
    return (r.lo, r.hi);
}
function divmod(a: i32, b: i32): (i32, i32) { return (a / b, a % b); }
function scan(n: i32): (boolean, i32) {
    if (n > 50) { return (true, n - 50); }
    return (false, n);
}
enum Got { Hit(i32), Miss }
function kind(g: Got, fallback: i32): (boolean, i32) {
    match (g) { Hit(n) => { return (true, n); }, Miss => { return (false, fallback); } }
    return (false, fallback);
}
@noinline function probe(i: i32): Got {
    if (i % 3 == 0) { return Hit(i); }
    return Miss;
}
function clamp(v: i32, lo: i32, hi: i32): i32 {
    if (v < lo) { return lo; }
    if (v > hi) { return hi; }
    return v;
}
@noinline function kept(a: i32, b: i32): (i32, i32) { return (a / b, a % b); }
@noinline function divmod_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let (q, r) = divmod(i + 7, 3); t = t + q + r; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function scan_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let (hit, rest) = scan(i); if (hit) { t = t + rest; } i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function clamp_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + clamp(i, 10, 20); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function kind_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let (hit, n) = kind(probe(i), 5); if (hit) { t = t + n; } else { t = t + n * 2; } i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function kept_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let (q, r) = kept(i + 7, 3); t = t + q + r; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function span_rounds(r: Range): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let (lo, width) = span(r); t = t + lo + width + i; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function kept_span_rounds(r: Range): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let (lo, width) = kept_span(r); t = t + lo + width + i; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function pick_rounds(a: Range, b: Range): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let (lo, hi) = pick(i % 2 == 0, a, b); t = t + lo + hi; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    print_int(divmod_rounds()); print("");
    print_int(scan_rounds()); print("");
    print_int(clamp_rounds()); print("");
    print_int(kept_rounds()); print("");
    print_int(kind_rounds()); print("");
    let r: Range = Range { lo: 3, hi: 10 };
    print_int(span_rounds(r)); print("");
    print_int(kept_span_rounds(r)); print("");
    print_int(pick_rounds(r, Range { lo: 1, hi: 4 })); print("");
    return 0;
}
`

var semInlineProduced = []string{"divmod", "scan", "clamp", "kept", "divmod_rounds", "scan_rounds", "clamp_rounds", "kept_rounds", "kind", "probe", "kind_rounds",
	"span", "kept_span", "span_rounds", "kept_span_rounds", "pick", "pick_rounds"}

const grownLeafProgram = `struct Box3 { a: i32, b: i32, c: i32 }
function first(r: Box3): i32 { return r.a; }
function second(r: Box3): i32 { return r.b + r.c; }
function parts(r: Box3): (i32, i32, i32) { return (first(r), second(r), r.c); }
@noinline function one(r: Box3): i32 { let (x, y, z) = parts(r); return x + y + z; }
@noinline function two(r: Box3): i32 { let (x, y, z) = parts(r); return x * y - z; }
@noinline function rounds(r: Box3): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let (x, y, z) = parts(r); t = t + x + y + z + one(r) + two(r); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    print_int(rounds(Box3 { a: 1, b: 2, c: 3 })); print("");
    return 0;
}
`

var grownLeafProduced = []string{"first", "second", "parts", "one", "two", "rounds"}

func TestSelfHostSemanticInlineGrownLeaf(t *testing.T) {
	runSemanticProgram(t, "grownleaf", grownLeafProgram, grownLeafProduced, semInlineWants("2000000\n"))
}

func TestSelfHostSemanticInlineGrownLeafOff(t *testing.T) {
	t.Setenv("FERN_SEM_INLINE", "")
	runSemanticProgram(t, "grownleaf-off", grownLeafProgram, grownLeafProduced, semInlineWants("2000300\n"))
}

func semInlineWants(want string) map[string]string {
	return map[string]string{"arm64-linux": want, "x86-64-linux": want, "x86-64-sanitize": want, "wasm32-wasi": want}
}

func TestSelfHostSemanticInline(t *testing.T) {
	runSemanticProgram(t, "seminline", semInlineProgram, semInlineProduced,
		semInlineWants("1950000\n1225000\n1845000\n1950000\n2343000\n5950000\n5950000\n900000\n"), "kept", "kept_span", "probe")
}

func TestSelfHostSemanticInlineOff(t *testing.T) {
	t.Setenv("FERN_SEM_INLINE", "")
	runSemanticProgram(t, "seminline-off", semInlineProgram, semInlineProduced,
		semInlineWants("1950100\n1225100\n1845000\n1950100\n2343134\n5950100\n5950100\n900100\n"))
}

const splicedUpdateProgram = `struct P { a: i32, b: i32, name: string }
function bump(p: P): P { return P { ...p, a: p.a + 1 }; }
@noinline function run(ps: P[]): P[] {
    let out: P[] = [];
    for p in ps { out = out.append(bump(p)); }
    return out;
}
function main(): i32 {
    let ps: P[] = [P { a: 1, b: 2, name: "n" }];
    let qs: P[] = run(ps);
    putchar(48 + qs[0].a);
    print("");
    return 0;
}
`

var semDumpUpdate = regexp.MustCompile(`(?m)^\s*v\d+ = k-5 \[[0-9,]*\] imm=(\d+)\s*$`)
var semDumpFieldRead = regexp.MustCompile(`(?m)^\s*v\d+ = k-6 \[(\d+),\] imm=\d+ (b|name)\s*$`)

func TestSelfHostSplicedUpdateNamesItsBase(t *testing.T) {
	c := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(splicedUpdateProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(c.runner, c.bin, "-target", "x86-64-linux", "-emit", "asm", "-o", filepath.Join(t.TempDir(), "out.s"), src, c.stdlib)
	cmd.Env = append(os.Environ(), "FERN_SEM_DUMP=run")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	updates := semDumpUpdate.FindAllSubmatch(out, -1)
	reads := semDumpFieldRead.FindAllSubmatch(out, -1)
	if len(updates) != 1 || len(reads) != 2 {
		t.Fatalf("want one record update and two kept-field reads in run, got %d and %d:\n%s", len(updates), len(reads), out)
	}
	imm, _ := strconv.Atoi(string(updates[0][1]))
	for _, r := range reads {
		if want, _ := strconv.Atoi(string(r[1])); imm-1 != want {
			t.Fatalf("the update's base is v%d, want v%d, the record its kept fields are read from:\n%s", imm-1, want, out)
		}
	}
}
