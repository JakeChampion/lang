package e2ecompiler

import (
	"bytes"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

// codeRE pulls stable diagnostic codes (E001…E0NN) out of a formatted
// diagnostic string.
var codeRE = regexp.MustCompile(`E\d{3}`)

// goCheckerCodes runs the production (Go) front end over src and returns
// the sorted, de-duplicated set of diagnostic codes it reports.
func goCheckerCodes(t *testing.T, dir, src string) []string {
	t.Helper()
	p := filepath.Join(dir, "gocheck_input.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write gocheck input: %v", err)
	}
	prog, _, err := modload.Load(p)
	if err != nil {
		// A parse/load failure isn't a checker code; treat as none.
		return nil
	}
	// `fern -check` folds top-level consts ahead of the checker, and a
	// parameter default naming one is folded with them (E076 otherwise
	// refuses the name as a free one), so the oracle folds too.
	if err = constfold.Fold(prog, nil); err == nil {
		_, err = checker.Check(prog)
	}
	if err == nil {
		return nil
	}
	// The stable E0XX code lives in the diag formatting layer, not the
	// checker error's bare message — format it the way `fern -check` does.
	return uniqueSortedCodes(codeRE.FindAllString(diag.Format(p, src, err), -1))
}

// driverDiag is one line of the self-host checker driver's output: the
// diagnostic code and the message text it printed after the tab.
type driverDiag struct {
	code string
	msg  string
}

// driverDiags splits the driver's `CODE\tMESSAGE` lines. The message half
// is what the text differential reads; every codes-only gate takes .code
// and throws it away.
func driverDiags(out string) []driverDiag {
	var ds []driverDiag
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimRight(line, "\r"); line == "" {
			continue
		}
		code, msg, _ := strings.Cut(line, "\t")
		ds = append(ds, driverDiag{code, msg})
	}
	return ds
}

// driverCodes is driverDiags reduced to the sorted, de-duplicated code
// set the differential gates compare.
func driverCodes(out string) []string {
	var codes []string
	for _, d := range driverDiags(out) {
		if d.code != "" {
			codes = append(codes, d.code)
		}
	}
	return uniqueSortedCodes(codes)
}

func uniqueSortedCodes(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range in {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// TestSelfHostCheckerCodesX86_64 is the differential gate for the
// self-host type-checker port: it compiles the diag-printing checker
// driver (checker_codes_run.fern) with the Go-built self-host bundle
// compiler, runs it over a corpus, and asserts the set of diagnostic
// CODES it prints matches what the production Go checker reports for the
// same source — the full unfiltered code set: the checker port covers
// every code the Go checker emits, so the historical
// selfHostImplementedCodes filter is deleted (freeze precondition 3,
// #4451).
// buildCheckerCodesBin builds the single-module checker-codes driver
// (checker_codes_run.fern). See buildCheckerDriverBin.
func buildCheckerCodesBin(t *testing.T) (checkerBin string, runner []string, dir string) {
	return buildCheckerDriverBin(t, "drivers/checker_codes_run.fern", false)
}

// buildCheckerDriverBin builds a self-host checker-codes driver binary: it
// compiles driverFile (bundled with lexer / parser / checker / util / io, plus
// flatten when withFlatten) with the Go-built self-host compiler, producing a
// binary that reads stdin and prints the diagnostic CODE of every diagnostic
// the self-host checker emits. Returns the binary path, the (possibly empty)
// qemu/exec runner prefix, and the temp project dir (which also holds
// goCheckerCodes' scratch input). The single-module driver reads one program;
// the bundle driver (withFlatten) reads a ///MODULE bundle. Shared so the
// expensive self-host bundle compile happens once per driver.
func buildCheckerDriverBin(t *testing.T, driverFile string, withFlatten bool) (checkerBin string, runner []string, dir string) {
	t.Helper()
	gcc, run, modDriverBin := buildModloadDriverX86(t)
	runner = run

	// Compile the self-hosted checker binary (driverFile = checker_run /
	// checker_codes_run, importing std/io + ./lexer + ./parser + ./checker)
	// with the file-based asm driver. The loader resolves `import "std/io"`
	// to the vendored flat io.fern (basename fallback), so the driver source
	// is used unmodified — no ///MODULE bundle, no import rewrite.
	// Derived from the driver's own imports rather than listed: a hand-written
	// set goes stale the moment one of these modules gains an import, and this
	// bundle is a map rather than a project dir, so the copy helpers that guard
	// that elsewhere do not reach it (#6993).
	files := map[string]string{}
	for _, p := range selfHostImportClosureFiles(t, driverFile) {
		if filepath.Base(p) == filepath.Base(driverFile) {
			continue // staged below as main.fern
		}
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		files[filepath.Base(p)] = e2eharness.FlatDriverSource(string(src))
	}
	if withFlatten {
		src, err := os.ReadFile("../../../compiler/flatten.fern")
		if err != nil {
			t.Fatalf("read flatten.fern: %v", err)
		}
		files["flatten.fern"] = string(src)
	}
	ioSrc, err := os.ReadFile("../../stdlib/std/io.fern")
	if err != nil {
		t.Fatalf("read std/io.fern: %v", err)
	}
	files["io.fern"] = string(ioSrc)
	runSrc, err := os.ReadFile(filepath.Join("../../../compiler", driverFile))
	if err != nil {
		t.Fatalf("read %s: %v", driverFile, err)
	}
	files["main.fern"] = e2eharness.FlatDriverSource(string(runSrc))

	checkerAsm, progDir := compileFilesModload(t, runner, modDriverBin, files)
	if len(checkerAsm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the codes driver")
	}
	checkerBin = buildBin(t, gcc, progDir, "codes", checkerAsm)
	return checkerBin, runner, progDir
}

func TestSelfHostCheckerCodesX86_64(t *testing.T) {
	checkerBin, runner, dir := buildCheckerCodesBin(t)

	cases := []struct {
		name string
		src  string
		want []string // codes the self-host checker should print
	}{
		{"clean", "function main(): i32 { return 1 + 2; }\n", nil},
		{"view-receiver-method", "function (xs: [T]) head(): T { return xs[0]; } function main(): i32 { let a = [3]; let v: [i32] = a[:]; return v.head(); }", nil},
		{"view-method-on-owned-array", "function (xs: [T]) head(): T { return xs[0]; } function main(): i32 { let a = [3]; return a.head(); }", []string{"E043"}},
		{"owned-method-on-view", "function (xs: T[]) first(): T { return xs[0]; } function main(): i32 { let a = [3]; let v: [i32] = a[:]; return v.first(); }", []string{"E043"}},
		{"byte-view-has-no-append", "function main(): i32 { let v: [u8] = \"abc\".as_bytes(); v.append(100 as u8); return 0; }", []string{"E043"}},
		{"array-view-has-no-with", "function main(): i32 { let a = [3]; let v: [i32] = a[:]; v.with(0, 7); return 0; }", []string{"E043"}},
		{"map-iter-has-no-next", "import \"core/map\";\nfunction main(): i32 { let m: Map[string, i32] = map_new(4); let it: MapIter[string, i32] = m.iter(); while (it.has_next()) { it.next(); } return 0; }\n", []string{"E043"}},
		{"map-iter-methods", "import \"core/map\";\nfunction main(): i32 { let m: Map[string, i32] = map_new(4); let it: MapIter[string, i32] = m.iter(); let n: i32 = 0; while (it.has_next()) { n = n + it.value() + it.key().len(); it.advance(); } return n; }\n", nil},
		{"view-append-is-not-a-builtin-arity-error", "function main(): i32 { let v: [u8] = \"abc\".as_bytes(); v.append(); return 0; }", []string{"E043"}},
		{"view-custom-append-method", "function (xs: [u8]) append(): i32 { return xs.len(); } function main(): i32 { let v: [u8] = \"abc\".as_bytes(); return v.append(); }", nil},
		{"view-custom-with-method", "function (xs: [u8]) with(s: string): string { return s; } function main(): i32 { let v: [u8] = \"abc\".as_bytes(); return v.with(\"x\").len(); }", nil},
		{"view-custom-append-discard", "function (xs: [u8]) append(): i32 { return xs.len(); } function main(): i32 { let v: [u8] = \"abc\".as_bytes(); v.append(); return 0; }", nil},
		{"view-custom-with-discard", "function (xs: [u8]) with(): i32 { return xs.len(); } function main(): i32 { let v: [u8] = \"abc\".as_bytes(); v.with(); return 0; }", nil},
		// A literal local takes ONE integer type: its first width-fixing use
		// decides it, i32 when none does (#10123). The self-host held it at i32
		// from its binding and native let each use pick a width, so the same
		// local read at two widths was accepted natively and miscompiled.
		{"literal-local-widens", "function main(): i32 { let x = 5; let y: i64 = x; return 0; }\n", nil},
		{"literal-local-narrows", "function main(): i32 { let x = 5; let y: u8 = x; return 0; }\n", nil},
		{"literal-local-shift-count", "function main(): i32 { let x = 5; let z: u64 = 1 as u64 << x; return 0; }\n", nil},
		{"literal-local-wider-operand", "function main(): i32 { let x = 5; let y: i64 = x + (1 as i64); return 0; }\n", nil},
		{"literal-local-through-a-second-local", "function main(): i32 { let x = 5; let y = x + 1; let z: u64 = y; return 0; }\n", nil},
		{"literal-local-assigned-typed", "function main(): i32 { let x = 0; let n: i64 = 7i64; x = n; let y: i64 = x; return 0; }\n", nil},
		{"literal-local-parameter", "function take(v: u64): i32 { return 0; }\nfunction main(): i32 { let x = 3; let r = take(x); let y: u64 = x; return r; }\n", nil},
		{"literal-local-struct-field", "struct P { a: i64 }\nfunction main(): i32 { let x = 3; let p = P { a: x }; let y: i64 = x; return 0; }\n", nil},
		{"literal-local-array-sibling", "function main(): i32 { let x = 3; let xs: i64[] = [x, 2i64]; let y: i64 = x; return 0; }\n", nil},
		{"literal-local-cast-does-not-decide", "function main(): i32 { let x = 5; let f: f64 = x as f64; let y: i64 = x; return 0; }\n", nil},
		{"range-variable-shifts-u64", "function main(): i32 {\n    let t: i64 = 0 as i64;\n    for i in 0..64 {\n        let bits: i64 = (1 as u64 << i) as i64;\n        t = t + bits;\n    }\n    return 0;\n}\n", nil},
		{"range-variable-typed-bound", "function main(): i32 { let n: i64 = 5i64; for i in 0..n { let k: i64 = i; } return 0; }\n", nil},
		{"literal-local-two-widths", "function main(): i32 { let x = 5; let a: i32 = x; let b: i64 = x; return 0; }\n", []string{"E003"}},
		{"literal-local-two-widths-wrapped", "function main(): i32 {\n    let x = 2147483647;\n    x = x + 1;\n    let a: i32 = x;\n    let b: i64 = x;\n    if (b > 0i64) { return 1; }\n    if (a < 0) { return 2; }\n    return 3;\n}\n", []string{"E003"}},
		{"literal-local-index-is-i32", "function main(): i32 { let xs: i32[] = [1, 2, 3]; let i = 1; let v = xs[i]; let w: i64 = i; return v; }\n", []string{"E003"}},
		{"literal-local-float", "function main(): i32 { let x = 5; let f: f64 = x; return 0; }\n", []string{"E003"}},
		{"literal-local-out-of-range", "function main(): i32 { let x = 300; let b: u8 = x; return 0; }\n", []string{"E047"}},
		{"struct-match-variant-pattern", "struct Circle { r: i32 }\nfunction main(): i32 { let c = Circle { r: 5 }; match (c) { Circle(x) => { return 1; }, _ => { return 0; } } return 0; }\n", []string{"E035"}},
		{"struct-match-record-pattern", "struct Circle { r: i32 }\nfunction main(): i32 { let c = Circle { r: 5 }; match (c) { Circle { r } => { return r; } } return 0; }\n", nil},
		{"literal-cast-out-of-range", "function main(): i32 { let y = 300 as u8; let z = 3000000000 as i32; return 0; }\n", []string{"E047", "E047"}},
		{"literal-cast-in-range", "function main(): i32 { let a = 255 as u8; let b = -1 as u8; let c = (1 + 300) as u8; return 0; }\n", nil},
		// An open literal local compared with, or combined with, one that has
		// already settled takes its width, whichever order the two settle in.
		{"literal-local-compared-with-settled", "function main(): i32 { let hi = 255; let i = 250; let b: u8 = i; if (i != hi) {} let c: u8 = hi; return 0; }\n", nil},
		{"literal-local-compared-before-settling", "function main(): i32 { let hi = 255; let i = 250; if (i != hi) {} let b: u8 = i; let c: u8 = hi; return 0; }\n", nil},
		{"literal-local-arithmetic-with-settled", "function main(): i32 { let hi = 255; let i = 250; let b: u8 = i; let d = hi - i; let e: u8 = d; return 0; }\n", nil},
		// A method called on a dyn value has the result its trait declares.
		{"dyn-method-result-typed", "trait Speak { function say(self: Self): i32; }\nimpl Speak for i32 { function say(self: Self): i32 { return self + 100; } }\nfunction f(s: dyn Speak): i32 { let x: string = s.say(); return x.len(); }\nfunction main(): i32 { return f(7); }\n", []string{"E003"}},
		// A `dyn` dispatches to its named traits' methods, a default included,
		// and to no supertrait's; a wrong argument count is E004 (#10524).
		{"dyn-default-method-typed", "trait Greet { function hi(self: Self): i32; function bye(self: Self): i32 { return 3; } }\nstruct Dog { }\nimpl Greet for Dog { function hi(self: Self): i32 { return 7; } }\nfunction main(): i32 { let d: dyn Greet = Dog { }; let s: string = d.bye(); return s.len(); }\n", []string{"E003"}},
		{"dyn-only-default-method", "trait Count { function n(self: Self): i32 { return 42; } }\nstruct T { }\nimpl Count for T { }\nfunction pick(t: dyn Count): i32 { return t.n(); }\nfunction main(): i32 { return pick(T { }); }\n", nil},
		{"dyn-method-arity", "trait Speak { function say(self: Self): i32; }\nimpl Speak for i32 { function say(self: Self): i32 { return self + 100; } }\nfunction f(s: dyn Speak): i32 { return s.say(\"a\", \"b\"); }\nfunction main(): i32 { return f(1); }\n", []string{"E004"}},
		{"dyn-undefined-method", "trait Speak { function say(self: Self): i32; }\nimpl Speak for i32 { function say(self: Self): i32 { return self + 100; } }\nfunction f(s: dyn Speak): i32 { return s.nope(); }\nfunction main(): i32 { return f(1); }\n", []string{"E021"}},
		{"dyn-supertrait-method-not-named", "trait Base { function tag(self: Self): i32; }\ntrait Derived : Base { function base(self: Self): i32; }\nimpl Base for i32 { function tag(self: Self): i32 { return 9; } }\nimpl Derived for i32 { function base(self: Self): i32 { return self; } }\nfunction f(d: dyn Derived): i32 { return d.tag(); }\nfunction main(): i32 { return f(1); }\n", []string{"E021"}},
		{"dyn-supertrait-method-named", "trait Base { function tag(self: Self): i32; }\ntrait Derived : Base { function base(self: Self): i32 { return 4; } }\nimpl Base for i32 { function tag(self: Self): i32 { return 9; } }\nimpl Derived for i32 { }\nfunction f(d: dyn Derived + Base): i32 { return d.tag() + d.base(); }\nfunction main(): i32 { return f(1); }\n", nil},
		{"dyn-method-result-accepted", "trait Speak { function say(self: Self): i32; }\nimpl Speak for i32 { function say(self: Self): i32 { return self + 100; } }\nfunction f(s: dyn Speak): i32 { let x: i32 = s.say(); return x; }\nfunction main(): i32 { return f(7); }\n", nil},
		// A `{ …; tail }` block is typed by its tail with the leading
		// statements bound (#10438). The self-host typed it as nothing, so a
		// mismatch went unreported and the lowering stamped no type on it.
		{"value-block-tail-mismatch", "function main(): i32 { let q: string = { let k = 1 + 2; k }; return 0; }\n", []string{"E003"}},
		{"value-block-struct-tail-mismatch", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nfunction measure(d: dyn Shape): i32 { return d.area(); }\nfunction main(): i32 { let s = Square { side: 3 }; let n: i32 = { let q = s; q }; return measure({ let q = s; q }) + n; }\n", []string{"E003"}},
		{"value-block-wide-tail-mismatch", "function main(): i32 { let k: i64 = 4i64; let n: i32 = { let q = k; q }; return n; }\n", []string{"E003"}},
		{"value-block-tail-typed", "struct P { x: i32 }\nfunction main(): i32 { let p: P = { let q = P { x: 1 }; q }; let y: i64 = { let z = 0; 5 }; return p.x; }\n", nil},
		// A literal local a block tail returns takes the destination's width,
		// as it does outside a block.
		{"value-block-literal-local-tail-widens", "function main(): i32 { let y: i64 = { let z = 5; z }; return 0; }\n", nil},
		{"value-block-literal-local-tail-sum-widens", "function main(): i32 { let y: i64 = { let z = 5; z + 1 }; return 0; }\n", nil},
		{"value-block-literal-local-tail-i32", "function main(): i32 { let y: i32 = { let z = 5; z }; return y; }\n", nil},
		{"value-block-literal-local-tail-arg-widens", "function f(n: u64): u64 { return n; }\nfunction main(): i32 { let r: u64 = f({ let z = 5; z }); return 0; }\n", nil},
		{"value-block-typed-local-tail-mismatch", "function main(): i32 { let k: i32 = 7; let y: i64 = { let q = k; q }; return 0; }\n", []string{"E003"}},
		{"literal-local-compared-takes-width", "function main(): i32 { let hi = 255; let i = 250; let b: u8 = i; if (i != hi) {} let c: i32 = hi; return 0; }\n", []string{"E003"}},
		{"literal-local-compared-out-of-range", "function main(): i32 { let hi = 300; let i = 250; let b: u8 = i; if (i != hi) {} return 0; }\n", []string{"E047"}},
		{"range-variable-two-widths", "function main(): i32 { for i in 0..4 { let a: u64 = i; let b: i32 = i; } return 0; }\n", []string{"E003"}},
		// A generic call's type parameter that only untyped literals bind is
		// settled by the position reading it (#10176): a destination names its
		// width, through a field read of the result too, and a comparison or
		// arithmetic with no typed side takes the default — i64 when a literal
		// has no i32 reading. The self-host refused every destination but the
		// widest reading, and native refused the comparison as E041.
		{"generic-literal-t-unannotated-var", "struct Box[T] { v: T }\nfunction box[T](v: T): Box[T] { return Box { v: v }; }\nfunction both[T](a: T, b: T): (T, T) { return (a, b); }\nfunction pick[T](a: T, b: T): Option[T] { return Some(b); }\nfunction id[T](a: T): T { return a; }\nfunction take(v: u64): i32 { return 0; }\nfunction main(): i32 { let q = both(1, 4611686018427387904); if (q.1 == 4611686018427387904) { return 7; } return 0; }\n", nil},
		{"generic-literal-t-scrutinee", "struct Box[T] { v: T }\nfunction box[T](v: T): Box[T] { return Box { v: v }; }\nfunction both[T](a: T, b: T): (T, T) { return (a, b); }\nfunction pick[T](a: T, b: T): Option[T] { return Some(b); }\nfunction id[T](a: T): T { return a; }\nfunction take(v: u64): i32 { return 0; }\nfunction main(): i32 { match (pick(1, 4611686018427387904)) { Some(v) => { if (v == 4611686018427387904) { return 7; } }, None => { } } return 0; }\n", nil},
		{"generic-literal-t-plain-comparison", "struct Box[T] { v: T }\nfunction box[T](v: T): Box[T] { return Box { v: v }; }\nfunction both[T](a: T, b: T): (T, T) { return (a, b); }\nfunction pick[T](a: T, b: T): Option[T] { return Some(b); }\nfunction id[T](a: T): T { return a; }\nfunction take(v: u64): i32 { return 0; }\nfunction main(): i32 { if (both(1, 4611686018427387904).1 == 4611686018427387904) { return 7; } return 0; }\n", nil},
		{"generic-literal-t-u64-destination", "struct Box[T] { v: T }\nfunction box[T](v: T): Box[T] { return Box { v: v }; }\nfunction both[T](a: T, b: T): (T, T) { return (a, b); }\nfunction pick[T](a: T, b: T): Option[T] { return Some(b); }\nfunction id[T](a: T): T { return a; }\nfunction take(v: u64): i32 { return 0; }\nfunction main(): i32 { let x: (u64, u64) = both(1, 4611686018427387904); return 0; }\n", nil},
		{"generic-literal-t-field-at-destination", "struct Box[T] { v: T }\nfunction box[T](v: T): Box[T] { return Box { v: v }; }\nfunction both[T](a: T, b: T): (T, T) { return (a, b); }\nfunction pick[T](a: T, b: T): Option[T] { return Some(b); }\nfunction id[T](a: T): T { return a; }\nfunction take(v: u64): i32 { return 0; }\nfunction main(): i32 { let z: u64 = both(1, 2).1; return 0; }\n", nil},
		{"generic-literal-t-struct-destination", "struct Box[T] { v: T }\nfunction box[T](v: T): Box[T] { return Box { v: v }; }\nfunction both[T](a: T, b: T): (T, T) { return (a, b); }\nfunction pick[T](a: T, b: T): Option[T] { return Some(b); }\nfunction id[T](a: T): T { return a; }\nfunction take(v: u64): i32 { return 0; }\nfunction main(): i32 { let b: Box[u64] = box(1); return 0; }\n", nil},
		{"generic-literal-t-struct-field-at-destination", "struct Box[T] { v: T }\nfunction box[T](v: T): Box[T] { return Box { v: v }; }\nfunction both[T](a: T, b: T): (T, T) { return (a, b); }\nfunction pick[T](a: T, b: T): Option[T] { return Some(b); }\nfunction id[T](a: T): T { return a; }\nfunction take(v: u64): i32 { return 0; }\nfunction main(): i32 { let v: u64 = box(1).v; return 0; }\n", nil},
		{"generic-literal-t-assignment", "struct Box[T] { v: T }\nfunction box[T](v: T): Box[T] { return Box { v: v }; }\nfunction both[T](a: T, b: T): (T, T) { return (a, b); }\nfunction pick[T](a: T, b: T): Option[T] { return Some(b); }\nfunction id[T](a: T): T { return a; }\nfunction take(v: u64): i32 { return 0; }\nfunction main(): i32 { let x: u64 = 0 as u64; x = id(1); return 0; }\n", nil},
		{"generic-literal-t-parameter", "struct Box[T] { v: T }\nfunction box[T](v: T): Box[T] { return Box { v: v }; }\nfunction both[T](a: T, b: T): (T, T) { return (a, b); }\nfunction pick[T](a: T, b: T): Option[T] { return Some(b); }\nfunction id[T](a: T): T { return a; }\nfunction take(v: u64): i32 { return 0; }\nfunction main(): i32 { return take(id(1) + 1); return 0; }\n", nil},
		{"generic-literal-t-arithmetic-at-destination", "struct Box[T] { v: T }\nfunction box[T](v: T): Box[T] { return Box { v: v }; }\nfunction both[T](a: T, b: T): (T, T) { return (a, b); }\nfunction pick[T](a: T, b: T): Option[T] { return Some(b); }\nfunction id[T](a: T): T { return a; }\nfunction take(v: u64): i32 { return 0; }\nfunction main(): i32 { let x: i64 = id(1) + 1; return 0; }\n", nil},
		{"generic-literal-t-typed-operand", "struct Box[T] { v: T }\nfunction box[T](v: T): Box[T] { return Box { v: v }; }\nfunction both[T](a: T, b: T): (T, T) { return (a, b); }\nfunction pick[T](a: T, b: T): Option[T] { return Some(b); }\nfunction id[T](a: T): T { return a; }\nfunction take(v: u64): i32 { return 0; }\nfunction main(): i32 { let y: u64 = 3 as u64; if (id(1) == y) { return 7; } return 0; }\n", nil},
		{"generic-literal-t-two-open-operands", "struct Box[T] { v: T }\nfunction box[T](v: T): Box[T] { return Box { v: v }; }\nfunction both[T](a: T, b: T): (T, T) { return (a, b); }\nfunction pick[T](a: T, b: T): Option[T] { return Some(b); }\nfunction id[T](a: T): T { return a; }\nfunction take(v: u64): i32 { return 0; }\nfunction main(): i32 { if (id(1) == id(4611686018427387904)) { return 7; } return 0; }\n", nil},
		{"generic-literal-t-beside-literal-local", "struct Box[T] { v: T }\nfunction box[T](v: T): Box[T] { return Box { v: v }; }\nfunction both[T](a: T, b: T): (T, T) { return (a, b); }\nfunction pick[T](a: T, b: T): Option[T] { return Some(b); }\nfunction id[T](a: T): T { return a; }\nfunction take(v: u64): i32 { return 0; }\nfunction main(): i32 { let x = 1; if (x == id(2)) { return 3; } let y: i64 = x; return 0; }\n", nil},
		{"generic-literal-t-wide-at-i32", "struct Box[T] { v: T }\nfunction box[T](v: T): Box[T] { return Box { v: v }; }\nfunction both[T](a: T, b: T): (T, T) { return (a, b); }\nfunction pick[T](a: T, b: T): Option[T] { return Some(b); }\nfunction id[T](a: T): T { return a; }\nfunction take(v: u64): i32 { return 0; }\nfunction main(): i32 { let x: i32 = id(4294967298); return 0; }\n", []string{"E047"}},
		// An array parameter is the destination of its argument's literal
		// elements, as an annotated `let` is (#10270): native typed them on
		// their own and refused a clean call as E038.
		{"array-argument-settles-its-literals", "struct Same[T] { a: T, b: T }\nfunction take(xs: Same[i64][]): i32 { return xs.len(); }\nfunction main(): i32 { return take([Same { a: 1, b: 2 }]); }\n", nil},
		{"array-argument-field-settles-at-the-parameter", "struct Same[T] { a: T, b: T }\nfunction take(xs: Same[i64][]): i32 { return xs.len(); }\nfunction main(): i32 { let y: i64 = 5; return take([Same { a: 1, b: y }]); }\n", nil},
		{"array-argument-literal-field-mismatch", "struct Same[T] { a: T, b: T }\nfunction take(xs: Same[i64][]): i32 { return xs.len(); }\nfunction main(): i32 { return take([Same { a: 1, b: \"x\" }]); }\n", []string{"E043"}},
		// A literal that writes its instantiation is what it writes: the
		// literal widening does not reach it, and a wrong argument count is
		// E040 (#10453). The self-host dropped the written arguments.
		{"struct-literal-written-instantiation-not-widened", "struct Same[T] { a: T, b: T }\nfunction main(): i32 { let q = Same[i32] { a: 1, b: 4611686018427387904 }; return 0; }\n", []string{"E047"}},
		{"struct-literal-written-arity", "struct Same[T] { a: T, b: T }\nfunction main(): i32 { let q = Same[i64, i32] { a: 1, b: 2 }; return 0; }\n", []string{"E040"}},
		// A free call that writes its type arguments is what it writes too: the
		// arguments are held to them, and neither the shared-literal widening
		// nor the destination's width reaches them (#11329).
		{"call-written-type-arg-contradicted", "struct Item { name: string }\nfunction ident[T](v: T): T { return v; }\nfunction main(): i32 { let x: Item = ident[Item](1); return 0; }\n", []string{"E038"}},
		{"call-written-instantiation-not-widened", "function big2[T](a: T, b: T): T { return a; }\nfunction use64(x: i64): i64 { return x; }\nfunction main(): i32 { use64(big2[i32](4611686018427387904, 1)); return 0; }\n", []string{"E038", "E047"}},
		{"call-written-i32-read-at-i64", "function big2[T](a: T, b: T): T { return a; }\nfunction use64(x: i64): i64 { return x; }\nfunction main(): i32 { use64(big2[i32](3, 1)); return 0; }\n", []string{"E038"}},
		{"struct-literal-written-i64-read-narrow", "struct Box[T] { v: T }\nfunction main(): i32 { let q = Box[i64] { v: 4 }; let r: i32 = q.v; return 0; }\n", []string{"E003"}},
		// A written argument is validated as an annotation is, a type
		// variable in it is the enclosing function's, and a struct-update
		// base's instantiation outranks it, all as natively.
		{"struct-literal-written-unknown-type", "struct Box[T] { v: i32 }\nfunction main(): i32 { let b = Box[Zzz] { v: 1 }; return 0; }\n", []string{"E064"}},
		{"struct-literal-written-map-key", "import \"core/map\";\nstruct Box[T] { v: T }\nfunction main(): i32 { let b = Box[Map[f64, i32]] { v: map_new(4) }; return 0; }\n", []string{"E045"}},
		{"struct-literal-written-type-variable", "struct T { z: i32 }\nstruct Box[T] { v: T }\nstruct Many[T] { xs: T[] }\nfunction wrap[T](x: T): Box[T] { return Box[T] { v: x }; }\nfunction many[T](x: T): Many[T] { return Many[T] { xs: [x] }; }\nfunction main(): i32 { let b = wrap(4294967296); let r: i64 = b.v; let m = many(4294967296); let k: i64 = m.xs[0]; return 0; }\n", nil},
		{"struct-literal-update-base-outranks-written", "struct Pair[T] { a: T, b: T }\nfunction f(p: Pair[string]): Pair[string] { return Pair[i32] { ...p, a: \"x\" }; }\nfunction main(): i32 { return f(Pair { a: \"a\", b: \"b\" }).a.len(); }\n", nil},
		{"struct-literal-wide-array-field", "struct Stack[T] { items: T[] }\nfunction main(): i32 { let q = Stack { items: [1, 4611686018427387904] }; let r: i64 = q.items[1]; return 0; }\n", nil},
		// A nested generic literal infers its own arguments (#10895).
		{"nested-generic-struct-literal", "struct Box[T] { v: T }\nstruct Outer[T] { b: Box[T] }\nfunction main(): i32 { let o = Outer { b: Box { v: 4 } }; return o.b.v; }\n", nil},
		{"nested-generic-struct-literal-wide", "struct Box[T] { v: T }\nstruct Two[T] { b: Box[T], c: T }\nfunction main(): i32 { let o = Two { b: Box { v: 4611686018427387904 }, c: 1 }; let r: i64 = o.c; return 0; }\n", nil},
		// A generic call in an unbound field infers from the destination.
		{"generic-call-in-unbound-field", "struct Holder[T] { xs: T[], z: T }\nfunction emptyArr[A](): A[] { return []; }\nfunction f[T](t: T): Holder[T] { let h = Holder { xs: emptyArr(), z: t }; return h; }\nfunction main(): i32 { let h = f(7); return h.z; }\n", nil},
		// A match on a built-in Option or Result is held to E014 and E030
		// like any enum's (#10985).
		{"option-match-result-arms", "function f(): Option[i32] { return Some(3); }\nfunction main(): i32 { match (f()) { Ok(_) => { return 1; }, Err(_) => { return 9; } } return 0; }\n", []string{"E014", "E030"}},
		{"option-match-misspelled-arm", "function f(): Option[i32] { return Some(3); }\nfunction main(): i32 { match (f()) { Some(v) => { return v; }, Nope => { return 9; } } return 0; }\n", []string{"E014", "E030"}},
		{"result-match-missing-err", "function f(): Result[i32, string] { return Ok(3); }\nfunction main(): i32 { match (f()) { Ok(v) => { return v; } } return 0; }\n", []string{"E030"}},
		{"option-result-matches-exhaustive", "function f(): Option[i32] { return Some(3); }\nfunction g(): Result[i32, string] { return Err(\"e\"); }\nfunction main(): i32 { let n: i32 = 0; match (f()) { Some(v) when v > 9 => { n = 1; }, Some(v) => { n = v; }, None => { n = 2; } } match (g()) { Ok(v) => { n = n + v; }, _ => { n = n + 1; } } if let Some(w) = f() { n = n + w; } return n; }\n", nil},
		// Two instantiations of one generic struct in an array literal are
		// E034 unless an integer literal gives way to the other's (#10912).
		{"array-two-instantiations", "struct Same[T] { a: T, b: T }\nfunction main(): i32 { let xs = [Same { a: 1, b: 2 }, Same { a: \"x\", b: \"y\" }]; return 0; }\n", []string{"E034"}},
		{"array-two-instantiations-third", "struct Box[T] { v: T }\nfunction main(): i32 { let xs = [Box { v: 1 }, Box { v: \"a\" }, Box { v: 2 }]; return 0; }\n", []string{"E034"}},
		{"array-two-instantiations-nested", "struct Box[T] { v: T }\nfunction main(): i32 { let xs = [Box { v: Box { v: 1 } }, Box { v: Box { v: \"x\" } }]; return 0; }\n", []string{"E034"}},
		{"array-two-instantiations-locals", "struct Box[T] { v: T }\nfunction main(): i32 { let a: Box[i32] = Box { v: 1 }; let b: Box[string] = Box { v: \"s\" }; let xs = [a, b]; return 0; }\n", []string{"E034"}},
		{"array-instantiation-literal-gives-way-to-float", "struct Box[T] { v: T }\nfunction main(): i32 { let xs = [Box { v: 1.5 }, Box { v: 2 }]; let ys = [Box { v: 2 }, Box { v: 1.5 }]; return 0; }\n", nil},
		{"nested-generic-struct-literal-clash", "struct Box[T] { v: T }\nstruct Two[T] { b: Box[T], c: T }\nfunction main(): i32 { let o = Two { b: Box { v: 1 }, c: \"x\" }; return 0; }\n", []string{"E043"}},
		{"struct-literal-wide-in-array", "struct Same[T] { a: T, b: T }\nfunction main(): i32 { let xs = [Same { a: 1, b: 4611686018427387904 }]; let r: i64 = xs[0].b; return 0; }\n", nil},
		// A typed field binds T ahead of an untyped literal written before
		// it, so this is a Same[i64] (#10453); the literal used to bind T at
		// i32 and the typed field then clashed.
		// A generic struct local whose type argument only untyped literals bind
		// takes one width: the first use that fixes it decides, and a second,
		// different width is E003 (#10453).
		{"struct-local-field-read-fixes-width", "struct Same[T] { a: T, b: T }\nfunction take1(x: Same[i64]): i32 { return 1; }\nfunction take(xs: Same[i64][]): i32 { return 1; }\nfunction main(): i32 { let q = Same { a: 1, b: 2 }; let r: i64 = q.a; return 0; }\n", nil},
		{"struct-local-passed-whole-fixes-width", "struct Same[T] { a: T, b: T }\nfunction take1(x: Same[i64]): i32 { return 1; }\nfunction take(xs: Same[i64][]): i32 { return 1; }\nfunction main(): i32 { let q = Same { a: 1, b: 2 }; return take1(q); }\n", nil},
		{"struct-local-in-array-argument-fixes-width", "struct Same[T] { a: T, b: T }\nfunction take1(x: Same[i64]): i32 { return 1; }\nfunction take(xs: Same[i64][]): i32 { return 1; }\nfunction main(): i32 { let q = Same { a: 1, b: 2 }; return take([q]); }\n", nil},
		{"struct-local-second-width", "struct Same[T] { a: T, b: T }\nfunction take1(x: Same[i64]): i32 { return 1; }\nfunction take(xs: Same[i64][]): i32 { return 1; }\nfunction main(): i32 { let q = Same { a: 1, b: 2 }; let r: i64 = q.a; let z: i32 = q.b; return 0; }\n", []string{"E003"}},
		{"struct-local-passed-then-read-narrow", "struct Same[T] { a: T, b: T }\nfunction take1(x: Same[i64]): i32 { return 1; }\nfunction take(xs: Same[i64][]): i32 { return 1; }\nfunction main(): i32 { let q = Same { a: 1, b: 2 }; let t = take1(q); let z: i32 = q.a; return 0; }\n", []string{"E003"}},
		// A literal-bound struct local used at a second width is the
		// destination's mismatch, E038, as a scalar literal local's is.
		{"generic-literal-struct-local-second-width", "struct Same[T] { a: T, b: T }\nfunction take(x: Same[i64]): i64 { return x.a; }\nfunction take32(x: Same[i32]): i32 { return x.a; }\nfunction main(): i32 { let s = Same { a: 1, b: 2 }; let a = take(s); return take32(s); }\n", []string{"E038"}},
		// The typed-first binding stays inside the literal, an array's
		// typed element widens a literal sibling in either order, and a
		// struct local's copy and capture share its width (#10453).
		{"generic-literal-update-over-literal-base", "struct Same[T] { a: T, b: T }\nfunction main(): i32 { let q = Same { a: 1, b: 2 }; let y: i64 = 8589934592; let w = Same { ...q, b: y }; return 0; }\n", []string{"E043"}},
		{"generic-literal-array-typed-sibling", "struct Same[T] { a: T, b: T }\nfunction main(): i32 { let y: i64 = 8589934592; let xs = [Same { a: 1, b: 2 }, Same { a: 3, b: y }]; let r: i64 = xs[0].a; return 0; }\n", nil},
		{"generic-literal-struct-local-copy", "struct Same[T] { a: T, b: T }\nfunction main(): i32 { let q = Same { a: 1, b: 2 }; let r = q; let z: i64 = r.a; let w: i64 = q.b; return 0; }\n", nil},
		{"generic-literal-struct-local-capture", "struct Same[T] { a: T, b: T }\nfunction main(): i32 { let q = Same { a: 1, b: 2 }; let f = (): i64 => q.a; return 0; }\n", nil},
		{"generic-literal-typed-field-binds-ahead", "struct Same[T] { a: T, b: T }\nfunction take(xs: Same[i64][]): i32 { return xs.len(); }\nfunction main(): i32 { let y: i64 = 5; let q = Same { a: 1, b: y }; let r: i64 = q.a; return take([q]); }\n", nil},
		// A literal whose fields clash has no instantiation, so its local
		// reads as untyped and no use reports the clash again (#10453). Native
		// took the first field's instantiation and added E038 / E003 per use.
		{"generic-literal-fields-clash-then-passed", "struct Same[T] { a: T, b: T }\nfunction take1(x: Same[i64]): i32 { return 1; }\nfunction main(): i32 { let q = Same { a: 1, b: \"x\" }; return take1(q); }\n", []string{"E043"}},
		{"generic-literal-fields-clash-then-assigned", "struct Same[T] { a: T, b: T }\nfunction main(): i32 { let q = Same { a: 1, b: \"x\" }; let z: string = q; return 0; }\n", []string{"E043"}},
		{"generic-literal-fields-clash-in-an-array", "struct Same[T] { a: T, b: T }\nfunction take1(x: Same[i64]): i32 { return 1; }\nfunction main(): i32 { let xs = [Same { a: 1, b: \"x\" }]; return take1(xs[0]); }\n", []string{"E043"}},
		{"generic-literal-t-out-of-range", "struct Box[T] { v: T }\nfunction box[T](v: T): Box[T] { return Box { v: v }; }\nfunction both[T](a: T, b: T): (T, T) { return (a, b); }\nfunction pick[T](a: T, b: T): Option[T] { return Some(b); }\nfunction id[T](a: T): T { return a; }\nfunction take(v: u64): i32 { return 0; }\nfunction main(): i32 { let x: u8 = id(300); return 0; }\n", []string{"E047"}},
		// The `.with` receiver root walk (#9699). The self-host matched a bare
		// identifier only, so a field receiver — the structure-of-arrays shape
		// `fbip` exists for — drew E053 there and nothing natively, and
		// examples/fip/event_loop_fbip.fern did not compile self-host at all.
		// A chain rooted at an own array is clean in both: it writes one array
		// in place (#9702). Rooted at a borrowed one, it is E053 in both.
		{"e053-with-on-an-own-structs-field", "struct S { xs: i32[] }\nfip function f(own s: S): i32[] { return s.xs.with(0, 1); }\nfunction main(): i32 { return f(S { xs: [1, 2] })[0]; }\n", nil},
		{"e053-with-on-a-borrowed-structs-field", "struct S { xs: i32[] }\nfip function f(s: S): i32[] { return s.xs.with(0, 1); }\nfunction main(): i32 { return f(S { xs: [1, 2] })[0]; }\n", []string{"E053"}},
		{"e053-with-chain-on-own", "fip function f(own b: i32[]): i32[] { return b.with(0, 1).with(1, 2); }\nfunction main(): i32 { return f([1, 2])[0]; }\n", nil},
		{"e053-with-chain-reads-receiver", "fip function f(own b: i32[]): i32[] { return b.with(0, 1).with(1, b[0]); }\nfunction main(): i32 { return f([1, 2])[0]; }\n", []string{"E053"}},
		{"e053-with-chain-on-borrowed", "fip function f(b: i32[]): i32[] { return b.with(0, 1).with(1, 2); }\nfunction main(): i32 { return f([1, 2])[0]; }\n", []string{"E053"}},
		// A builtin that allocates nothing is a legal fip / fbip callee
		// (#9607); an allocating one stays E053. A free function named like a
		// builtin redeclares it (E006), and takes no admission from the name.
		{"e053-fip-calls-nonalloc-builtins", "fip function f(s: string, x: u32, y: u64): i32 { return __memchr(s, 44, 0) + __count_byte(s, 44) + __crc32_cksum(0, s) + __popcount32(x) + __clz64(y) + __ptr_width() + (monotonic_ns() as i32) + (__heap_alloc_count() as i32); }\nfunction main(): i32 { return f(\"a,b\", 3 as u32, 3 as u64); }\n", nil},
		{"e053-fbip-calls-nonalloc-builtin", "fbip function f(x: u32): i32 { return __ctz32(x); }\nfunction main(): i32 { return f(8 as u32); }\n", nil},
		{"e053-fip-calls-allocating-builtin", "fip function f(xs: f64[]): f64[] { return __scale_f64(xs, 2.0); }\nfunction main(): i32 { return f([1.0]).len(); }\n", []string{"E053"}},
		{"e006-builtin-redeclared", "function print(s: string): void { }\nfunction main(): i32 { print(\"x\"); return 0; }\n", []string{"E006"}},
		// An intrinsic's name is registered too, so a free function taking it
		// is E006; a METHOD of that name shadows nothing, natively or here.
		{"e006-intrinsic-redeclared", "function __memchr(s: string, b: i32, f: i32): i32 { return 0; }\nfunction main(): i32 { return __memchr(\"a\", 1, 0); }\n", []string{"E006"}},
		{"e053-method-named-like-builtin", "struct R { v: i32 }\nfunction (r: R) __memchr(): i32 { let a: i32[] = [1]; return a.len(); }\nfip function f(s: string): i32 { return __memchr(s, 44, 0); }\nfunction main(): i32 { return f(\"a,b\"); }\n", nil},
		{"e006-builtin-redeclared-fip-callee", "function monotonic_ns(): i64 { let a: i32[] = [1]; return a.len() as i64; }\nfip function f(): i32 { return monotonic_ns() as i32; }\nfunction main(): i32 { return f(); }\n", []string{"E006", "E053"}},
		// A method call's written type arguments bind the method's parameters
		// (#9976): a short list the method's own, the last ones, a full list all
		// of them, receiver's first. A contradicting argument is E038 and a
		// surplus E040; every well-formed spelling is clean.
		{"method-type-arg-contradicts-argument", "enum Box[T, E] { Full(T), Blank(E) }\nfunction (b: Box[T, E]) pair[U](other: Box[U, E]): Box[U, E] { return other; }\nfunction take(b: Box[i32, string]): i32 {\n    match (b) {\n        Full(n) => { return n; },\n        Blank(s) => { return s.len(); }\n    }\n}\nfunction main(): i32 {\n    let b: Box[i32, string] = Full(5);\n    return take(b.pair[string](Full(9)));\n}\n", []string{"E038"}},
		{"method-type-arg-full-list-contradicts", "enum Box[T, E] { Full(T), Blank(E) }\nfunction (b: Box[T, E]) pair[U](other: Box[U, E]): Box[U, E] { return other; }\nfunction take(b: Box[i32, string]): i32 {\n    match (b) {\n        Full(n) => { return n; },\n        Blank(s) => { return s.len(); }\n    }\n}\nfunction main(): i32 {\n    let b: Box[i32, string] = Full(5);\n    return take(b.pair[i32, string, string](Full(9)));\n}\n", []string{"E038"}},
		{"method-type-arg-too-many", "enum Box[T, E] { Full(T), Blank(E) }\nfunction (b: Box[T, E]) pair[U](other: Box[U, E]): Box[U, E] { return other; }\nfunction take(b: Box[i32, string]): i32 {\n    match (b) {\n        Full(n) => { return n; },\n        Blank(s) => { return s.len(); }\n    }\n}\nfunction main(): i32 {\n    let b: Box[i32, string] = Full(5);\n    return take(b.pair[i32, string, i32, i32](Full(9)));\n}\n", []string{"E040"}},
		{"method-type-arg-inferred", "enum Box[T, E] { Full(T), Blank(E) }\nfunction (b: Box[T, E]) pair[U](other: Box[U, E]): Box[U, E] { return other; }\nfunction take(b: Box[i32, string]): i32 {\n    match (b) {\n        Full(n) => { return n; },\n        Blank(s) => { return s.len(); }\n    }\n}\nfunction main(): i32 {\n    let b: Box[i32, string] = Full(5);\n    return take(b.pair(Full(9)));\n}\n", nil},
		{"method-type-arg-own-written", "enum Box[T, E] { Full(T), Blank(E) }\nfunction (b: Box[T, E]) pair[U](other: Box[U, E]): Box[U, E] { return other; }\nfunction take(b: Box[i32, string]): i32 {\n    match (b) {\n        Full(n) => { return n; },\n        Blank(s) => { return s.len(); }\n    }\n}\nfunction main(): i32 {\n    let b: Box[i32, string] = Full(5);\n    return take(b.pair[i32](Full(9)));\n}\n", nil},
		{"method-type-arg-full-written", "enum Box[T, E] { Full(T), Blank(E) }\nfunction (b: Box[T, E]) pair[U](other: Box[U, E]): Box[U, E] { return other; }\nfunction take(b: Box[i32, string]): i32 {\n    match (b) {\n        Full(n) => { return n; },\n        Blank(s) => { return s.len(); }\n    }\n}\nfunction main(): i32 {\n    let b: Box[i32, string] = Full(5);\n    return take(b.pair[i32, string, i32](Full(9)));\n}\n", nil},
		// A generic enum's arguments take part in assignability: a Box[string,
		// string] is not a Box[i32, string] (#10248).
		// A union's arguments take part in assignability (#10197).
		{"option-argument-mismatch-return", "function g(): Option[f64] { let v: f32 = 1.0; return Some(v); }\nfunction main(): i32 { return 0; }\n", []string{"E002"}},
		{"option-argument-mismatch-var", "function main(): i32 { let v: f32 = 1.0; let o: Option[f64] = Some(v); return 0; }\n", []string{"E003"}},
		{"generic-enum-argument-mismatch", "enum Box[T, E] { Full(T), Blank(E) }\nfunction take(b: Box[i32, string]): i32 {\n    match (b) {\n        Full(n) => { return n; },\n        Blank(s) => { return s.len(); }\n    }\n}\nfunction main(): i32 {\n    let y: Box[string, string] = Blank(\"a\");\n    return take(y);\n}\n", []string{"E038"}},
		// Shadowed-callee scoping (#9532). A binding shadows an own-func's name
		// inside ITS OWN scope: a block-local from its declaration to the end of
		// its block, a match binder for its arm. The self-host answered from a
		// whole-function name set, which silenced the first and invented the
		// second — neither direction had a row here, which is how both reached
		// a reviewer instead of a test.
		{"e051-local-shadows-after-the-call", "function keep(own ys: i32[]): i32 { return ys[0]; }\nfunction f(xs: i32[]): i32 {\n    let a: i32 = keep(xs);\n    let keep: (i32[]) => i32 = (v: i32[]) => v.len();\n    return a + keep(xs);\n}\nfunction main(): i32 { return f([1, 2]); }\n", []string{"E051"}},
		{"e051-local-shadows-in-an-inner-block", "function keep(own ys: i32[]): i32 { return ys[0]; }\nfunction f(xs: i32[], c: boolean): i32 {\n    let n: i32 = 0;\n    if (c) { let keep: (i32[]) => i32 = (v: i32[]) => v.len(); n = keep(xs); }\n    return n + keep(xs);\n}\nfunction main(): i32 { return f([1, 2], true); }\n", []string{"E051"}},
		{"arm-binder-on-a-borrowed-scrutinee-clean", "enum Box { B((i32[]) => i32) }\nfunction keep(own ys: i32[]): i32 { return ys[0]; }\nfunction f(b: Box, xs: i32[]): i32 {\n    let n: i32 = 0;\n    match (b) { B(keep) => { n = keep(xs); } }\n    return n;\n}\nfunction main(): i32 { return f(B((v: i32[]) => v.len()), [1, 2]); }\n", nil},
		// Whether a call's result may be transferred into an `own` parameter
		// comes from what the callee RETURNS, not from its parameter list
		// (#9538). Both compilers infer it, so a factory that takes a
		// reference and builds something new is clean in both, and one that
		// can hand its borrowed parameter back — directly, through a local, or
		// through a chain — still draws E051 in both.
		// A local handed to an `own` parameter inside a returned tuple dies at
		// the call, loop or not: the return exits (#10679). A second mention in
		// the returned value withholds it.
		{"e051-local-moved-inside-a-returned-tuple", "function keep(own ys: i32[]): i32 { return ys[0]; }\nfunction f(n: i32): (i32, boolean) {\n    let i: i32 = 0;\n    while (i < n) {\n        let xs: i32[] = [i];\n        if (i > 2) { return (keep(xs), true); }\n        i = i + 1;\n    }\n    return (0, false);\n}\nfunction main(): i32 { let r: (i32, boolean) = f(5); return r.0; }\n", nil},
		{"e051-local-named-twice-in-a-returned-tuple", "function keep(own ys: i32[]): i32 { return ys[0]; }\nfunction f(n: i32): (i32, i32) {\n    let i: i32 = 0;\n    while (i < n) {\n        let xs: i32[] = [i];\n        if (i > 2) { return (keep(xs), xs.len()); }\n        i = i + 1;\n    }\n    return (0, 0);\n}\nfunction main(): i32 { let r: (i32, i32) = f(5); return r.0; }\n", []string{"E051"}},
		{"e051-fresh-result-from-a-borrowing-factory", "function keep(own ys: i32[]): i32 { return ys[0]; }\nfunction build(tag: string): i32[] { return [tag.len()]; }\nfunction main(): i32 { return keep(build(\"xy\")); }\n", nil},
		{"e051-fresh-result-through-a-call-chain", "function keep(own ys: i32[]): i32 { return ys[0]; }\nfunction sized(xs: i32[]): i32[] { return [xs.len()]; }\nfunction relay(ys: i32[]): i32[] { return sized(ys); }\nfunction main(): i32 { return keep(relay([1, 2])); }\n", nil},
		{"e051-result-is-the-borrowed-parameter", "function keep(own ys: i32[]): i32 { return ys[0]; }\nfunction passthru(xs: i32[]): i32[] { return xs; }\nfunction main(): i32 { return keep(passthru([1, 2])); }\n", []string{"E051"}},
		{"e051-result-borrowed-through-a-local", "function keep(own ys: i32[]): i32 { return ys[0]; }\nfunction hop(xs: i32[]): i32[] {\n    let y: i32[] = xs;\n    return y;\n}\nfunction main(): i32 { return keep(hop([1, 2])); }\n", []string{"E051"}},
		{"e051-result-borrowed-through-a-chain", "function keep(own ys: i32[]): i32 { return ys[0]; }\nfunction passthru(xs: i32[]): i32[] { return xs; }\nfunction relay(ys: i32[]): i32[] { return passthru(ys); }\nfunction main(): i32 { return keep(relay([1, 2])); }\n", []string{"E051"}},
		// A top-level const is a fresh value at every use, so it may be handed
		// to an `own` parameter; a parameter of the same name shadows it (#11471).
		{"own-accepts-a-const-array", "const W: i32[] = [1, 2];\nfunction keep(own ys: i32[]): i32 { return ys[0]; }\nfunction main(): i32 { return keep(W); }\n", nil},
		{"e051-param-shadows-a-const", "const W: i32[] = [1, 2];\nfunction keep(own ys: i32[]): i32 { return ys[0]; }\nfunction f(W: i32[]): i32 { return keep(W); }\nfunction main(): i32 { return f([3]); }\n", []string{"E051"}},
		// Call-site checks against a fn-typed PARAM (#5986's last half): the
		// param resolves to a real TypeFunc from its sidecars, so a
		// non-function argument draws E038 — the same code native emits —
		// while a named function (which now types as its signature's
		// function type) and a lambda both stay clean.
		{"e038-fn-param-bad-arg", "function mk(n: i32): i32 { return n + 1; }\nfunction apply(f: (i32) => i32): i32 { return f(1); }\nfunction main(): i32 { return apply(3); }\n", []string{"E038"}},
		{"e038-fn-param-named-fn-clean", "function mk(n: i32): i32 { return n + 1; }\nfunction apply(f: (i32) => i32): i32 { return f(1); }\nfunction main(): i32 { return apply(mk); }\n", nil},
		{"e038-fn-param-lambda-clean", "function apply(f: (i32) => i32): i32 { return f(1); }\nfunction main(): i32 { return apply((n: i32) => n + 2); }\n", nil},
		// A bare function name used as a value types as its signature's
		// function type now, so assigning it where a scalar is declared
		// draws native's E003 instead of an unknown-typed silence.
		{"e003-bare-fn-value-mismatch", "function mk(n: i32): i32 { return n + 1; }\nfunction main(): i32 { let x: i32 = mk; return x; }\n", []string{"E003"}},
		// A fn-typed TUPLE ELEMENT (#7961). The parser used to coarsen an
		// element containing an arrow to the bare tag `fn`, which no type
		// resolver had an arm for, so the DECLARED element read `unknown` and
		// every one of these drew E003 against an init the checker typed
		// correctly — a refusal native's -check does not make. The element now
		// keeps its signature and resolves to the opaque callable, so all three
		// spellings of a fn value in that position are clean: a bare zero-arg
		// name, a one-param name, and a lambda.
		{"e003-fn-tuple-elem-zeroarg-clean", "function a1(): i32 { return 3; }\nfunction main(): i32 { let t: ((() => i32), i32) = (a1, 4); return t.1; }\n", nil},
		{"e003-fn-tuple-elem-onearg-clean", "function inc(x: i32): i32 { return x + 1; }\nfunction main(): i32 { let t: (((i32) => i32), i32) = (inc, 4); return t.1; }\n", nil},
		{"e003-fn-tuple-elem-lambda-clean", "function main(): i32 { let t: ((() => i32), i32) = (() => 3, 4); return t.1; }\n", nil},
		// The other half of that resolution, and the reason it is a real type
		// rather than a hole: a NON-function in a fn-typed element is now
		// rejected against a TypeFunc, where before it was rejected against an
		// `unknown` that rejected everything alike. Same code, and now for the
		// reason native gives it.
		{"e003-fn-tuple-elem-scalar-init", "function main(): i32 { let t: ((() => i32), i32) = (4, 5); return t.1; }\n", []string{"E003"}},
		// The control the fix must not blunt: an ordinary element mismatch in a
		// tuple with no fn in it still draws E003.
		{"e003-tuple-elem-mismatch", "function main(): i32 { let t: (string, i32) = (1, 2); return t.1; }\n", []string{"E003"}},
		// An ARRAY literal's elements are checked against the destination's
		// element type, the way a tuple literal's already were. Native used to
		// stamp the destination's element type onto the literal unconditionally
		// — settling, which is right for an unsuffixed numeric and wrong for
		// anything else — so the literal then CLAIMED that type and the
		// comparison passed. The self-host has always rejected these, so these
		// rows were a real divergence before the native fix. Both directions,
		// because the stamp was reached from either.
		{"e003-array-lit-elem-mismatch", "function main(): i32 { let xs: i32[] = [\"ab\", \"cd\"]; return xs.len(); }\n", []string{"E003"}},
		{"e038-array-lit-arg-elem-mismatch", "function total(xs: i32[]): i32 { return xs.len(); }\nfunction main(): i32 { return total([\"ab\", \"cd\"]); }\n", []string{"E038"}},
		{"e038-array-lit-arg-numeric-into-string", "function take(xs: string[]): i32 { return xs.len(); }\nfunction main(): i32 { return take([1, 2, 3]); }\n", []string{"E038"}},
		// The controls the fix must not blunt: settling a polymorphic numeric
		// literal to the destination's width is the whole point of the stamp,
		// and an empty literal has no elements to contradict it.
		{"array-lit-settles-to-i64-clean", "function main(): i32 { let xs: i64[] = [1, 2, 3]; return xs.len(); }\n", nil},
		{"array-lit-settles-to-u8-clean", "function main(): i32 { let xs: u8[] = [1, 2, 3]; return xs.len(); }\n", nil},
		{"array-lit-empty-clean", "function main(): i32 { let xs: i32[] = []; return xs.len(); }\n", nil},
		// A polymorphic FLOAT element settles only to a float destination; the
		// scalar `let x: i64 = 1.5` is already E003 in both checkers, so the
		// array literal must not be the one way round it.
		{"e003-array-lit-float-into-int", "function main(): i32 { let xs: i64[] = [1.5, 2.5]; return xs.len(); }\n", []string{"E003"}},
		{"e038-array-lit-float-into-int-arg", "function total(xs: i64[]): i32 { return xs.len(); }\nfunction main(): i32 { return total([1.5, 2.5]); }\n", []string{"E038"}},
		// int-to-float promotion stays legal, and is the control for the row above.
		{"array-lit-int-into-float-clean", "function main(): i32 { let xs: f64[] = [1, 2]; return xs.len(); }\n", nil},
		// `@try` (docs/TRY.md): the marker's SHAPE obligation is enforced by
		// both compilers, so the rule is agreed even while only native can
		// lower `?` on a marked enum. A valid marked enum is clean on both;
		// each way of breaking the shape is E078 on both.
		{"try-marker-clean", "@try\nenum MyOpt[T] { Here(T), Gone }\nfunction pick(m: MyOpt[i32]): MyOpt[i32] { let v: i32 = m?; return Here(v + 1); }\nfunction main(): i32 { match (pick(Here(7))) { Here(v) => { return v; }, Gone => { return 0; } } }\n", nil},
		{"try-marker-payload-failure-clean", "@try\nenum Outcome[T, E] { Good(T), Bad(E) }\nfunction step(o: Outcome[i32, string]): Outcome[i32, string] { let v: i32 = o?; return Good(v * 2); }\nfunction main(): i32 { match (step(Good(21))) { Good(v) => { return v; }, Bad(e) => { return e.len(); } } }\n", nil},
		{"e078-three-variants", "@try\nenum Three[T] { A(T), B, C }\nfunction main(): i32 { return 0; }\n", []string{"E078"}},
		{"e078-success-variant-payloadless", "@try\nenum NoPay { A, B }\nfunction main(): i32 { return 0; }\n", []string{"E078"}},
		{"e078-failure-variant-two-payloads", "@try\nenum TwoPay[T, E] { A(T), B(E, E) }\nfunction main(): i32 { return 0; }\n", []string{"E078"}},
		// E079: `?` inside a `defer` action (#9470). The second row is the
		// shape the first one's walk missed — a defer nested in a LAMBDA body,
		// where the self-host mirror entered no expression and so accepted what
		// native refused. Both draw E042 as well, because a `?` anywhere in a
		// lambda reads the enclosing function's return type rather than the
		// lambda's (#9515); the differential is what pins them together.
		{"e079-defer-try-op", "function g(v: i32): Option[i32] { if (v < 100) { return Some(v + 1); } return None; }\nfunction f(): Option[i32] { let n: i32 = 0; defer n = g(n)?; return Some(n); }\nfunction main(): i32 { return 0; }\n", []string{"E079"}},
		{"e079-defer-inside-lambda-body", "function g(v: i32): Option[i32] { if (v < 100) { return Some(v + 1); } return None; }\nfunction main(): i32 { let h: (i32) => i32 = (x: i32) => { let n: i32 = x; defer n = g(n)?; return n; }; return h(1); }\n", []string{"E042", "E079"}},
		// The complementary shape — a lambda LITERAL in the action, whose `?`
		// leaves the lambda. Neither compiler reports E079; both report the
		// lambda's conflicting exits (the `?`'s Option and the i32 it yields)
		// and the lambda handed to an i32 parameter (#9518).
		{"e079-try-in-lambda-literal-in-defer", "function g(v: i32): Option[i32] { if (v < 100) { return Some(v + 1); } return None; }\nfunction f(out: Cell[i32]): i32 {\n    defer out.set((x: i32) => g(x)?);\n    return 0;\n}\nfunction main(): i32 { let c: Cell[i32] = cell_new(0); f(c); return c.get(); }\n", []string{"E002", "E038"}},
		// An unannotated lambda's result is inferred from every exit: each
		// value return and each `?` must agree, and a bare `return;` beside a
		// value is E012.
		{"lambda-exits-conflict", "function main(): i32 { let k: i32 = 3; let h = (b: boolean) => { if (b) { return k; } return true; }; return 0; }\n", []string{"E002"}},
		{"lambda-try-exit-conflict", "function g(v: i32): Option[i32] { if (v < 100) { return Some(v + 1); } return None; }\nfunction main(): i32 { let h = (x: i32) => { let y: i32 = g(x)?; return y; }; return 0; }\n", []string{"E002"}},
		{"lambda-try-exit-clean", "function g(v: i32): Option[i32] { if (v < 100) { return Some(v + 1); } return None; }\nfunction main(): i32 { let h = (x: i32) => { let y: i32 = g(x)?; if (y > 5) { return None; } return Some(y); }; let r: Option[i32] = h(1); return 0; }\n", nil},
		// Two exits of one union conflict when their arguments differ, and a
		// bare `None` between them adopts the first one's arguments rather
		// than hiding the conflict.
		{"lambda-exits-bare-union-keeps-arguments", "function g(v: i32): Option[i32] { return Some(v); }\nfunction s(v: i32): Option[string] { return None; }\nfunction main(): i32 { let h = (x: i32) => { if (x == 0) { return g(x); } if (x == 1) { return None; } return s(x); }; return 0; }\n", []string{"E002"}},
		{"lambda-exits-bare-and-value", "function main(): i32 { let k: i32 = 3; let h = (b: boolean) => { if (b) { return; } return k; }; return 0; }\n", []string{"E012"}},
		// The shadowing guard on that fallback: a binding typed opaquely
		// unknown (here a builtin variant payload) still shadows the module
		// function table. Without the is_bound gate, `Some(pair)` with a
		// user function named `pair` resolved the PAYLOAD read to the
		// function and E043'd ("field access on non-struct value of type
		// fn") — which broke std/unicode for every program defining `pair`.
		// The row wants NO codes; the uncoded #9053 unrepresentable-type
		// note this shape also prints carries none.
		{"fn-value-shadowed-by-payload-binding", "function pair(): i32 { return 7; }\nfunction main(): i32 {\n    let o: Option[(i32, i32)] = Some((1, 2));\n    match (o) {\n        Some(pair) => { return pair.0 + pair.1; },\n        None => { return 0; }\n    }\n    return 0;\n}\n", nil},
		// An annotation on a LAMBDA parameter or return, which the two
		// annotation reporters could not see at all: both walked statements
		// for `let` and nothing else, so `((x: Wibble) => 1)` was accepted
		// outright while the `let x: Wibble` beside it drew E064 (#7100).
		// Positive AND negative, because the fix widens what is looked at:
		// a declared type on the same shape must stay clean, or the walk
		// would be reporting on every lambda in the tree.
		//
		// The RETURN annotation is gated only by its clean row. An UNKNOWN
		// return type draws a second code natively — E002 for the body not
		// matching a type the same run has just rejected as undeclared —
		// which the self-host does not emit, so that shape measures an
		// unrelated difference rather than this one.
		{"e064-lambda-param", "function main(): i32 { let f = ((x: Wibble) => 1); return 0; }\n", []string{"E064"}},
		{"e064-lambda-param-clean", "struct P { x: i32 }\nfunction main(): i32 { let f = ((p: P) => p.x); return 0; }\n", nil},
		{"e064-lambda-return-clean", "struct P { x: i32 }\nfunction main(): i32 { let f = ((n: i32): P => P { x: n }); return 0; }\n", nil},
		// The same walk feeds E057, whose annotation form has the identical
		// blind spot — native has reported it on a lambda parameter all along.
		{"e057-lambda-param", "struct P { x: i32 }\nfunction main(): i32 { let f = ((c: Cell[P]) => 1); return 0; }\n", []string{"E057"}},
		// E072 with the code written out. The differential below derives its
		// expectation from the Go checker, so it cannot tell "both sides emit
		// E072" from "neither side emits anything"; this row can.
		{"e072-void-variant-payload", "function nothing(): void { }\nfunction f(): Option[()] { return Some(nothing()); }\nfunction main(): i32 { return 0; }\n", []string{"E072"}},
		// Bare no-payload enum variant used as a value (#4346 piece 2). A unit
		// variant (`Red`) types to its enum's union, so a matching declared
		// type is clean and a MISMATCHED one draws E003 (Color not assignable
		// to i32) — the same code the Go checker emits. Before the slice the
		// self-host typed `Red` as unknown, so E003 never fired here (the
		// mismatch went silently un-reported, invisible to this codes gate);
		// the mismatch case is what actively verifies the variant now types to
		// its union. The clean-accept cases (no over-reject) are pinned by the
		// self_host_cli_test `-check` exit-code tests, since a silent
		// over-reject also emits no codes and would pass this gate regardless.
		{"enum-value-mismatch", "enum Color { Red, Green }\nfunction main(): i32 { let x: i32 = Red; return x; }\n", []string{"E003"}},
		{"enum-value-assign-clean", "enum Color { Red, Green }\nfunction main(): i32 { let c: Color = Red; return 0; }\n", nil},
		{"enum-value-return-clean", "enum Color { Red, Green }\nfunction pick(): Color { return Green; }\nfunction main(): i32 { let c: Color = pick(); return 0; }\n", nil},
		// A struct FIELD typed by an enum. The struct table is built before the
		// full union table exists, from the declared union NAMES alone, and an
		// enum names a union exactly as a type alias does.
		{"enum-struct-field-clean", "enum Color { Red, Green }\nstruct H { c: Color, n: i32 }\nfunction main(): i32 { let h: H = H { c: Red, n: 1 }; return h.n; }\n", nil},
		{"enum-struct-field-mismatch", "enum Color { Red, Green }\nstruct H { c: Color, n: i32 }\nfunction main(): i32 { let h: H = H { c: 5, n: 1 }; return h.n; }\n", []string{"E043"}},
		{"enum-struct-field-match-clean", "enum Color { Red, Green(i32) }\nstruct H { c: Color, n: i32 }\nfunction main(): i32 { let h: H = H { c: Green(3), n: 1 };\n    match (h.c) { Green(v) => { return v; }, _ => { return 0; } }\n}\n", nil},
		{"enum-struct-field-payload-mismatch", "enum Color { Red, Green(i32) }\nstruct H { c: Color, n: i32 }\nfunction main(): i32 { let h: H = H { c: Green(3), n: 1 };\n    match (h.c) { Green(v) => { let s: string = v; return 0; }, _ => { return 0; } }\n}\n", []string{"E003"}},
		// Builtin Option/Result as values (#4346 piece 2, second slice). The
		// generic annotation `Option[i32]` resolves to a name-only union, the
		// constructor call `Some(3)` / `Ok(3)` types to that union, and bare
		// `None` does too — so a matching declared type is clean and a
		// MISMATCHED one (`let x: i32 = Some(3)`) draws E003, the same code the
		// Go checker emits. Before the slice all three collapsed to unknown, so
		// E003 never fired (the mismatch was silently un-reported). The
		// mismatch cases verify the value now types to its union; the clean
		// accepts are pinned by the self_host_cli_test `-check` exit-code tests.
		{"option-some-mismatch", "function main(): i32 { let x: i32 = Some(3); return x; }\n", []string{"E003"}},
		{"result-ok-mismatch", "function main(): i32 { let x: i32 = Ok(3); return x; }\n", []string{"E003"}},
		{"option-some-clean", "function main(): i32 { let o: Option[i32] = Some(3); return 0; }\n", nil},
		{"option-none-clean", "function main(): i32 { let o: Option[i32] = None; return 0; }\n", nil},
		{"result-ok-clean", "function main(): i32 { let r: Result[i32, i32] = Ok(3); return 0; }\n", nil},
		// Generic-call return-type inference (#4346 piece 2): a call to a
		// generic function whose return type NAMES a parameter's type
		// (`ident[T](v: T): T`) infers the concrete return from that argument,
		// so `ident(3)` types to i32 — a MISMATCHED destination (`string`) draws
		// E003, the same code the Go checker emits, where the pre-slice
		// self-host typed the call as unknown and E003 never fired. The clean
		// call path is pinned by the self_host_cli_test exit-code test.
		{"generic-call-mismatch", "function ident[T](v: T): T { return v; }\nfunction main(): i32 { let x: string = ident(3); return 0; }\n", []string{"E003"}},
		{"generic-call-clean", "function ident[T](v: T): T { return v; }\nfunction main(): i32 { return ident(3); }\n", nil},
		// `str`, the borrowed-string view (#7293). The spelling reaches the
		// checker unerased (#9915), and slice_unchecked yields one; every
		// owning sink then refuses the view
		// exactly as native does — E003 on a var init and an assignment, E002
		// on a return, E043 on a struct-literal field — while a `str`
		// destination, an argument position (params are borrowed), and a
		// `str`-returning function stay clean. Before the fix the self-host
		// accepted every one of the mismatch rows.
		{"str-view-into-string-var-e003", "function main(): i32 { let t: string = \"abcdef\"; let s: string = slice_unchecked(t, 0, 3); return 0; }\n", []string{"E003"}},
		{"str-view-assign-e003", "function main(): i32 { let t: string = \"abcdef\"; let s: string = \"x\"; s = slice_unchecked(t, 0, 3); return 0; }\n", []string{"E003"}},
		{"str-view-return-e002", "function f(t: string): string { return slice_unchecked(t, 0, 3); }\nfunction main(): i32 { return 0; }\n", []string{"E002"}},
		{"str-view-field-e043", "struct Q { tag: string }\nfunction mk(t: string): Q { return Q { tag: slice_unchecked(t, 0, 3) }; }\nfunction main(): i32 { return 0; }\n", []string{"E043"}},
		{"str-ret-fn-into-string-e003", "function f(t: string): str { return slice_unchecked(t, 0, 3); }\nfunction main(): i32 { let s: string = f(\"abcdef\"); return 0; }\n", []string{"E003"}},
		{"str-view-annotated-clean", "function f(t: string): i32 { let v: str = slice_unchecked(t, 0, 3); return v.len(); }\nfunction main(): i32 { return f(\"abcdef\"); }\n", nil},
		{"str-view-arg-borrow-clean", "function g(x: string): i32 { return x.len(); }\nfunction main(): i32 { let t: string = \"abcdef\"; return g(slice_unchecked(t, 0, 3)); }\n", nil},
		{"str-ret-fn-into-str-clean", "function f(t: string): str { return slice_unchecked(t, 0, 3); }\nfunction main(): i32 { let v: str = f(\"abcdef\"); return v.len(); }\n", nil},
		// A `str` struct field and tuple element are views too (#9915): each
		// takes a view, and hands one to a `str` binding but not a `string`
		// one. A `str` receiver shares the `string` method namespace.
		{"str-view-into-str-field-clean", "struct H { s: str, n: i32 }\nfunction mk(t: string): H { return H { s: slice_unchecked(t, 0, 3), n: 1 }; }\nfunction main(): i32 { return mk(\"abcde\").n; }\n", nil},
		{"str-field-into-string-var-e003", "struct H { s: str }\nfunction get(h: H): i32 { let v: string = h.s; return v.len(); }\nfunction main(): i32 { return get(H { s: \"abc\" }); }\n", []string{"E003"}},
		{"str-field-into-str-var-clean", "struct H { s: str }\nfunction get(h: H): i32 { let v: str = h.s; return v.len(); }\nfunction main(): i32 { return get(H { s: \"abc\" }); }\n", nil},
		{"str-tuple-elem-into-string-var-e003", "function main(): i32 { let p: (str, i32) = (\"abc\", 4); let v: string = p.0; return v.len(); }\n", []string{"E003"}},
		{"str-tuple-elem-into-str-var-clean", "function main(): i32 { let p: (str, i32) = (\"abc\", 4); let v: str = p.0; return v.len() + p.1; }\n", nil},
		{"str-tuple-result-elem-into-string-e003", "function mk(t: string): (str, i32) { return (slice_unchecked(t, 0, 2), 1); }\nfunction main(): i32 { let v: string = mk(\"abc\").0; return v.len(); }\n", []string{"E003"}},
		{"str-receiver-method-clean", "function (s: str) first(): u8 { return s[0]; }\nfunction main(): i32 { let v: str = \"hey\"; return v.first() as i32; }\n", nil},
		// `[T]`, the array view a slice or `.as_bytes()` yields (#9944). A view
		// and an owned `T[]` convert in neither direction — var init, assignment,
		// return, field, argument — except an owned argument lent to a `[T]`
		// parameter. A `[T]` binding, a slice of a view, a `[T]`-returning
		// function and a `[T]` parameter handed on to another stay clean.
		{"arr-view-into-array-var-e003", "function main(): i32 { let all: string[] = [\"a\", \"b\", \"c\"]; let mid: string[] = all[1:3]; return mid.len(); }\n", []string{"E003"}},
		{"arr-view-assign-e003", "function main(): i32 { let x: i32[] = [1, 2]; let y: i32[] = [3]; y = x[0:1]; return y.len(); }\n", []string{"E003"}},
		{"arr-view-return-e002", "function g(a: i32[]): i32[] { return a[0:1]; }\nfunction main(): i32 { return 0; }\n", []string{"E002"}},
		{"arr-view-field-e043", "struct Q { xs: i32[] }\nfunction mk(a: i32[]): Q { return Q { xs: a[0:1] }; }\nfunction main(): i32 { return 0; }\n", []string{"E043"}},
		{"arr-ret-fn-into-array-e003", "function first(a: [i32]): [i32] { return a[0:1]; }\nfunction main(): i32 { let x: i32[] = [1, 2]; let o: i32[] = first(x); return o.len(); }\n", []string{"E003"}},
		{"arr-owned-into-view-var-e003", "function main(): i32 { let a: i32[] = [1]; let w: [i32] = a; return w.len(); }\n", []string{"E003"}},
		{"arr-owned-return-from-view-fn-e002", "function f(a: i32[]): [i32] { return a; }\nfunction main(): i32 { return 0; }\n", []string{"E002"}},
		{"arr-view-param-into-array-param-e038", "function h(x: i32[]): i32 { return x.len(); }\nfunction k(v: [i32]): i32 { return h(v); }\nfunction main(): i32 { return 0; }\n", []string{"E038"}},
		{"as-bytes-into-array-var-e003", "function main(): i32 { let s: string = \"ab\"; let b: u8[] = s.as_bytes(); return b.len(); }\n", []string{"E003"}},
		{"arr-view-param-clean", "function n(v: [u8]): i32 { return v.len(); }\nfunction k(v: [u8]): i32 { let w: [u8] = v[0:1]; return n(v) + n(w); }\nfunction main(): i32 { let s: string = \"ab\"; let b: [u8] = s.as_bytes(); let o: u8[] = [1, 2]; return k(b) + k(o); }\n", nil},
		{"arr-view-clean", "function sum(s: [i32]): i32 { let t: i32 = 0; for v in s { t = t + v; } return t; }\nfunction first(a: [i32]): [i32] { return a[0:1]; }\nfunction main(): i32 { let a: i32[] = [1, 2, 3, 4, 5]; let s: [i32] = a[1:4]; let s2: [i32] = s[0:2]; let u = a[0:2]; let f: [i32] = first(a); return sum(a[0:5]) + s.len() + s2[1] + u.len() + f[0] + sum(s); }\n", nil},
		// #7311's remaining half: string-builtin and free-builtin arity.
		// These used to fall through to IR lowering and surface as the
		// whole-function #9053 ineligibility hint; native reports E004 at
		// the call. The clean row pins the arity constants AND the new
		// builtin result types (print is void, as_bytes u8[]) — before
		// them, even `print("a")` marked the function ill-typed under
		// -check.
		{"string-len-arity-e004", "function main(): i32 { let s: string = \"abc\"; return s.len(1); }\n", []string{"E004"}},
		{"string-as-bytes-arity-e004", "function main(): i32 { let s: string = \"abc\"; return s.as_bytes(1).len(); }\n", []string{"E004"}},
		{"print-arity-e004", "function main(): i32 { print(\"a\", \"b\"); return 0; }\n", []string{"E004"}},
		{"eprint-arity-e004", "function main(): i32 { eprint(\"a\", \"b\"); return 0; }\n", []string{"E004"}},
		{"write-arity-e004", "function main(): i32 { write(\"a\", \"b\"); return 0; }\n", []string{"E004"}},
		{"slice-unchecked-arity-e004", "function main(): i32 { let s: string = \"abcdef\"; let t: str = slice_unchecked(s, 1); return t.len(); }\n", []string{"E004"}},
		// target_os() is a string under -check, where nothing folds it: a
		// clean use types, a mismatch is E003, and an argument is E004 — the
		// three answers native gives.
		{"target-os-clean", "function main(): i32 { let os: string = target_os(); if (os == \"linux\" || target_os() != \"wasi\") { return 1; } return 0; }\n", nil},
		{"target-os-mismatch-e003", "function main(): i32 { let n: i32 = target_os(); return n; }\n", []string{"E003"}},
		{"target-os-arity-e004", "function main(): i32 { let os: string = target_os(1); return 0; }\n", []string{"E004"}},
		{"builtins-correct-arity-clean", "function main(): i32 { print(\"a\"); let s: string = \"abc\"; return s.len() + s.as_bytes().len(); }\n", nil},
		// The Display spine (docs/TRAITS.md §3a): a `print` / `write` /
		// `eprint` argument whose type resolves no `to_string` is E038, on
		// both compilers; a struct carrying its own is accepted (#9945).
		{"display-i32-without-to-string-e038", "function main(): i32 { print(7); return 0; }\n", []string{"E038"}},
		{"display-u8-array-e038", "function main(): i32 { let b: u8[] = [72 as u8]; write(b); return 0; }\n", []string{"E038"}},
		{"display-f64-without-to-string-e038", "function main(): i32 { let v: f64 = 1.5; eprint(v); return 0; }\n", []string{"E038"}},
		{"display-struct-with-to-string-clean", "struct Q { a: i32 }\nfunction (q: Q) to_string(): string { return \"Q\"; }\nfunction main(): i32 { print(Q { a: 1 }); return 0; }\n", nil},
		{"display-i32-with-own-to-string-clean", "function (n: i32) to_string(): string { return \"n\"; }\nfunction main(): i32 { print(5); return 0; }\n", nil},
		// User generic-struct instantiation (#4346 piece 2): a `Box[i32]`
		// annotation resolves to the name-only struct `Box`, and constructing
		// `Box { v: 3 }` type-checks (the opaque generic field `v: T` accepts any
		// value). A MISMATCHED initialiser (`= 5`) draws E003 — the same code the
		// Go checker emits — where the pre-slice self-host typed the annotation
		// as unknown and E003 never fired. The clean construction is pinned
		// end-to-end by the self_host_cli_test exit-code test. (Field access
		// `b.v` still yields unknown — the field's type parameter isn't
		// substituted yet — so it's a further slice, kept out of these cases.)
		{"generic-struct-mismatch", "struct Box[T] { v: T }\nfunction main(): i32 { let b: Box[i32] = 5; return 0; }\n", []string{"E003"}},
		{"generic-struct-clean", "struct Box[T] { v: T }\nfunction main(): i32 { let b: Box[i32] = Box { v: 3 }; return 0; }\n", nil},
		// Generic-struct FIELD-ACCESS substitution (#4346 piece 2): reading a
		// field typed by a type parameter off a concrete instantiation
		// (`Box[i32].v`) yields the substituted arg (i32), so assigning it to a
		// MISMATCHED destination (`string`) draws E003 — the same code the Go
		// checker emits — where the pre-slice self-host typed `b.v` as unknown
		// and E003 never fired. The clean read is pinned by the CLI exit test.
		{"generic-struct-field-mismatch", "struct Box[T] { v: T }\nfunction main(): i32 { let b: Box[i32] = Box { v: 3 }; let s: string = b.v; return 0; }\n", []string{"E003"}},
		// NESTED generic field spellings (#4346 piece 2): a type parameter INSIDE
		// a field's spelling is substituted throughout, so off a `Wrapper[i32]`
		// (`items: T[]`) the field `items` is i32[] and its element is i32 —
		// binding it to a MISMATCHED `string` draws E003, matching the Go
		// checker, where the pre-slice self-host typed the nested field unknown
		// and E003 never fired. The clean read is pinned by the CLI exit test.
		{"generic-nested-field-mismatch", "struct Wrapper[T] { items: T[] }\nfunction main(): i32 { let w: Wrapper[i32] = Wrapper { items: [1, 2, 3] }; let s: string = w.items[0]; return 0; }\n", []string{"E003"}},
		{"generic-nested-field-clean", "struct Wrapper[T] { items: T[] }\nfunction main(): i32 { let w: Wrapper[i32] = Wrapper { items: [1, 2, 3] }; return w.items[0]; }\n", nil},
		// Generic-receiver METHOD return substitution (#4346 piece 2): a method
		// whose declared return names a receiver type parameter (`(b: Box[T])
		// get(): T`) resolves to the instantiation arg off a concrete receiver,
		// so `Box[i32].get()` is i32 — assigning it to a MISMATCHED destination
		// draws E003 (matching the Go checker) where the pre-slice self-host
		// typed the call unknown and E003 never fired. The clean call is pinned
		// by the CLI exit test.
		{"generic-method-ret-mismatch", "struct Box[T] { v: T }\nfunction (b: Box[T]) get(): T { return b.v; }\nfunction main(): i32 { let b: Box[i32] = Box { v: 5 }; let s: string = b.get(); return 0; }\n", []string{"E003"}},
		{"generic-method-ret-clean", "struct Box[T] { v: T }\nfunction (b: Box[T]) get(): T { return b.v; }\nfunction main(): i32 { let b: Box[i32] = Box { v: 5 }; return b.get(); }\n", nil},
		// E064: a bare nominal annotation that names no declared type, in a
		// non-generic function parameter — both checkers flag it.
		{"unknown-param-type", "function f(a: Wibble): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E064"}},
		{"unknown-field-type", "struct S { v: Wibble }\nfunction main(): i32 { return 0; }\n", []string{"E064"}},
		// E064 in a body `let` annotation. The init `q()` is itself undefined
		// (E001), so there is no E003 init-mismatch cascade to diverge on.
		{"unknown-var-type", "function main(): i32 { let x: Wibble = q(); return 0; }\n", []string{"E001", "E064"}},
		// A typed init into that annotation is E003 beside the E064, since
		// the type is unknown to native too (#10965); a bare unknown struct
		// literal is E043 where it stands.
		{"unknown-var-type-typed-init", "function main(): i32 { let x: Wibble = 3; return 0; }\n", []string{"E003", "E064"}},
		{"unknown-struct-literal", "function main(): i32 { let w = Wibble { x: 1 }; return 0; }\n", []string{"E043"}},
		// Sub-word integer keywords (u8/usize) the parser accepts but the
		// self-host name resolver doesn't model. They must NOT draw E064 in a
		// body `let` annotation — the Go oracle accepts them, and the stdlib uses
		// them (`let b: u8`, `let p: usize`), so a false E064 here would bail every
		// importing module off the IR path (the #3813 regression).
		{"subword-int-vars-clean", "function main(): i32 { let a: u8 = 1 as u8; let e: usize = 1 as usize; return 0; }\n", nil},
		// `byte` is NOT a parser keyword, so the Go checker flags it E064 too —
		// the self-host must keep flagging it (init `q()` is E001, avoiding an
		// E003 init-mismatch cascade, same as unknown-var-type above).
		{"unknown-byte-var-type", "function main(): i32 { let x: byte = q(); return 0; }\n", []string{"E001", "E064"}},
		// Only the method spelling `a.len()` exists; a free `len(a)` names nothing.
		{"free-len-is-undefined", "function main(): i32 { let a: i32[] = [1, 2]; return len(a); }\n", []string{"E001"}},
		// Reading all of stdin is std/io's `io.read_all_stdin()`; there is no bare builtin.
		{"free-read-all-stdin-is-undefined", "function main(): i32 { let s: string = read_all_stdin(); return s.len(); }\n", []string{"E001"}},
		// isize/i8/i16/u16 were retired (#4408): neither is a lexer keyword
		// any more, so a reference to one is an unknown nominal type — both
		// checkers must now flag E064 here, the mirror image of the
		// subword-int-vars-clean case above.
		{"unknown-retired-subword-var-type", "function main(): i32 { let x: i8 = q(); return 0; }\n", []string{"E001", "E064"}},
		// `float` is the width-unqualified f64 alias (#5363). The self-host
		// checker always resolved it; the Go checker used to reject it with
		// E064 (+ a "did you mean f64?" hint) — this fixture pins the
		// reconciled behavior: clean on BOTH checkers, including flowing
		// into an f64 destination.
		{"float-alias-ok", "function main(): i32 { let x: float = 1.5; let y: f64 = x; if (y > 1.0) { return 0; } return 1; }\n", nil},
		// ... and a `float` value in a mismatched destination draws the
		// same E003 both sides (it is a real float type, not unknown).
		{"float-alias-mismatch", "function main(): i32 { let x: float = 1.5; let s: string = x; return 0; }\n", []string{"E003"}},
		// E064 widening (#4363 item 3): an unknown nominal reached through an
		// array-element (`Nope[]`) or generic-argument (`Map[string, Nope]`)
		// spelling draws E064 just like a bare `Nope` — the check used to bail on
		// any non-identifier text, so these inner positions went unflagged. The
		// emitted message names the INNER unknown ("Nope"), matching the Go
		// oracle. Each shape is chosen with no init, so there's no E001/E003
		// cascade to diverge on.
		{"unknown-arrelem-param", "function f(a: Nope[]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E064"}},
		{"unknown-arrelem-nested-param", "function f(a: Nope[][]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E064"}},
		{"unknown-genarg-param", "function f(m: Map[string, Nope]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E064"}},
		{"unknown-arrelem-field", "struct S { xs: Nope[] }\nfunction main(): i32 { return 0; }\n", []string{"E064"}},
		{"unknown-genarg-field", "struct S { m: Map[string, Nope] }\nfunction main(): i32 { return 0; }\n", []string{"E064"}},
		{"unknown-arrelem-var", "function main(): i32 { let xs: Nope[] = []; return 0; }\n", []string{"E064"}},
		// Negative controls: a valid array-element / generic-argument type must
		// NOT draw E064 — the widening only checks the INNER name, never the
		// builtin generic base (Map / Cell), so these stay clean like native.
		{"arrelem-ok-param", "function f(a: string[]): i32 { return a.len(); }\nfunction main(): i32 { return 0; }\n", nil},
		{"genarg-cell-ok-param", "function f(c: Cell[i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		{"genarg-map-ok-param", "function f(m: Map[string, i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		{"arrelem-ok-field", "struct S { xs: string[] }\nfunction main(): i32 { return 0; }\n", nil},
		// E021 (#4347): an impl that omits a REQUIRED (abstract) trait method.
		// A complete impl and a default-only trait (whose default is synthesised
		// onto the omitting impl) stay clean, matching the Go oracle.
		{"impl-missing-method", "trait Greet { function hello(): i32; }\nstruct Dog {}\nimpl Greet for Dog {}\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"impl-complete-ok", "trait Greet { function hello(): i32; }\nstruct Dog {}\nimpl Greet for Dog { function hello(): i32 { return 1; } }\nfunction main(): i32 { return 0; }\n", nil},
		{"impl-default-omitted-ok", "trait Greet { function hi(): i32 { return 9; } }\nstruct Dog {}\nimpl Greet for Dog {}\nfunction main(): i32 { return 0; }\n", nil},
		{"impl-missing-one-of-two", "trait Two { function a(): i32; function b(): i32; }\nstruct S {}\nimpl Two for S { function a(): i32 { return 1; } }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		// E021 receiver rule: a method on a built-in enum (`Body`, `Option`)
		// is as clean as one on a built-in struct, matching the Go checker,
		// where the self-host once drew E021 for every built-in enum receiver.
		{"builtin-enum-receiver-ok", "function (b: Body) kind(): i32 { return 1; }\nfunction main(): i32 { return 0; }\n", nil},
		// E021 signature mismatch (#4347 slice 2): the impl provides the method
		// but with the wrong arity / param type / return type vs the trait's
		// declaration (Self resolves to the impl type). A correct impl is clean.
		{"impl-sig-arity", "trait T { function m(self: Self, x: i32): i32; }\nstruct S { v: i32 }\nimpl T for S { function m(self: Self): i32 { return 1; } }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"impl-sig-ret", "trait T { function m(self: Self): i32; }\nstruct S { v: i32 }\nimpl T for S { function m(self: Self): string { return \"x\"; } }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"impl-sig-paramtype", "trait T { function m(self: Self, x: i32): i32; }\nstruct S { v: i32 }\nimpl T for S { function m(self: Self, x: string): i32 { return 1; } }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"impl-sig-correct-ok", "trait T { function m(self: Self, x: i32): i32; }\nstruct S { v: i32 }\nimpl T for S { function m(self: Self, x: i32): i32 { return x; } }\nfunction main(): i32 { return 0; }\n", nil},
		// E021 supertrait conformance (#4347 slice 3): `impl B for S` where
		// `trait B: A` requires a separate `impl A for S`. Missing it (or any one
		// of several supertraits) draws E021; providing all of them is clean.
		{"impl-supertrait-missing", "trait A { function a(self: Self): i32; }\ntrait B: A { function b(self: Self): i32; }\nstruct S { v: i32 }\nimpl B for S { function b(self: Self): i32 { return 1; } }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"impl-supertrait-multi-missing", "trait A { function a(self: Self): i32; }\ntrait C { function c(self: Self): i32; }\ntrait B: A + C { function b(self: Self): i32; }\nstruct S { v: i32 }\nimpl A for S { function a(self: Self): i32 { return 2; } }\nimpl B for S { function b(self: Self): i32 { return 1; } }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"impl-supertrait-satisfied-ok", "trait A { function a(self: Self): i32; }\ntrait B: A { function b(self: Self): i32; }\nstruct S { v: i32 }\nimpl A for S { function a(self: Self): i32 { return 2; } }\nimpl B for S { function b(self: Self): i32 { return 1; } }\nfunction main(): i32 { return 0; }\n", nil},
		// E021 generic-bound conformance AT CALL SITES (#4842): calling
		// `f[T: Tr](x)` with an argument whose concrete type doesn't implement
		// Tr draws E021 — the last unported E021 shape (the impl-side family
		// above is #4347). A struct or primitive with no `impl Tr` fails; an
		// explicit impl, an `@derive(Tr)` (whose synthesised methods witness
		// conformance), and an argument that stays generic (an opaque type
		// param — the checker can't know its instantiation, so it never fires)
		// are all clean. A `T: A + B` bound missing either trait fails. Each
		// verified against the Go oracle (the native checker emits the same set).
		{"bound-struct-no-impl", "trait Ord { function cmp(self: Self, other: Self): i32; }\nstruct Foo { x: i32 }\nfunction pick[T: Ord](a: T, b: T): T { return a; }\nfunction main(): i32 { let p: Foo = Foo { x: 1 }; let r: Foo = pick(p, p); return r.x; }\n", []string{"E021"}},
		{"bound-prim-no-impl", "trait Ord { function cmp(self: Self, other: Self): i32; }\nfunction pick[T: Ord](a: T): T { return a; }\nfunction main(): i32 { return pick(3); }\n", []string{"E021"}},
		{"bound-struct-impl-ok", "trait Ord { function cmp(self: Self, other: Self): i32; }\nstruct Foo { x: i32 }\nimpl Ord for Foo { function cmp(self: Self, other: Self): i32 { return 0; } }\nfunction pick[T: Ord](a: T): T { return a; }\nfunction main(): i32 { let p: Foo = Foo { x: 1 }; let r: Foo = pick(p); return r.x; }\n", nil},
		// bound-derive-ok carries `impl Ord for i32`: the derived `cmp`
		// dispatches per-field to the FIELD type's impl (synthOrd emits
		// `self.x.cmp(other.x)` — the ord_struct_enum e2e fixture is the
		// design reference), so without it the derive is rejected by the
		// E021 field-conformance pre-check below (#5392).
		{"bound-derive-ok", "trait Ord { function cmp(self: Self, other: Self): i32; }\nimpl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } }\n@derive(Ord)\nstruct Foo { x: i32 }\nfunction pick[T: Ord](a: T): T { return a; }\nfunction main(): i32 { let p: Foo = Foo { x: 1 }; let r: Foo = pick(p); return r.x; }\n", nil},
		// An untyped literal argument takes the type the call binds T to (i64
		// from `xs`), not its default i32, which has no `impl Add`.
		{"bound-literal-takes-binding-ok", "trait Add { function add(self: Self, o: Self): Self; }\nimpl Add for i64 { function add(self: Self, o: Self): Self { return self + o; } }\nfunction sum_with[T: Add](xs: T[], zero: T): T { let acc = zero; for x in xs { acc = acc.add(x); } return acc; }\nfunction main(): i32 { let xs: i64[] = [30, 11]; return sum_with(xs, 0) as i32; }\n", nil},
		// E021 @derive field conformance (#5392): deriving Eq / Ord / Hash
		// for a type whose field (or enum variant payload) type does not
		// implement the trait — no impl, no derive of its own, no method
		// set — draws ONE positioned E021 at the deriving decl instead of
		// the position-less per-field E043 errors the ill-typed
		// synthesized body used to surface. With the impl present the
		// derive is clean; a two-field gap still reports a single error.
		{"derive-field-no-impl", "trait Ord { function cmp(self: Self, other: Self): i32; }\n@derive(Ord)\nstruct Foo { x: i32 }\nfunction main(): i32 { let p: Foo = Foo { x: 1 }; return p.x; }\n", []string{"E021"}},
		{"derive-field-impl-ok", "trait Ord { function cmp(self: Self, other: Self): i32; }\nimpl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } }\n@derive(Ord)\nstruct Foo { x: i32 }\nfunction main(): i32 { let p: Foo = Foo { x: 1 }; return p.x; }\n", nil},
		{"derive-enum-payload-no-impl", "trait Eq { function eq(self: Self, other: Self): boolean; }\n@derive(Eq)\nenum E { A, B(i32) }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"derive-two-fields-no-impl", "trait Ord { function cmp(self: Self, other: Self): i32; }\n@derive(Ord)\nstruct P { x: i32, y: i32 }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		// The pre-check covers every derivable kind whose synthesised body
		// calls the trait method on each field, not just Eq/Ord/Hash:
		// Display / Debug / Json compose identically (`self.f.to_string()`
		// / `to_debug()` / `to_json()`), so each draws the same positioned
		// E021 at the deriving decl instead of the position-less E043 that
		// names the trait's method as a missing field.
		//
		// The BROKEN path agrees too. It did not always: the self-host
		// synthesises derived bodies at PARSE time — before any `impl` is
		// known — so the ill-typed `self.f.<method>()` survived to its
		// checker and stacked a position-less E043 on top of the E021.
		// Native never has that body, because it skips synthesis once the
		// pre-check fires. The self-host now reaches the same end state
		// from the other side: e021_derive_field_diags hands back the
		// "Type.method" key of each derive it rejected, and the body loop
		// declines to check exactly those synthesised functions
		// (derive_body_suppressed). Only synthesised methods sit at 0:0, so
		// a user-written method of the same name is still checked.
		//
		// This spans all six field-wise kinds, not the three whose gate
		// #5948 widened. Eq/Ord/Hash were said to escape it because their
		// bodies render inline (`==` / `<`) — but that holds only for a
		// SCALAR field. Over a NOMINAL one, `Ord`'s body calls `.cmp()` and
		// diverged identically; `derive-ord-field-broken` pins it.
		{"derive-display-impl-ok", "trait Display { function to_string(self: Self): string; }\nimpl Display for i32 { function to_string(self: Self): string { return \"n\"; } }\n@derive(Display)\nstruct Foo { x: i32 }\nfunction main(): i32 { return 0; }\n", nil},
		// Debug uses a NOMINAL field: the self-host renders Debug
		// type-directed, sending scalars through `to_string` rather than
		// `to_debug`, so a scalar carrying only a Debug impl diverges for
		// reasons of its own. A nominal field routes through `to_debug` on
		// both sides.
		{"derive-debug-impl-ok", "trait Debug { function to_debug(self: Self): string; }\nstruct Bare { n: i32 }\nimpl Debug for Bare { function to_debug(self: Self): string { return \"b\"; } }\n@derive(Debug)\nstruct Foo { b: Bare }\nfunction main(): i32 { return 0; }\n", nil},
		{"derive-json-impl-ok", "trait Json { function to_json(self: Self): string; }\nimpl Json for i32 { function to_json(self: Self): string { return \"0\"; } }\n@derive(Json)\nstruct Foo { x: i32 }\nfunction main(): i32 { return 0; }\n", nil},
		// A value-block arm that always leaves the function hands the block no
		// value, so its unreachable filler is not an arm type (#9326).
		{"value-block-arm-returns-early", "function probe(n: i32): boolean {\n  let s: string = match (n) { 0 => \"zero\", _ => { return false; } };\n  return s.len() > 0;\n}\nfunction main(): i32 { if (probe(0)) { return 1; } return 0; }\n", nil},
		// A derive resolves its trait by the name as written (#9322): with no
		// prelude, a bare `Eq` names nothing unless the program declares it,
		// and a declared trait outside the derivable set is refused. A
		// qualified derive through an import this single-module driver never
		// loaded is declined; the bundle differential covers the loaded case.
		{"derive-unknown-trait", "@derive(Eq)\nstruct P { n: i32 }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"derive-not-derivable", "trait Frob { function frob(self: Self): i32; }\n@derive(Frob)\nstruct P { n: i32 }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"derive-unloaded-import", "import \"core/cmp\";\n@derive(cmp.Eq)\nstruct P { n: i32 }\nfunction main(): i32 { return 0; }\n", nil},
		// The broken path, one per field-wise kind: a nominal field with no
		// impl of the derived trait. Each is E021 ALONE — an E043 here means
		// the synthesised body escaped suppression.
		{"derive-display-field-broken", "trait Display { function to_string(self: Self): string; }\nstruct Bare { n: i32 }\n@derive(Display)\nstruct HasBare { b: Bare }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"derive-debug-field-broken", "trait Debug { function to_debug(self: Self): string; }\nstruct Bare { n: i32 }\n@derive(Debug)\nstruct HasBare { b: Bare }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"derive-json-field-broken", "trait Json { function to_json(self: Self): string; }\nstruct Bare { n: i32 }\n@derive(Json)\nstruct HasBare { b: Bare }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"derive-ord-field-broken", "trait Ord { function cmp(self: Self, other: Self): i32; }\nstruct Bare { n: i32 }\n@derive(Ord)\nstruct HasBare { b: Bare }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		// The ENUM path: derives are stamped on each variant, and the
		// rejected key is the ENUM's name, not a variant's.
		{"derive-debug-enum-broken", "trait Debug { function to_debug(self: Self): string; }\nstruct Bare { n: i32 }\n@derive(Debug)\nenum E { A(Bare), B(i32) }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		// Suppression is keyed to the rejected derive, not to E043 at
		// large: a genuine bad field access is still reported, and so is a
		// USER-written method of a derived trait's name (it sits at a real
		// position, so it never matches a synthesised key).
		{"real-e043-still-reported", "struct S { n: i32 }\nfunction main(): i32 { let s: S = S { n: 1 }; return s.missing; }\n", []string{"E043"}},
		{"user-written-method-still-checked", "trait Debug { function to_debug(self: Self): string; }\nstruct Bare { n: i32 }\nstruct Foo { b: Bare }\nimpl Debug for Foo { function to_debug(self: Self): string { return self.nope; } }\nfunction main(): i32 { return 0; }\n", []string{"E043"}},
		// u8 / char are named by ast.ReceiverTypeName, so the pre-check must
		// reason about them too; e021_derive_field_known omitted both, which
		// left native reporting E021 where the self-host reported only the
		// E043 from the body it then failed to suppress.
		{"derive-u8-field-broken", "trait Debug { function to_debug(self: Self): string; }\n@derive(Debug)\nstruct Foo { b: u8 }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"derive-char-field-broken", "trait Debug { function to_debug(self: Self): string; }\n@derive(Debug)\nstruct Foo { c: char }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"bound-opaque-generic-ok", "trait Ord { function cmp(self: Self, other: Self): i32; }\nfunction inner[T: Ord](a: T): T { return a; }\nfunction outer[U](x: U): U { return inner(x); }\nfunction main(): i32 { return 0; }\n", nil},
		// A type PARAMETER is a type variable inside its own function, whatever
		// declared type shares its name. Every module is merged into one program
		// before checking, so a user `struct T` — or an enum variant `T`, which
		// registers as one — otherwise captured the `T` of every stdlib generic:
		// `a` bound to that struct and `b.cmp(a)` read as a field of it, plus an
		// E038 per argument. Native scopes the lookup (#6118). This is why the
		// build gate could not enforce E043 (#7380), so the row is the shape the
		// gate rests on, not a spelling of the rule.
		{"type-param-shadowed-by-struct-ok", "trait Ord { function cmp(self: Self, other: Self): i32; }\nstruct T { z: i32 }\nstruct Q { n: i32 }\nimpl Ord for Q { function cmp(self: Q, other: Q): i32 { return self.n - other.n; } }\nfunction smaller[T: Ord](a: T, b: T): T { if (b.cmp(a) < 0) { return b; } return a; }\nfunction main(): i32 { let x: Q = Q { n: 1 }; let y: Q = Q { n: 2 }; let t: T = T { z: 0 }; return smaller(x, y).n + t.z; }\n", nil},
		{"bound-multi-missing", "trait A { function fa(self: Self): i32; }\ntrait B { function fb(self: Self): i32; }\nstruct S { v: i32 }\nimpl A for S { function fa(self: Self): i32 { return self.v; } }\nfunction need[T: A + B](x: T): T { return x; }\nfunction main(): i32 { let s: S = S { v: 1 }; let r: S = need(s); return r.v; }\n", []string{"E021"}},
		// E021 (#7221): a trait requirement with no leading `self` is an
		// ASSOCIATED function — `T.show()` — and calling it through a VALUE
		// of the bounded type parameter is a user error. The self-host said
		// nothing, so `-check` passed a program the compiler then refused at
		// codegen with the IR verifier's internal-invariant message. Both
		// forms: the zero-parameter one, and the one with a parameter (where
		// the receiver made the call one argument wider than the callee).
		{"tp-assoc-fn-as-method", "trait Show { function show(): i32; }\nstruct P { v: i32 }\nimpl Show for P { function show(): i32 { return 5; } }\nfunction pick[T: Show](a: T): i32 { return a.show(); }\nfunction main(): i32 { return pick(P { v: 42 }); }\n", []string{"E021"}},
		{"tp-assoc-fn-as-method-with-arg", "trait Show { function show(x: i32): i32; }\nstruct P { v: i32 }\nimpl Show for P { function show(x: i32): i32 { return x + 1; } }\nfunction pick[T: Show](a: T): i32 { return a.show(1); }\nfunction main(): i32 { return pick(P { v: 41 }); }\n", []string{"E021"}},
		// The two shapes the rejection must not widen into: an ordinary trait
		// METHOD reached through a value, and an associated function reached
		// the way it is meant to be, through the type parameter.
		{"tp-trait-method-on-value-ok", "trait Show { function show(self: Self): i32; }\nstruct P { v: i32 }\nimpl Show for P { function show(self: Self): i32 { return self.v; } }\nfunction pick[T: Show](a: T): i32 { return a.show(); }\nfunction main(): i32 { return pick(P { v: 42 }); }\n", nil},
		{"tp-assoc-fn-on-type-param-ok", "trait Zero { function zero(): Self; }\nstruct P { v: i32 }\nimpl Zero for P { function zero(): Self { return P { v: 7 }; } }\nfunction mk[T: Zero](): i32 { let z: T = T.zero(); return 1; }\nfunction main(): i32 { return 42; }\n", nil},
		// E021 (#7187): a method NO bound on the type parameter provides. The
		// self-host said nothing, so `-check` passed a program whose call then
		// resolved against the first same-named function in the module —
		// `impl Key for i32` ran with a string box as its receiver. The
		// receiver kinds native types as the parameter: the parameter itself,
		// the result of a call through a fn-typed parameter returning it, a
		// local annotated with it, and a local initialised from it; plus an
		// unbounded parameter, where nothing can provide the method.
		{"tp-method-unbound", "trait Key { function k_id(self: Self): i32; }\nimpl Key for i32 { function k_id(self: Self): i32 { return self; } }\nfunction direct[K: Key](k: K): i32 { return k.no_such_method(); }\nfunction main(): i32 { return direct(3); }\n", []string{"E021"}},
		{"tp-method-unbound-fn-param-result", "trait Key { function k_id(self: Self): i32; }\nimpl Key for i32 { function k_id(self: Self): i32 { return self; } }\nimpl Key for string { function k_id(self: Self): i32 { return self.len(); } }\nfunction keyed_sum[T, K: Key](xs: T[], key: (T) => K): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < xs.len()) { acc = acc + key(xs[i]).no_such_method(); i = i + 1; } return acc; }\nstruct Row { n: i32, name: string }\nfunction main(): i32 { let rows: Row[] = [Row { n: 7, name: \"abcd\" }]; return keyed_sum(rows, (r: Row): string => r.name); }\n", []string{"E021"}},
		{"tp-method-unbound-annotated-local", "trait Key { function k_id(self: Self): i32; }\nimpl Key for i32 { function k_id(self: Self): i32 { return self; } }\nfunction direct[K: Key](k: K): i32 { let kv: K = k; return kv.no_such_method(); }\nfunction main(): i32 { return direct(3); }\n", []string{"E021"}},
		{"tp-method-unbound-inferred-local", "trait Key { function k_id(self: Self): i32; }\nimpl Key for i32 { function k_id(self: Self): i32 { return self; } }\nfunction direct[K: Key](k: K): i32 { let kv = k; return kv.no_such_method(); }\nfunction main(): i32 { return direct(3); }\n", []string{"E021"}},
		{"tp-method-unbounded-param", "function unbounded[K](k: K): i32 { return k.no_such_method(); }\nfunction main(): i32 { return unbounded(3); }\n", []string{"E021"}},
		// The accepting side: a method a bound provides through a supertrait,
		// through a default body, through the second of two `+` bounds, and on
		// the result of a call through a fn-typed parameter.
		{"tp-method-via-supertrait-and-default-ok", "trait A { function fa(self: Self): i32; }\ntrait B: A { function fb(self: Self): i32; function fd(self: Self): i32 { return 4; } }\nstruct S { v: i32 }\nimpl A for S { function fa(self: Self): i32 { return self.v; } }\nimpl B for S { function fb(self: Self): i32 { return self.v + 1; } }\nfunction viasuper[T: B](x: T): i32 { return x.fa() + x.fb() + x.fd(); }\nfunction main(): i32 { return viasuper(S { v: 1 }); }\n", nil},
		{"tp-method-via-second-bound-ok", "trait A { function fa(self: Self): i32; }\ntrait C { function fc(self: Self): i32; }\nstruct S { v: i32 }\nimpl A for S { function fa(self: Self): i32 { return self.v; } }\nimpl C for S { function fc(self: Self): i32 { return self.v + 2; } }\nfunction viaplus[T: A + C](x: T): i32 { let y: T = x; let z = x; return y.fc() + z.fa(); }\nfunction main(): i32 { return viaplus(S { v: 1 }); }\n", nil},
		{"tp-method-on-fn-param-result-ok", "trait C { function fc(self: Self): i32; }\nstruct S { v: i32 }\nimpl C for S { function fc(self: Self): i32 { return self.v + 2; } }\nfunction viafn[T, K: C](xs: T[], key: (T) => K): i32 { return key(xs[0]).fc(); }\nfunction main(): i32 { let ss: S[] = [S { v: 1 }]; return viafn(ss, (q: S): S => q); }\n", nil},
		// A match EXPRESSION over a tuple scrutinee that evaluates to a struct
		// (#8777): the desugar routes the arms through a value local, and a
		// struct has no literal zero to declare it with, so the local kept the
		// parser's i32 and the arm store was E003. The local is now declared by
		// annotation with the placeholder, so the shape is clean — for a
		// struct, an annotated enum destination, an array and a tuple.
		{"value-local-struct-ok", "struct P { x: i32 }\nfunction main(): i32 { let t: (i32, i32) = (7, 2); let p: P = match (t) { (a, b) => P { x: a } }; return p.x - 7; }\n", nil},
		{"value-local-enum-dest-ok", "function main(): i32 { let t: (i32, i32) = (7, 2); let o: Option[i32] = match (t) { (a, b) => Some(a + b) }; match (o) { Some(v) => { return v - 9; }, None => { return 5; } } }\n", nil},
		{"value-local-array-ok", "function main(): i32 { let t: (i32, i32) = (7, 2); let xs: i32[] = match (t) { (a, b) => [a, b, 1] }; return xs.len() - 3; }\n", nil},
		{"value-local-tuple-ok", "function main(): i32 { let t: (i32, i32) = (7, 2); let u: (i32, i32) = match (t) { (a, b) => (b, a) }; return u.0 - 2; }\n", nil},
		// The same locals inside a lambda the programmer wrote: the value
		// local's type is read off the arm stores, which name the lambda's
		// own parameter and locals, so the pass has to enter the body in the
		// lambda's scope — the enclosing one resolves none of them.
		{"value-local-in-lambda-ok", "struct P { x: i32 }\nfunction main(): i32 { let f = (k: i32): i32 => { let t: (i32, i32) = (k, 2); let p: P = match (t) { (a, b) => P { x: a } }; let o: Option[i32] = match (t) { (a, b) => Some(a + b) }; match (o) { Some(v) => { return p.x + v - k - 9; }, None => { return 5; } } }; return f(7); }\n", nil},
		// E021 object-safety (#4347 slice 4): a `dyn T` param whose trait T is not
		// object-safe draws E021 — T has an associated function (no self) or a
		// Self-returning method, neither of which can dispatch through a dyn
		// vtable. An object-safe trait (all methods take self, non-Self return)
		// is fine as `dyn T`.
		// Object safety (docs/DYN-TRAITS.md §3), one row per condition so each
		// is pinned by a program that isolates it. The row this replaced —
		// `trait T { function make(): Self; }` — had NO receiver AND returned
		// Self, so it was green whichever rule fired and could not see the two
		// checkers disagreeing about associated functions for as long as they
		// did (#7264).
		{"dyn-unsafe-self-return", "trait T { function m(self: Self): Self; }\nfunction f(x: dyn T): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"dyn-unsafe-self-param", "trait T { function m(self: Self, other: Self): i32; }\nfunction f(x: dyn T): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		// `Self` NESTED rather than whole: rules 1 and 2 are stated over `Self`
		// appearing anywhere in the type. A whole-type string compare passes
		// the two rows above and misses both of these.
		{"dyn-unsafe-self-array-return", "trait T { function m(self: Self): Self[]; }\nfunction f(x: dyn T): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"dyn-unsafe-self-array-param", "trait T { function m(self: Self, xs: Self[]): i32; }\nfunction f(x: dyn T): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		// An associated function is NOT a reason on its own: it takes no vtable
		// slot, and calling one through a dyn value is separately E021 (#7398).
		// This is the row the old corpus could not express, and the one that
		// fails if the self-host goes back to rejecting the trait outright.
		{"dyn-assoc-fn-alone-is-safe", "trait T { function make(): i32;\n  function m(self: Self): i32; }\nfunction f(x: dyn T): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		// The plainly object-safe shape, so a rule that rejects everything is
		// not mistaken for a rule that works.
		{"dyn-object-safe-plain", "trait T { function m(self: Self): i32; }\nfunction f(x: dyn T): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		// Every row above annotates a PARAMETER, which is why the gap #7974
		// names survived: E021 is owed at three positions and only one of them
		// was pinned. A `dyn T` LOCAL was silent where native draws E021, and a
		// `dyn T` RETURN worked but had nothing holding it there.
		{"dyn-unsafe-local", "trait T { function m(self: Self, other: Self): i32; }\nstruct S { v: i32 }\nimpl T for S { function m(self: Self, other: Self): i32 { return other.v; } }\nfunction main(): i32 { let d: dyn T = S { v: 3 }; return 0; }\n", []string{"E021"}},
		{"dyn-unsafe-return", "trait T { function m(self: Self, other: Self): i32; }\nstruct S { v: i32 }\nimpl T for S { function m(self: Self, other: Self): i32 { return other.v; } }\nfunction mk(): dyn T { return S { v: 3 }; }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		// The local walk descends into nested blocks, so an annotation inside an
		// `if` is a site too — a body-level-only scan passes the row above and
		// misses this one.
		{"dyn-unsafe-local-nested", "trait T { function m(self: Self, other: Self): i32; }\nstruct S { v: i32 }\nimpl T for S { function m(self: Self, other: Self): i32 { return other.v; } }\nfunction main(): i32 { if (true) { let d: dyn T = S { v: 3 }; return 0; } return 1; }\n", []string{"E021"}},
		// The negative for that position: an object-safe trait in the same local
		// slot stays clean, so the new site is not just "any dyn local errors".
		{"dyn-safe-local", "trait T { function m(self: Self, other: i32): i32; }\nstruct S { v: i32 }\nimpl T for S { function m(self: Self, other: i32): i32 { return self.v + other; } }\nfunction main(): i32 { let d: dyn T = S { v: 3 }; return 0; }\n", nil},
		// The position native deliberately does NOT flag. It is here so widening
		// the scan cannot quietly widen it past parity: a `dyn T` STRUCT FIELD
		// draws nothing from either checker, object-unsafe trait or not.
		{"dyn-unsafe-struct-field-not-flagged", "trait T { function m(self: Self, other: Self): i32; }\nstruct S { v: i32 }\nimpl T for S { function m(self: Self, other: Self): i32 { return other.v; } }\nstruct Holder { d: dyn T }\nfunction main(): i32 { return 0; }\n", nil},
		// E060 (#4347): `d as? T` on a dyn-annotated local — T must be a
		// declared struct/enum implementing every trait in the dyn set. A
		// primitive target draws the target-shape arm; a declared struct
		// missing the impl draws the not-implementing arm; a correct
		// downcast is clean from both checkers.
		{"as-downcast-nonimpl-target", "trait Shape { function area(self: Self): i32; }\nstruct Circle { r: i32 }\nstruct Square { s: i32 }\nimpl Shape for Circle { function area(self: Self): i32 { return self.r; } }\nfunction main(): i32 {\n    let d: dyn Shape = Circle { r: 3 };\n    match (d as? Square) { Some(sq) => { return sq.s; }, None => { return 0; } }\n}\n", []string{"E060"}},
		{"as-downcast-prim-target", "trait Shape { function area(self: Self): i32; }\nstruct Circle { r: i32 }\nimpl Shape for Circle { function area(self: Self): i32 { return self.r; } }\nfunction main(): i32 {\n    let d: dyn Shape = Circle { r: 3 };\n    match (d as? i32) { Some(x) => { return x; }, None => { return 0; } }\n}\n", []string{"E060"}},
		{"as-downcast-impl-ok", "trait Shape { function area(self: Self): i32; }\nstruct Circle { r: i32 }\nimpl Shape for Circle { function area(self: Self): i32 { return self.r; } }\nfunction main(): i32 {\n    let d: dyn Shape = Circle { r: 3 };\n    match (d as? Circle) { Some(c) => { return c.r; }, None => { return 0; } }\n}\n", nil},
		// E062 (#4347): `d.m()` on `dyn A + B` where BOTH traits declare m is
		// ambiguous. E006 is no longer included: two DIFFERENT traits each
		// providing `m` for S is a legitimate pair of providers since #6931,
		// not a redeclaration, on both checkers. What is ambiguous is the
		// dyn CALL, which is what E062 says. Distinct method names dispatch
		// cleanly.
		{"dyn-ambiguous-method", "trait A { function m(self: Self): i32; }\ntrait B { function m(self: Self): i32; }\nstruct S { v: i32 }\nimpl A for S { function m(self: Self): i32 { return self.v; } }\nimpl B for S { function m(self: Self): i32 { return 7; } }\nfunction main(): i32 {\n    let d: dyn A + B = S { v: 3 };\n    return d.m();\n}\n", []string{"E062"}},
		{"dyn-multi-trait-dispatch-ok", "trait A { function m(self: Self): i32; }\ntrait B { function n(self: Self): i32; }\nstruct S { v: i32 }\nimpl A for S { function m(self: Self): i32 { return self.v; } }\nimpl B for S { function n(self: Self): i32 { return 7; } }\nfunction main(): i32 {\n    let d: dyn A + B = S { v: 3 };\n    return d.m() + d.n();\n}\n", nil},
		// E021 (#7398): an ASSOCIATED function — a trait requirement with no
		// `self` receiver — reached through a `dyn` value. Nothing can
		// dispatch it, so both checkers must reject the CALL. Native accepted
		// it and left the backends to fail with a positionless internal `ir:`
		// message; the self-host emitted a dispatch that answered with the
		// wrong number. The `self`-taking twin must stay clean, so the rule
		// cannot widen into ordinary dyn dispatch.
		{"dyn-assoc-fn-call", "struct Box { v: i32 }\ntrait Mk { function make(own b: Box): i32; }\nstruct P { v: i32 }\nimpl Mk for P { function make(own b: Box): i32 { return b.v; } }\nfunction main(): i32 {\n    let d: dyn Mk = P { v: 0 };\n    return d.make(Box { v: 3 });\n}\n", []string{"E021"}},
		{"dyn-self-method-call-ok", "struct Box { v: i32 }\ntrait Mk { function make(self: Self, b: Box): i32; }\nstruct P { v: i32 }\nimpl Mk for P { function make(self: Self, b: Box): i32 { return b.v; } }\nfunction main(): i32 {\n    let d: dyn Mk = P { v: 0 };\n    return d.make(Box { v: 3 });\n}\n", nil},
		// The other side of that relaxation: the SAME trait implemented twice
		// for one type is still a redeclaration, not a second provider, so
		// E006 must keep firing. (Two inherent declarations are covered by
		// "method-redeclared" below.)
		{"same-trait-twice-redeclared", "trait A { function m(self: Self): i32; }\nstruct S { v: i32 }\nimpl A for S { function m(self: Self): i32 { return self.v; } }\nimpl A for S { function m(self: Self): i32 { return 7; } }\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		// An impl on `str` is the impl on `string`: a method is keyed by the
		// erased receiver, so the pair redeclares it, and a `string` coerces
		// to a dyn through the `str` impl. A `str` itself assigns only to a
		// `str`, so it boxes into a dyn at no site (#10908).
		{"str-and-string-impl-redeclared", "trait Size { function size(self: Self): i32; }\nimpl Size for str { function size(self: str): i32 { return self.len(); } }\nimpl Size for string { function size(self: string): i32 { return 1; } }\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		{"string-into-dyn-through-str-impl", "trait Size { function size(self: Self): i32; }\nimpl Size for str { function size(self: str): i32 { return self.len(); } }\nfunction main(): i32 { let s: string = \"ab\"; let d: dyn Size = s; let e: dyn Size = \"x\"; return d.size() + e.size(); }\n", nil},
		{"str-into-dyn-var", "trait Size { function size(self: Self): i32; }\nimpl Size for str { function size(self: str): i32 { return self.len(); } }\nfunction main(): i32 { let s: string = \"abc\"; let v: str = slice_unchecked(s, 0, 2); let d: dyn Size = v; return d.size(); }\n", []string{"E003"}},
		{"str-into-dyn-assign", "trait Size { function size(self: Self): i32; }\nimpl Size for str { function size(self: str): i32 { return self.len(); } }\nfunction main(): i32 { let s: string = \"abc\"; let v: str = slice_unchecked(s, 0, 2); let d: dyn Size = s; d = v; return d.size(); }\n", []string{"E003"}},
		{"str-into-dyn-argument", "trait Size { function size(self: Self): i32; }\nimpl Size for string { function size(self: string): i32 { return self.len(); } }\nfunction take(d: dyn Size): i32 { return d.size(); }\nfunction main(): i32 { let s: string = \"abc\"; let v: str = slice_unchecked(s, 0, 2); return take(v); }\n", []string{"E038"}},
		{"str-into-dyn-return", "trait Size { function size(self: Self): i32; }\nimpl Size for str { function size(self: str): i32 { return self.len(); } }\nfunction box(v: str): dyn Size { return v; }\nfunction main(): i32 { return box(\"ab\").size(); }\n", []string{"E002"}},
		{"str-into-dyn-array-element", "trait Size { function size(self: Self): i32; }\nimpl Size for str { function size(self: str): i32 { return self.len(); } }\nfunction main(): i32 { let s: string = \"abc\"; let v: str = slice_unchecked(s, 0, 2); let ds: dyn Size[] = [s, v]; return ds.len(); }\n", []string{"E034"}},
		{"str-into-dyn-field", "trait Size { function size(self: Self): i32; }\nimpl Size for str { function size(self: str): i32 { return self.len(); } }\nstruct H { d: dyn Size }\nfunction main(): i32 { let s: string = \"abc\"; let v: str = slice_unchecked(s, 0, 2); let h: H = H { d: v }; return h.d.size(); }\n", []string{"E043"}},
		{"str-into-dyn-payload", "trait Size { function size(self: Self): i32; }\nimpl Size for str { function size(self: str): i32 { return self.len(); } }\nenum W { One(dyn Size), Zero }\nfunction main(): i32 { let s: string = \"abc\"; let v: str = slice_unchecked(s, 0, 2); let w: W = One(v); return 0; }\n", []string{"E036"}},
		{"array-view-into-dyn-var", "trait Size { function size(self: Self): i32; }\nimpl Size for i32 { function size(self: i32): i32 { return self; } }\nfunction boxit(v: [i32]): i32 { let d: dyn Size = v; return d.size(); }\nfunction main(): i32 { let xs: i32[] = [1, 2]; return boxit(xs); }\n", []string{"E003"}},
		{"array-view-into-dyn-assign", "trait Size { function size(self: Self): i32; }\nimpl Size for i32 { function size(self: i32): i32 { return self; } }\nfunction boxit(v: [i32]): i32 { let d: dyn Size = 1; d = v; return d.size(); }\nfunction main(): i32 { let xs: i32[] = [1, 2]; return boxit(xs); }\n", []string{"E003"}},
		{"array-view-into-dyn-argument", "trait Size { function size(self: Self): i32; }\nimpl Size for i32 { function size(self: i32): i32 { return self; } }\nfunction take(d: dyn Size): i32 { return d.size(); }\nfunction boxit(v: [i32]): i32 { return take(v); }\nfunction main(): i32 { let xs: i32[] = [1, 2]; return boxit(xs); }\n", []string{"E038"}},
		{"array-view-into-dyn-return", "trait Size { function size(self: Self): i32; }\nimpl Size for i32 { function size(self: i32): i32 { return self; } }\nfunction box(v: [i32]): dyn Size { return v; }\nfunction main(): i32 { let xs: i32[] = [1, 2]; return box(xs).size(); }\n", []string{"E002"}},
		{"owned-array-into-dyn-var", "trait Size { function size(self: Self): i32; }\nimpl Size for i32 { function size(self: i32): i32 { return self; } }\nfunction main(): i32 { let xs: i32[] = [1, 2]; let d: dyn Size = xs; return d.size(); }\n", []string{"E003"}},
		{"owned-array-into-dyn-assign", "trait Size { function size(self: Self): i32; }\nimpl Size for i32 { function size(self: i32): i32 { return self; } }\nfunction main(): i32 { let xs: i32[] = [1, 2]; let d: dyn Size = 1; d = xs; return d.size(); }\n", []string{"E003"}},
		{"owned-array-into-dyn-argument", "trait Size { function size(self: Self): i32; }\nimpl Size for i32 { function size(self: i32): i32 { return self; } }\nfunction take(d: dyn Size): i32 { return d.size(); }\nfunction main(): i32 { let xs: i32[] = [1, 2]; return take(xs); }\n", []string{"E038"}},
		{"owned-array-into-dyn-return", "trait Size { function size(self: Self): i32; }\nimpl Size for i32 { function size(self: i32): i32 { return self; } }\nfunction box(xs: i32[]): dyn Size { return xs; }\nfunction main(): i32 { let xs: i32[] = [1, 2]; return box(xs).size(); }\n", []string{"E002"}},
		{"impl-for-owned-array", "trait Size { function size(self: Self): i32; }\nimpl Size for i32[] { function size(self: i32[]): i32 { return self.len() as i32; } }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"impl-for-array-view", "trait Size { function size(self: Self): i32; }\nstruct P { v: i32 }\nimpl Size for [P] { function size(self: [P]): i32 { return self.len() as i32; } }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"dyn-object-safe-ok", "trait T { function m(self: Self): i32; }\nfunction f(x: dyn T): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		{"rec-local-ok", "function main(): i32 { function f(n: i32): i32 { if (n <= 0) { return 0; } return f(n - 1); } return f(3); }\n", nil},
		{"rec-local-capture-ok", "function main(): i32 { let base: i32 = 10; function f(n: i32): i32 { if (n <= 0) { return base; } return 1 + f(n - 1); } return f(3); }\n", nil},
		// Only a nested `function` sees its own name (#10383): an arrow lambda
		// bound by `let` calling that var, directly or one lambda deeper, is E001.
		{"rec-local-arrow-e001", "function main(): i32 { let f = (n: i32): i32 => { if (n <= 0) { return 0; } return f(n - 1); }; return f(3); }\n", []string{"E001"}},
		{"rec-local-arrow-nested-e001", "function main(): i32 { let f = (): i32 => { let g = (): i32 => { return f(); }; return g(); }; return f(); }\n", []string{"E001"}},
		// Nested functions calling one another around a cycle see each other
		// whatever their order (native's checkBlock pre-binds the SCC). A
		// forward reference that closes no cycle, or one between arrow
		// lambdas, stays E001.
		{"rec-local-mutual-ok", "function main(): i32 {\n  function isEven(n: i32): boolean { if (n == 0) { return true; } return isOdd(n - 1); }\n  function isOdd(n: i32): boolean { if (n == 0) { return false; } return isEven(n - 1); }\n  if (isEven(10) && !isEven(11) && isOdd(7) && !isOdd(8)) { return 0; }\n  return 1;\n}\n", nil},
		{"rec-local-three-way-cycle-ok", "function main(): i32 {\n  function a(n: i32): i32 { if (n <= 0) { return 0; } return 1 + b(n - 1); }\n  function b(n: i32): i32 { if (n <= 0) { return 0; } return 2 + c(n - 1); }\n  function c(n: i32): i32 { if (n <= 0) { return 0; } return 3 + a(n - 1); }\n  return a(6);\n}\n", nil},
		{"rec-local-mutual-capture-ok", "function main(): i32 {\n  let step: i32 = 1;\n  function down(n: i32): i32 { if (n <= 0) { return 0; } return up(n - step) + 1; }\n  function up(n: i32): i32 { if (n <= 0) { return 0; } return down(n - step) + step; }\n  return down(5);\n}\n", nil},
		{"rec-local-mutual-in-nested-ok", "function main(): i32 {\n  function outer(x: i32): i32 {\n    function ping(n: i32): i32 { if (n <= 0) { return x; } return pong(n - 1) + 1; }\n    function pong(n: i32): i32 { if (n <= 0) { return 0; } return ping(n - 1) + 2; }\n    return ping(4);\n  }\n  return outer(30);\n}\n", nil},
		{"rec-local-forward-no-cycle-e001", "function main(): i32 {\n  function first(n: i32): i32 { return second(n) + 1; }\n  function second(n: i32): i32 { return n * 2; }\n  return first(3);\n}\n", []string{"E001"}},
		{"rec-local-arrow-mutual-e001", "function main(): i32 { let f = (n: i32): i32 => { return g(n); }; let g = (n: i32): i32 => { return f(n); }; return 0; }\n", []string{"E001"}},
		// Range-for `for i in LOW..HIGH` (#2699 self-host IR slice): the loop
		// var is an i32 over the half-open interval. A clean program draws no
		// codes from EITHER checker — the differential proves the self-host
		// checker binds the range var to i32 (no spurious E001 / E008) the
		// same way the Go checker does (it desugars to a C-style for).
		{"range-clean", "function main(): i32 { let s = 0; for i in 0..5 { s = s + i; } return s; }\n", nil},
		{"range-nested-clean", "function main(): i32 { let t = 0; for i in 0..3 { for j in 0..3 { t = t + i + j; } } return t; }\n", nil},
		{"range-expr-bounds-clean", "function main(): i32 { let n = 4; let s = 0; for i in 1..n + 1 { s = s + i; } return s; }\n", nil},
		// Type ascription `e as T` (#2669): a zero-cost annotation. An array /
		// string ascription draws no diagnostic from EITHER checker — the
		// self-host only flags E033 when both sides are scalar primitives, and
		// the Go checker accepts the cast as an upcast assignable to the target.
		{"asc-arr-clean", "function main(): i32 { let a = [] as i32[]; a = [1, 2]; return a[0] + a[1]; }\n", nil},
		{"asc-str-clean", "function main(): i32 { let s = \"x\" as string; return s.len(); }\n", nil},
		// Non-binding-position ascription (#2669) — arg / return / nested — is
		// also clean from both checkers.
		{"asc-arg-clean", "function id(a: i32[]): i32 { return a.len(); }\nfunction main(): i32 { let a = [1, 2]; return id(a as i32[]); }\n", nil},
		{"asc-ret-clean", "function mk(): i32[] { let a = [1, 2]; return a as i32[]; }\nfunction main(): i32 { return mk()[0]; }\n", nil},
		// break / continue inside `for` loops (#2788) — clean from both checkers.
		{"for-continue-clean", "function main(): i32 { let s = 0; for i in 0..5 { if (i == 2) { continue; } s = s + i; } return s; }\n", nil},
		{"for-break-clean", "function main(): i32 { let a = [1, 2, 3]; let s = 0; for x in a { if (x == 3) { break; } s = s + x; } return s; }\n", nil},
		// E058 (labeled break/continue names no enclosing loop, #2857): a
		// labeled `break L` / `continue L` whose `L` matches no enclosing loop
		// label is E058. A valid label (the enclosing loop's, or an outer one
		// from a nested loop) draws no code — matching the Go checker, which
		// tracks the enclosing-loop label stack.
		{"break-bad-label", "function main(): i32 { let c = 0; outer: while (c < 5) { c = c + 1; if (c == 2) { break nope; } } return c; }\n", []string{"E058"}},
		{"continue-bad-label", "function main(): i32 { let c = 0; outer: while (c < 5) { c = c + 1; if (c == 2) { continue nope; } } return c; }\n", []string{"E058"}},
		{"break-good-label-clean", "function main(): i32 { let c = 0; outer: while (c < 5) { c = c + 1; let j = 0; while (j < 5) { j = j + 1; if (j == 2) { break outer; } } } return c; }\n", nil},
		{"continue-good-label-clean", "function main(): i32 { let c = 0; outer: while (c < 3) { c = c + 1; let j = 0; while (j < 3) { j = j + 1; if (j == 1) { continue outer; } } } return c; }\n", nil},
		// A labeled break that targets the INNERMOST loop's own label is in
		// scope (depth 0) — clean.
		{"break-self-label-clean", "function main(): i32 { let c = 0; inner: while (c < 5) { c = c + 1; if (c == 2) { break inner; } } return c; }\n", nil},
		// E011 still wins for an out-of-loop labeled break (the two never both
		// fire) — a labeled break with no enclosing loop at all is E011.
		{"labeled-break-no-loop", "function main(): i32 { break nope; return 0; }\n", []string{"E011"}},
		// E061 (value-position block has no trailing value, #2857): an if/match
		// used as a value whose branch ends in a `;`-terminated statement (no
		// tail expression) has no result. parse_branch_body tags that branch
		// with a marker the checker turns into E061 — matching the Go checker.
		// Both branches value-less (so they agree as void → no E031) and an
		// un-annotated var (→ no E003) isolate E061 as the only code.
		{"if-branch-no-tail", "function f(): i32 { return 0; }\nfunction main(): i32 { let x = if (true) { f(); } else { f(); }; return 0; }\n", []string{"E061"}},
		{"match-arm-no-tail", "function f(): i32 { return 0; }\nfunction main(): i32 { let a = match (1) { 1 => { f(); }, _ => { f(); } }; return 0; }\n", []string{"E061"}},
		// A value if/match WITH trailing values (incl. leading statements before
		// the tail) draws no E061 from either checker.
		{"if-branch-tail-ok", "function main(): i32 { let x = if (true) { 1 } else { 2 }; return x; }\n", nil},
		{"if-branch-leading-then-tail-ok", "function main(): i32 { let x = if (true) { let k = 1; k + 1 } else { 0 }; return x; }\n", nil},
		// E059 (`as?` downcast requires a `dyn Trait` value on the left, #2857):
		// the operand of `x as? T` must be a `dyn Trait` value. A concrete
		// scalar (i32 / string) on the left can never be dyn, so it's E059 —
		// matching the Go checker. (A real dyn value types to `unknown` in the
		// self-host and is left alone, like the E033/E042 conservatism.) The
		// regular `as` cast is unaffected (it's the `as_` op, not `as?_`).
		{"as-downcast-i32-left", "struct P { x: i32 }\nfunction main(): i32 { let a = 5; let b = a as? P; return 0; }\n", []string{"E059"}},
		{"as-downcast-string-left", "struct P { x: i32 }\nfunction main(): i32 { let s = \"hi\"; let b = s as? P; return 0; }\n", []string{"E059"}},
		{"regular-as-cast-ok", "function main(): i32 { let a = 5; let b = a as i32; return b; }\n", nil},
		// `for x in <EXPR>` over a non-ident array iterable — clean from both checkers.
		{"for-literal-clean", "function main(): i32 { let s = 0; for x in [1, 2, 3] { s = s + x; } return s; }\n", nil},
		{"for-call-clean", "function mk(): i32[] { return [1, 2]; }\nfunction main(): i32 { let s = 0; for x in mk() { s = s + x; } return s; }\n", nil},
		// `for x in <EXPR>` over a value no loop iterates: native lowers it to
		// `.len()` + index and reports both at the loop.
		{"for-over-struct", "enum Ty { S(i32), N(i32) }\nstruct Item { ty: Ty }\nstruct Box { list: Item[], k: i32 }\nfunction main(): i32 {\n  let b: Box = Box { list: [Item { ty: Ty.S(1) }], k: 1 };\n  let names: string[] = [];\n  for it in b {\n    if let Ty.S(v) = it.ty {\n      names = names.append(\"x\");\n    }\n  }\n  return names.len();\n}\n", []string{"E034", "E043"}},
		{"for-over-i32", "function main(): i32 { let n: i32 = 3; let s: i32 = 0; for x in n { s = s + x; } return s; }\n", []string{"E034", "E043"}},
		// Unannotated struct-array literal (`let ps = [P{..}, ..]`) — element type
		// inferred, clean from both checkers.
		{"inferred-struct-array-clean", "struct P { v: i32 }\nfunction main(): i32 { let ps = [P { v: 3 }, P { v: 4 }]; return ps[0].v + ps[1].v; }\n", nil},
		// Tuple literal with an i32[] element — clean from both checkers.
		{"tuple-arr-elem-clean", "function main(): i32 { let t = ([10, 20], 9); let a = t.0; return a[0] + t.1; }\n", nil},
		// A tuple whose ANNOTATION names an element type this resolver cannot
		// resolve — `Cell[T]` is one — must not be rejected for that. The
		// element goes unknown here, and an unknown destination element is a
		// wildcard exactly as it is in the array arm beside it: a bare
		// `let c: Cell[i32] = cell_new(0)` skipped the check entirely while
		// the same element inside a tuple used to draw E003.
		{"tuple-cell-elem-clean", "function main(): i32 { let t: (i32, Cell[i32]) = (7, cell_new(0)); return t.0 - 7; }\n", nil},
		{"tuple-cell-string-elem-clean", "function main(): i32 { let t: (i32, Cell[string]) = (7, cell_new(\"a\")); return t.0 - 7; }\n", nil},
		{"dup-field", "struct P { x: i32, x: i32 }\nfunction main(): i32 { return 0; }\n", []string{"E007"}},
		{"dup-param", "function f(a: i32, a: i32): i32 { return a; }\nfunction main(): i32 { return 0; }\n", []string{"E018"}},
		{"dup-field-and-param", "struct P { y: i32, y: i32 }\nfunction g(b: i32, b: i32): i32 { return b; }\nfunction main(): i32 { return 0; }\n", []string{"E007", "E018"}},
		{"clean-struct-and-func", "struct Q { a: i32, b: string }\nfunction h(x: i32, y: i32): i32 { return x + y; }\nfunction main(): i32 { return 0; }\n", nil},
		{"func-redeclared", "function f(): i32 { return 1; }\nfunction f(): i32 { return 2; }\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		{"method-redeclared", "struct P { x: i32 }\nfunction (p: P) m(): i32 { return 1; }\nfunction (p: P) m(): i32 { return 2; }\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		// Array and view receivers are two namespaces, each shared by every
		// element type, as native keys them.
		{"view-and-array-method-same-name-ok", "function (xs: [u8]) m(): u8 { return xs[0]; }\nfunction (xs: u8[]) m(): u8 { return xs[0]; }\nfunction main(): i32 { return 0; }\n", nil},
		{"view-method-redeclared-across-elements", "function (xs: [u8]) m(): u8 { return xs[0]; }\nfunction (xs: [i32]) m(): i32 { return xs[0]; }\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		{"array-method-redeclared-across-elements", "function (xs: u8[]) m(): u8 { return xs[0]; }\nfunction (xs: i32[]) m(): i32 { return xs[0]; }\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		{"free-and-method-same-name-ok", "struct P { x: i32 }\nfunction m(): i32 { return 1; }\nfunction (p: P) m(): i32 { return 2; }\nfunction main(): i32 { return 0; }\n", nil},
		{"return-mismatch", "function main(): i32 { let s: string = \"x\"; return s; }\n", []string{"E002"}},
		// A written `return` inside a value block leaves the ENCLOSING function,
		// so it is held to that function's type; only the tail of each arm is
		// the block's value (#9828). The self-host checked every return in the
		// block against the block's own tag, so the loop's return of a lambda
		// went unreported and the binary returned a truncated box pointer.
		{"return-inside-value-block", "function main(): i32 {\n    let k: i32 = 3;\n    let xs: i32[] = [1, 2, 3];\n    let f: (i32) => i32 = (if (k > 1) { for x in xs { if (x == 2) { return ((a: i32) => a + k + x); } } ((b: i32) => b) } else { ((y: i32) => y - k) });\n    return f(1);\n}\n", []string{"E002"}},
		{"return-inside-value-block-clean", "function pick(c: boolean): i32 {\n    let v: i32 = (if (c) { let i: i32 = 0; while (i < 5) { i = i + 1; if (i == 3) { return 40; } } 1 } else { 2 });\n    return v + 100;\n}\nfunction main(): i32 { return pick(true); }\n", nil},
		{"return-inside-match-expression-arm", "function f(n: i32): i32 {\n    let s: string = (match (n) { 0 => { \"zero\" }, _ => { if (n < 0) { return \"neg\"; } \"pos\" } });\n    return s.len();\n}\nfunction main(): i32 { return f(1); }\n", []string{"E002"}},
		{"return-inside-value-block-of-void", "function g(c: boolean): void {\n    let v: i32 = (if (c) { return 3; 1 } else { 2 });\n}\nfunction main(): i32 { g(true); return 0; }\n", []string{"E002"}},
		{"return-inside-nested-value-block", "function f(c: boolean, d: boolean): i32 {\n    let v: i32 = (if (c) { let w: i32 = (if (d) { return \"x\"; 1 } else { 2 }); w } else { 3 });\n    return v;\n}\nfunction main(): i32 { return f(true, true); }\n", []string{"E002"}},
		// A value block in call-argument position is reached because the walk
		// descends through an ordinary call (not_a_scope_root prunes only at a
		// lambda and at a value block it has already taken).
		{"return-inside-value-block-call-argument", "function g(n: i32): i32 { return n + 1; }\nfunction f(c: boolean): i32 {\n    return g((if (c) { return \"x\"; 1 } else { 2 }));\n}\nfunction main(): i32 { return f(true); }\n", []string{"E002"}},
		// An else-if chain's arms are the tail of the else body, so their own
		// tails stay the block's value while a written return in one is checked.
		{"return-inside-else-if-arm", "function f(c: boolean, d: boolean): i32 {\n    let v: i32 = (if (c) { 1 } else if (d) { return \"x\"; 2 } else { 3 });\n    return v;\n}\nfunction main(): i32 { return f(false, true); }\n", []string{"E002"}},
		{"return-inside-else-if-arm-clean", "function f(c: boolean, d: boolean): i32 {\n    let v: i32 = (if (c) { 1 } else if (d) { return 40; 2 } else { 3 });\n    return v + 100;\n}\nfunction main(): i32 { return f(false, true); }\n", nil},
		{"return-mismatch-nested", "function f(): i32 { if (true) { return \"no\"; } return 1; }\nfunction main(): i32 { return 0; }\n", []string{"E002"}},
		{"return-ok", "function f(): string { let s: string = \"x\"; return s; }\nfunction main(): i32 { return 0; }\n", nil},
		{"struct-missing-field", "struct P { x: i32, y: i32 }\nfunction main(): i32 { let p: P = P { x: 1 }; return p.x; }\n", []string{"E005"}},
		{"struct-nested-missing", "struct Q { a: i32 }\nstruct P { q: Q }\nfunction main(): i32 { let p: P = P { q: Q {} }; return 0; }\n", []string{"E005"}},
		// E043 (unknown-field-in-literal): a struct literal naming a field the
		// struct doesn't declare (incl. update literals). All-declared is clean.
		{"struct-extra-field", "struct P { x: i32 }\nfunction main(): i32 { let p = P { x: 1, y: 2 }; return p.x; }\n", []string{"E043"}},
		{"struct-extra-update", "struct P { x: i32, y: i32 }\nfunction f(p: P): P { return P { ...p, z: 9 }; }\nfunction main(): i32 { return 0; }\n", []string{"E043"}},
		{"struct-all-fields-ok", "struct P { x: i32, y: i32 }\nfunction main(): i32 { let p = P { x: 1, y: 2 }; return p.x + p.y; }\n", nil},
		{"struct-complete-ok", "struct P { x: i32, y: i32 }\nfunction main(): i32 { let p: P = P { x: 1, y: 2 }; return p.x; }\n", nil},
		{"struct-field-type-mismatch", "struct P { x: i32, y: i32 }\nfunction main(): i32 { let p: P = P { x: 1, y: \"no\" }; return 0; }\n", []string{"E043"}},
		{"struct-field-type-string-ok", "struct P { x: i32, name: string }\nfunction main(): i32 { let p: P = P { x: 1, name: \"hi\" }; return p.x; }\n", nil},
		{"struct-field-array-mismatch", "struct P { xs: i32[] }\nfunction main(): i32 { let p: P = P { xs: 5 }; return 0; }\n", []string{"E043"}},
		{"struct-field-array-ok", "struct P { xs: i32[] }\nfunction main(): i32 { let p: P = P { xs: [1, 2, 3] }; return 0; }\n", nil},
		// A struct literal may write the declared fields in ANY order (#9036).
		// The literal has to type as the struct either way, so a later misuse
		// of the binding is still caught: an out-of-order literal that typed as
		// unknown made every downstream check vanish.
		{"struct-lit-order-swapped-ok", "struct P { a: i32, b: string }\nfunction main(): i32 { let p = P { b: \"x\", a: 1 }; return p.a; }\n", nil},
		{"struct-lit-order-swapped-downstream", "struct P { a: i32, b: string }\nfunction main(): i32 { let p = P { b: \"x\", a: 1 }; let w: i32 = p.b; return 0; }\n", []string{"E003"}},
		{"struct-lit-order-swapped-value-mismatch", "struct P { a: i32, b: string }\nfunction main(): i32 { let p = P { b: 7, a: 1 }; return p.a; }\n", []string{"E043"}},
		// A field value is judged AGAINST its declared field type (#9042).
		// NEITHER half of that is gated here, and the rows below do not
		// discriminate it:
		//
		// The accepting half bails the module with an UNCODED `error[type]`,
		// which contributes no code, so a row expecting none passes either way.
		// The refusing half is reported by call_diags_struct_lit, a separate
		// coded pass that already resolves by name and already uses the
		// settles_to / type_assignable pair — it was check_expr's TYPE result
		// that disagreed with it. So E043 flows whatever check_expr does, and
		// these rows stay green even against a blanket accept.
		//
		// TestSelfHostCLIX86_64/check-accepts-typed-field is the gate that
		// discriminates the fix, by exit status. What these rows pin is that
		// the E043 CODE is still produced for the non-member and wrong-primitive
		// shapes once the widening exists — not the widening itself.
		{"struct-lit-field-non-member-at-union", "struct Lf { v: i32 }\nstruct Tw { xs: i32[] }\ntype Node = Lf | Tw;\nstruct Holds { t: Node, n: i32 }\nstruct Other { z: i32 }\nfunction mk(n: i32): Holds { return Holds { t: Other { z: n }, n: n }; }\nfunction main(): i32 { return mk(1).n; }\n", []string{"E043"}},
		{"struct-lit-field-wrong-prim-still-refused", "struct P { a: i32, b: string }\nfunction main(): i32 { let p = P { a: 1, b: 7 }; return p.a; }\n", []string{"E043"}},
		// A method on an enum or struct-union receiver dispatches under the
		// union's name (#9031); without that the call typed as unknown and its
		// result went unchecked.
		{"enum-receiver-method-ok", "enum Shape { Dot, Line(i32) }\nfunction (s: Shape) mag(): i32 { match (s) { Dot => { return 0; }, Line(n) => { return n; } } return 0; }\nfunction main(): i32 { let s: Shape = Line(3); return s.mag(); }\n", nil},
		{"enum-receiver-method-ret-mismatch", "enum Shape { Dot, Line(i32) }\nfunction (s: Shape) mag(): i32 { match (s) { Dot => { return 0; }, Line(n) => { return n; } } return 0; }\nfunction main(): i32 { let s: Shape = Line(3); let w: string = s.mag(); return 0; }\n", []string{"E003"}},
		{"struct-union-receiver-method-ret-mismatch", "struct Circle { r: i32 }\nstruct Square { w: i32 }\ntype Shape = Circle | Square;\nfunction (s: Shape) area(): i32 { match (s) { Circle(c) => { return c.r; }, Square(q) => { return q.w; } } return 0; }\nfunction main(): i32 { let s: Shape = Circle { r: 2 }; let w: string = s.area(); return 0; }\n", []string{"E003"}},
		// E034 (typed composite-array element): an element of a `let x: Elem[]`
		// literal must be assignable to Elem. A union element type widens
		// (members ok); a non-member, a wrong struct, or a primitive is E034.
		{"typed-arr-struct-bad-prim", "struct P { x: i32 }\nfunction main(): i32 { let a: P[] = [P { x: 1 }, 5]; return 0; }\n", []string{"E034"}},
		{"typed-arr-struct-bad-struct", "struct P { x: i32 }\nstruct Q { y: i32 }\nfunction main(): i32 { let a: P[] = [P { x: 1 }, Q { y: 2 }]; return 0; }\n", []string{"E034"}},
		{"typed-arr-struct-ok", "struct P { x: i32 }\nfunction main(): i32 { let a: P[] = [P { x: 1 }, P { x: 2 }]; return 0; }\n", nil},
		{"typed-arr-union-ok", "struct P { x: i32 }\nstruct Q { y: i32 }\ntype U = P | Q;\nfunction main(): i32 { let a: U[] = [P { x: 1 }, Q { y: 2 }]; return 0; }\n", nil},
		{"typed-arr-union-bad", "struct P { x: i32 }\nstruct Q { y: i32 }\nstruct R { z: i32 }\ntype U = P | Q;\nfunction main(): i32 { let a: U[] = [P { x: 1 }, R { z: 3 }]; return 0; }\n", []string{"E034"}},
		// E034 in non-let positions: the same composite-array element check at
		// a `T[]` return, a `T[]` call argument, and a reassignment to a `T[]`
		// variable. Union element types still widen (members ok).
		{"arr-elem-return-bad", "struct P { x: i32 }\nfunction f(): P[] { return [P { x: 1 }, 5]; }\nfunction main(): i32 { return 0; }\n", []string{"E034"}},
		{"arr-elem-return-ok", "struct P { x: i32 }\nstruct Q { y: i32 }\ntype U = P | Q;\nfunction f(): U[] { return [P { x: 1 }, Q { y: 2 }]; }\nfunction main(): i32 { return 0; }\n", nil},
		{"arr-elem-arg-bad", "struct P { x: i32 }\nfunction f(a: P[]): i32 { return 0; }\nfunction main(): i32 { return f([P { x: 1 }, 5]); }\n", []string{"E034"}},
		// A float suffix names the literal's type outright (#10757): an f32
		// literal sits beside an f32 value, and against an f64 it is E034 / E003.
		{"f32-suffix-beside-f32-value", "function main(): i32 { let p: f32 = 1.0f32; let a: f32[] = [p, 1.5f32]; return a.len(); }\n", nil},
		{"f32-suffix-before-f32-value", "function main(): i32 { let p: f32 = 1.0f32; let a: f32[] = [1.5f32, p]; return a.len(); }\n", nil},
		{"f32-suffix-beside-f64-value", "function main(): i32 { let p: f64 = 1.0; let a: f64[] = [p, 1.5f32]; return a.len(); }\n", []string{"E034"}},
		{"f32-suffix-into-f64", "function main(): i32 { let x: f64 = 1.5f32; return 0; }\n", []string{"E003"}},
		{"f64-suffix-into-f32", "function main(): i32 { let x: f32 = 1.5f64; return 0; }\n", []string{"E003"}},
		{"unsuffixed-float-adapts", "function main(): i32 { let x: f32 = 1.5; let y: f64 = 2.5; return 0; }\n", nil},
		{"arr-elem-assign-bad", "struct P { x: i32 }\nfunction main(): i32 { let a: P[] = [P { x: 1 }]; a = [P { x: 1 }, 5]; return 0; }\n", []string{"E034"}},
		// E034 at a struct-literal field of composite-array type: the field
		// value's elements are checked against the field's element type (plain
		// and `...base` literals). Union fields widen; the whole-value scalar
		// mismatch stays E043.
		{"arr-elem-field-bad", "struct Q { n: i32 }\nstruct P { xs: Q[] }\nfunction main(): i32 { let p = P { xs: [Q { n: 1 }, 5] }; return 0; }\n", []string{"E034"}},
		{"arr-elem-field-update-bad", "struct Q { n: i32 }\nstruct P { xs: Q[] }\nfunction f(p: P): P { return P { ...p, xs: [Q { n: 1 }, 5] }; }\nfunction main(): i32 { return 0; }\n", []string{"E034"}},
		{"arr-elem-field-union-ok", "struct A { a: i32 }\nstruct B { b: i32 }\ntype U = A | B;\nstruct P { xs: U[] }\nfunction main(): i32 { let p = P { xs: [A { a: 1 }, B { b: 2 }] }; return 0; }\n", nil},
		// E038 (builtin array-method arg type): `.append(elem)` and the value of
		// `.with(i32, elem)` must match the array's element type. A union
		// element widens; a correctly-typed call stays clean.
		{"arr-append-arg-bad", "function main(): i32 { let a: i32[] = [1]; a = a.append(\"x\"); return 0; }\n", []string{"E038"}},
		{"arr-append-arg-ok", "function main(): i32 { let a: i32[] = [1]; a = a.append(2); return 0; }\n", nil},
		{"arr-with-arg-bad", "function main(): i32 { let a: i32[] = [1]; a = a.with(0, \"x\"); return 0; }\n", []string{"E038"}},
		{"arr-append-union-ok", "struct P { x: i32 }\nstruct Q { y: i32 }\ntype U = P | Q;\nfunction main(): i32 { let a: U[] = [P { x: 1 }]; a = a.append(Q { y: 2 }); return 0; }\n", nil},
		// E043 (method-call on a numeric scalar): i32 / f64 carry no methods,
		// so any `x.m(...)` on one is a field access on a non-struct. Valid
		// string / array / struct method calls stay clean (no false positive).
		{"method-on-i32", "function main(): i32 { let x: i32 = 3; x.foo(); return 0; }\n", []string{"E043"}},
		// A LITERAL receiver, which reaches the arm through check_expr rather
		// than through a binding's declared type. `to_string` is std/i32's and
		// the program does not import it (#7380).
		{"method-on-i32-literal-unimported", "function main(): i32 { let t: string = 7.to_string(); return t.len(); }\n", []string{"E043"}},
		{"method-on-f64", "function main(): i32 { let f: f64 = 1.0; f.foo(); return 0; }\n", []string{"E043"}},
		{"method-on-string-ok", "function main(): i32 { let s: string = \"a\"; return s.len(); }\n", nil},
		{"method-on-i32-user-method-ok", "function (n: i32) twice(): i32 { return n * 2; }\nfunction main(): i32 { let x: i32 = 21; return x.twice(); }\n", nil},
		// E043 (array method existence): only append / with / len are
		// unconditional array builtins; everything else is an auto-discovered
		// std/array function, so `a.sum()` / `a.bogus()` without `import
		// "std/array"` is a call to a non-existent method. append / with / len
		// stay clean. (The import path — where the std/array functions ARE in
		// scope — is covered by TestSelfHostCheckerBundleDifferentialX86_64.)
		{"method-on-array-sum-noimp", "function main(): i32 { let a: i32[] = [1]; return a.sum(); }\n", []string{"E043"}},
		{"method-on-array-bogus", "function main(): i32 { let a: i32[] = [1]; return a.bogus(); }\n", []string{"E043"}},
		{"method-on-array-append-ok", "function main(): i32 { let a: i32[] = []; a = a.append(1); return a.len(); }\n", nil},
		{"method-on-array-with-ok", "function main(): i32 { let a: i32[] = [1, 2]; a = a.with(0, 9); return a.len(); }\n", nil},
		// E043 (string method existence): a string carries only the `len` /
		// `as_bytes` builtins; any other method must be user-defined (here,
		// none is in scope), else it's a call to a non-existent method.
		{"method-on-string-missing", "function main(): i32 { let s: string = \"a\"; let t = s.bogus(); return 0; }\n", []string{"E043"}},
		{"method-on-string-substr-missing", "function main(): i32 { let s: string = \"abc\"; let t = s.substr(0, 1); return 0; }\n", []string{"E043"}},
		{"method-on-string-as-bytes-ok", "function main(): i32 { let s: string = \"a\"; let b = s.as_bytes(); return 0; }\n", nil},
		{"method-on-string-user-method-ok", "function (s: string) shout(): string { return s; }\nfunction main(): i32 { let s = \"a\"; let t = s.shout(); return 0; }\n", nil},
		// E043 (struct method/field both missing): `p.m()` where struct P has
		// no method m and no field m. A declared method, or a present field
		// (closure-field call), is excluded — no false positive.
		{"method-on-struct-missing", "struct P { x: i32 }\nfunction main(): i32 { let p = P { x: 1 }; return p.nope(); }\n", []string{"E043"}},
		{"method-on-struct-defined-ok", "struct P { x: i32 }\nfunction (p: P) m(): i32 { return p.x; }\nfunction main(): i32 { let p = P { x: 1 }; return p.m(); }\n", nil},
		// E038 (struct non-function field called): `p.x()` where x is a field
		// whose type isn't a function. A function-typed field (closure call)
		// stays clean; a missing member is E043 (above), not E038.
		{"call-struct-field-i32", "struct P { x: i32 }\nfunction main(): i32 { let p = P { x: 1 }; return p.x(); }\n", []string{"E038"}},
		{"call-struct-field-string", "struct P { s: string }\nfunction main(): i32 { let p = P { s: \"a\" }; p.s(); return 0; }\n", []string{"E038"}},
		{"call-too-few-args", "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1); }\n", []string{"E004"}},
		{"call-too-many-args", "function id(a: i32): i32 { return a; }\nfunction main(): i32 { return id(1, 2); }\n", []string{"E004"}},
		{"call-correct-arity-ok", "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n", nil},
		{"call-shadowed-local-ok", "function f(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { let f = (x: i32): i32 => { return x; }; return f(7); }\n", nil},
		{"method-too-few-args", "struct P { x: i32 }\nfunction (p: P) add(a: i32, b: i32): i32 { return p.x + a + b; }\nfunction main(): i32 { let p: P = P { x: 1 }; return p.add(5); }\n", []string{"E004"}},
		{"method-too-many-args", "struct P { x: i32 }\nfunction (p: P) one(a: i32): i32 { return p.x + a; }\nfunction main(): i32 { let p: P = P { x: 1 }; return p.one(5, 6); }\n", []string{"E004"}},
		{"method-correct-arity-ok", "struct P { x: i32 }\nfunction (p: P) add(a: i32, b: i32): i32 { return p.x + a + b; }\nfunction main(): i32 { let p: P = P { x: 1 }; return p.add(5, 6); }\n", nil},
		{"method-arg-type-mismatch", "struct P { x: i32 }\nfunction (p: P) add(a: i32): i32 { return p.x + a; }\nfunction main(): i32 { let p: P = P { x: 1 }; let s: string = \"n\"; return p.add(s); }\n", []string{"E038"}},
		{"method-arg-type-ok", "struct P { x: i32 }\nfunction (p: P) add(a: i32): i32 { return p.x + a; }\nfunction main(): i32 { let p: P = P { x: 1 }; return p.add(7); }\n", nil},
		{"method-arg-array-mismatch", "struct P { x: i32 }\nfunction (p: P) take(xs: string[]): i32 { return p.x; }\nfunction main(): i32 { let p: P = P { x: 1 }; let n: i32 = 5; return p.take(n); }\n", []string{"E038"}},
		{"method-arg-empty-array-ok", "struct P { x: i32 }\nfunction (p: P) take(xs: string[]): i32 { return p.x; }\nfunction main(): i32 { let p: P = P { x: 1 }; return p.take([]); }\n", nil},
		{"var-annotation-mismatch", "function main(): i32 { let x: i32 = \"no\"; return x; }\n", []string{"E003"}},
		{"assign-mismatch", "function main(): i32 { let x: i32 = 1; x = \"no\"; return x; }\n", []string{"E003"}},
		{"assign-ok", "function main(): i32 { let x: i32 = 1; x = 2; return x; }\n", nil},
		{"arg-type-mismatch", "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, \"no\"); }\n", []string{"E038"}},
		{"arg-type-ok", "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n", nil},
		// E038 (call-non-function variant): calling a value whose type isn't a
		// function. A free function / closure / fn-value local / method call is
		// fine; only a scalar / non-fn local callee is flagged.
		{"call-nonfn-i32", "function main(): i32 { let x = 5; return x(3); }\n", []string{"E038"}},
		{"call-nonfn-string", "function main(): i32 { let s = \"a\"; return s(3); }\n", []string{"E038"}},
		{"call-nonfn-noargs", "function main(): i32 { let x = 5; return x(); }\n", []string{"E038"}},
		{"call-closure-ok", "function main(): i32 { let g = (x: i32): i32 => { return x + 1; }; return g(41); }\n", nil},
		{"call-fnval-named-ok", "function dbl(n: i32): i32 { return n * 2; }\nfunction main(): i32 { let f = dbl; return f(21); }\n", nil},
		{"if-nonbool-cond", "function main(): i32 { if (5) { return 1; } return 0; }\n", []string{"E008"}},
		{"while-nonbool-cond", "function main(): i32 { while (\"x\") { return 1; } return 0; }\n", []string{"E008"}},
		{"if-bool-cond-ok", "function main(): i32 { if (1 < 2) { return 1; } return 0; }\n", nil},
		{"break-outside-loop", "function main(): i32 { break; return 0; }\n", []string{"E011"}},
		{"continue-outside-loop", "function main(): i32 { continue; return 0; }\n", []string{"E011"}},
		{"break-in-loop-ok", "function main(): i32 { while (1 < 2) { break; } return 0; }\n", nil},
		{"break-in-match-outside-loop", "enum E { A, B }\nfunction main(): i32 { let e: E = A; match (e) { A => { break; }, B => { } } return 0; }\n", []string{"E011"}},
		{"return-no-value-nonvoid", "function f(): i32 { return; }\nfunction main(): i32 { return 0; }\n", []string{"E012"}},
		{"return-no-value-void-ok", "function f(): void { return; }\nfunction main(): i32 { return 0; }\n", nil},
		{"return-no-value-nested", "function f(): i32 { if (1 < 2) { return; } return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E012"}},
		{"dup-var-same-block", "function main(): i32 { let x: i32 = 1; let x: i32 = 2; return x; }\n", []string{"E013"}},
		{"dup-var-nested-shadow-ok", "function main(): i32 { let x: i32 = 1; if (1 < 2) { let x: i32 = 2; } return x; }\n", nil},
		{"var-shadows-param-ok", "function f(a: i32): i32 { let a: i32 = 1; return a; }\nfunction main(): i32 { return 0; }\n", nil},
		{"empty-array-no-annotation", "function main(): i32 { let x = []; return 0; }\n", []string{"E020"}},
		{"empty-array-annotated-ok", "function main(): i32 { let x: i32[] = []; return 0; }\n", nil},
		{"nonempty-array-ok", "function main(): i32 { let x = [1, 2]; return x[0]; }\n", nil},
		{"and-on-ints", "function main(): i32 { if (1 && 2) { return 1; } return 0; }\n", []string{"E009"}},
		{"not-on-int", "function main(): i32 { if (!5) { return 1; } return 0; }\n", []string{"E009"}},
		{"and-on-bools-ok", "function main(): i32 { if ((1 < 2) && (2 < 3)) { return 1; } return 0; }\n", nil},
		{"not-on-bool-ok", "function main(): i32 { if (!(1 < 2)) { return 1; } return 0; }\n", nil},
		// E009 (extended ops): unary `-`, shift / bitwise, and ordering a
		// pair the operator has no rule for. Ordering is clean on the types
		// it does cover — i32 / f64, and string (byte order, #7110).
		{"neg-on-string", "function main(): i32 { let s = \"a\"; let n = -s; return 0; }\n", []string{"E009"}},
		{"shift-on-string", "function main(): i32 { let s = \"a\"; return s << 2; }\n", []string{"E009"}},
		{"bitand-on-string", "function main(): i32 { let s = \"a\"; return s & 1; }\n", []string{"E009"}},
		{"order-on-strings-ok", "function main(): i32 { if (\"a\" < \"b\") { return 1; } return 0; }\n", nil},
		{"order-on-string-vars-ok", "function main(): i32 { let a: string = \"a\"; let b: string = \"b\"; if (a >= b) { return 1; } return 0; }\n", nil},
		{"order-mismatch", "function main(): i32 { if (5 < \"x\") { return 1; } return 0; }\n", []string{"E009"}},
		{"order-i32-ok", "function main(): i32 { if (3 < 5) { return 1; } return 0; }\n", nil},
		{"order-f64-ok", "function main(): i32 { if (1.5 < 2.5) { return 1; } return 0; }\n", nil},
		{"neg-i32-ok", "function main(): i32 { let x = 5; return -x; }\n", nil},
		{"shift-i32-ok", "function main(): i32 { return 1 << 4; }\n", nil},
		{"eq-i32-string", "function main(): i32 { if (1 == \"x\") { return 1; } return 0; }\n", []string{"E041"}},
		{"eq-bool-i32", "function main(): i32 { if ((1 < 2) == 3) { return 1; } return 0; }\n", []string{"E041"}},
		{"eq-i32-i32-ok", "function main(): i32 { if (1 == 2) { return 1; } return 0; }\n", nil},
		{"eq-string-string-ok", "function main(): i32 { if (\"a\" == \"b\") { return 1; } return 0; }\n", nil},
		// E041 (composite ordering): `<` / `>` / `<=` / `>=` on two values of
		// the SAME composite type (struct / array / tuple) is E041, not E009.
		// Mixed composite/scalar or differing types stay E009; scalars are ok.
		{"order-struct-struct", "struct P { x: i32 }\nfunction main(): i32 { let a = P { x: 1 }; let b = P { x: 2 }; if (a < b) { return 1; } return 0; }\n", []string{"E041"}},
		{"order-array-array", "function main(): i32 { let a = [1]; let b = [2]; if (a <= b) { return 1; } return 0; }\n", []string{"E041"}},
		{"order-struct-i32-mixed", "struct P { x: i32 }\nfunction main(): i32 { let a = P { x: 1 }; if (a < 3) { return 1; } return 0; }\n", []string{"E009"}},
		{"field-unknown", "struct P { x: i32 }\nfunction main(): i32 { let p: P = P { x: 1 }; return p.y; }\n", []string{"E043"}},
		{"field-known-ok", "struct P { x: i32 }\nfunction main(): i32 { let p: P = P { x: 1 }; return p.x; }\n", nil},
		{"method-call-not-field-ok", "struct P { x: i32 }\nfunction (p: P) getx(): i32 { return p.x; }\nfunction main(): i32 { let p: P = P { x: 1 }; return p.getx(); }\n", nil},
		{"field-nested-unknown", "struct Q { a: i32 }\nstruct P { q: Q }\nfunction main(): i32 { let p: P = P { q: Q { a: 1 } }; return p.q.z; }\n", []string{"E043"}},
		// E043 (non-struct-value variant): a field READ on an i32 / string /
		// array (no fields). Method calls and struct/tuple field access stay ok.
		{"field-on-i32", "function main(): i32 { let x = 5; return x.foo; }\n", []string{"E043"}},
		{"field-on-string", "function main(): i32 { let s = \"a\"; return s.foo; }\n", []string{"E043"}},
		{"field-on-array", "function main(): i32 { let a = [1, 2, 3]; return a.foo; }\n", []string{"E043"}},
		{"str-method-not-field-ok", "function main(): i32 { let s = \"abc\"; return s.len(); }\n", nil},
		{"slice-low-non-i32", "function main(): i32 { let s: string = \"hello\"; let t: Option[str] = s[\"x\":3]; return 0; }\n", []string{"E037"}},
		{"slice-high-non-i32", "function main(): i32 { let s: string = \"hello\"; let t: Option[str] = s[1:\"y\"]; return 0; }\n", []string{"E037"}},
		{"slice-bounds-ok", "function main(): i32 { let s: string = \"hello\"; let t: str = slice_unchecked(s, 1, 3); return 0; }\n", nil},
		// `s[:]` is the full-range slice (#6798 un-reserved it in the Go
		// parser). Since #5634 every string slice yields `Option[str]`, open
		// forms included, so the sink is annotated to match.
		//
		// This row pins acceptance on both lanes, not the slice's TYPE on the
		// self-host one: `s[:]` carries an `s.len()` bound, and the self-host
		// checker types every `.len()` call as unknown, which suppresses the
		// diagnostic either annotation would produce. Until that is fixed the
		// E003 below is the row that pins the sink rule, and it uses literal
		// bounds so both checkers actually settle the type.
		{"slice-full-ok", "function main(): i32 { let s: string = \"hello\"; let t: Option[str] = s[:]; return 0; }\n", nil},
		{"slice-str-sink", "function main(): i32 { let s: string = \"hello\"; let t: str = s[1:3]; return 0; }\n", []string{"E003"}},
		// E065 through a match-arm payload binding. Since #5634 the slice
		// is an `Option[str]`, so the unwrap is the only way to name the
		// view — and an arm binding is not a declared local, which is why
		// both checkers were blind to it. The param row is the control:
		// a view of caller-owned backing must stay accepted.
		{"e065-match-arm-local", "function mk(): string { return \"ab\"; }\nfunction f(): str { let s: string = mk(); match (s[0:1]) { Some(v) => { return v; }, None => { return \"\"; } } }\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		{"e065-match-arm-two-step", "function mk(): string { return \"ab\"; }\nfunction f(): str { let s: string = mk(); let t = s[0:1]; match (t) { Some(v) => { return v; }, None => { return \"\"; } } }\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		{"e065-match-arm-param-ok", "function f(p: string): str { match (p[0:1]) { Some(v) => { return v; }, None => { return \"\"; } } }\nfunction main(): i32 { return 0; }\n", nil},
		{"e065-slice-unchecked-return", "function mk(): string { return \"ab\"; }\nfunction f(): str { let s: string = mk(); return slice_unchecked(s, 0, 1); }\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		// E065 through a CALLEE that returns a view of one of its own
		// parameters. Both checkers stopped their chase at a call, so a
		// one-line identity function carried the view past the rule; the
		// per-function summary is what closes it. The last two rows are the
		// precision controls: a callee that returns a DIFFERENT argument
		// than the local-backed one, and one that views a param-backed
		// source, both stay accepted — a summary coarsened to "this
		// function returns some view" would reject both.
		// A struct literal naming no struct is E043, so the typed lowering
		// never meets it (#10965): an imported struct left unqualified, and a
		// name nothing declares.
		{"e043-literal-of-unqualified-imported-struct", "import \"std/json\";\nfunction main(): i32 {\n    let e: json.JsonError = JsonError { message: \"m\", offset: 0, line: 1, col: 1 };\n    return e.line;\n}\n", []string{"E043"}},
		{"e043-literal-of-undeclared-struct", "function main(): i32 {\n    let l = Nope { body: 1024 };\n    return 0;\n}\n", []string{"E043"}},
		// A `str` receiver's summary is keyed as `string`, the key a call site
		// looks it up by (#10924).
		{"e065-str-receiver-method", "function mk(): string { return \"ab\"; }\nfunction (s: str) head1(): str { return slice_unchecked(s, 0, 1); }\nfunction f(): str { let s: string = mk(); return s.head1(); }\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		{"e065-callee-launder", "function mk(): string { return \"ab\"; }\nfunction idv(s: str): str { return s; }\nfunction f(): str { let s: string = mk(); return idv(slice_unchecked(s, 0, 1)); }\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		{"e065-callee-two-hop", "function mk(): string { return \"ab\"; }\nfunction idv(s: str): str { return s; }\nfunction hop(s: str): str { return idv(s); }\nfunction f(): str { let s: string = mk(); return hop(slice_unchecked(s, 0, 1)); }\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		{"e065-callee-method", "function mk(): string { return \"ab\"; }\nfunction (s: string) view(): str { return s; }\nfunction f(): str { let s: string = mk(); return s.view(); }\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		{"e065-callee-other-arg-ok", "function mk(): string { return \"ab\"; }\nfunction second(a: str, b: str): str { return b; }\nfunction f(): str { let s: string = mk(); return second(slice_unchecked(s, 0, 1), \"lit\"); }\nfunction main(): i32 { return 0; }\n", nil},
		{"e065-callee-param-ok", "function idv(s: str): str { return s; }\nfunction f(p: string): str { return idv(slice_unchecked(p, 0, 1)); }\nfunction main(): i32 { return 0; }\n", nil},
		{"e065-callee-var-binding", "function mk(): string { return \"ab\"; }\nfunction idv(s: str): str { return s; }\nfunction f(): str { let s: string = mk(); let t: str = idv(slice_unchecked(s, 0, 1)); return t; }\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		// `x.len()` in METHOD spelling. Both receivers resolve through a
		// table that is empty without the stdlib in scope, so the
		// statement typed unknown: check_func_body then marked the module
		// ill-typed while every coded pass stayed silent, and the whole
		// class came out as the uncoded "cannot represent yet" bail
		// rather than a real code. A mismatched sink is what makes that
		// visible — the matching sink checked clean either way.
		{"len-method-string-sink", "function main(): i32 { let s: string = \"hello\"; let b: boolean = s.len(); return 0; }\n", []string{"E003"}},
		{"len-method-array-sink", "function main(): i32 { let xs: i32[] = [1, 2, 3]; let b: boolean = xs.len(); return 0; }\n", []string{"E003"}},
		{"len-method-literal-sink", "function main(): i32 { let b: boolean = \"lit\".len(); return 0; }\n", []string{"E003"}},
		{"len-method-ok", "function main(): i32 { let s: string = \"hello\"; let xs: i32[] = [1, 2]; return s.len() + xs.len(); }\n", nil},
		{"tuple-field-non-numeric", "function main(): i32 { let t = (1, 2); return t.foo; }\n", []string{"E046"}},
		{"tuple-field-out-of-range", "function main(): i32 { let t = (1, 2); return t.5; }\n", []string{"E046"}},
		// The rc detector intrinsics type as native registers them.
		{"rc-intrinsics-clean", "function main(): i32 { let n: i32 = __rc_underflow_count() + __arr_push_shared_count(); let b: i64 = __arr_push_shared_bytes(); return n; }\n", nil},
		{"rc-intrinsic-sink", "function main(): i32 { let b: boolean = __rc_underflow_count(); return 0; }\n", []string{"E003"}},
		// A tuple has no methods: a method-call callee takes the field rule.
		{"tuple-method-call", "function main(): i32 { let t = (1, 2); return t.len(); }\n", []string{"E046"}},
		{"tuple-method-call-builtin", "function main(): i32 { let t = (1, 2); print(t.to_string()); return 0; }\n", []string{"E046"}},
		{"tuple-element-fn-call-clean", "function one(): i32 { return 1; }\nfunction main(): i32 { let t: (() => i32, i32) = (one, 4); return t.0() + t.1; }\n", nil},
		{"tuple-field-ok", "function main(): i32 { let t = (1, 2); return t.0; }\n", nil},
		// E003 (tuple var annotation): a tuple-literal init must match the
		// annotation element-wise (and in arity). Matching tuples — including
		// nested, union-element, and struct-element — stay clean.
		{"tuple-annot-elem-bad", "function main(): i32 { let t: (i32, string) = (1, 2); return 0; }\n", []string{"E003"}},
		{"tuple-annot-order-bad", "function main(): i32 { let t: (string, i32) = (1, 2); return 0; }\n", []string{"E003"}},
		{"tuple-annot-arity-bad", "function main(): i32 { let t: (i32, string) = (1, \"a\", 3); return 0; }\n", []string{"E003"}},
		{"tuple-annot-ok", "function main(): i32 { let t: (i32, string) = (1, \"a\"); return t.0; }\n", nil},
		{"tuple-annot-nested-ok", "function main(): i32 { let t: (i32, (string, i32)) = (1, (\"a\", 2)); return t.0; }\n", nil},
		{"tuple-annot-union-ok", "enum E { A, B }\nfunction main(): i32 { let t: (E, i32) = (A, 1); return 0; }\n", nil},
		{"arith-sub-string", "function main(): i32 { let n: i32 = 1 - \"x\"; return n; }\n", []string{"E009"}},
		{"arith-add-mismatch", "function main(): i32 { let s = 1 + \"x\"; return 0; }\n", []string{"E009"}},
		{"arith-mul-ok", "function main(): i32 { return 3 * 4; }\n", nil},
		{"string-concat-ok", "function main(): i32 { let s: string = \"a\" + \"b\"; return 0; }\n", nil},
		{"literal-too-big-i32", "function main(): i32 { let x: i32 = 3000000000; return 0; }\n", []string{"E047"}},
		{"literal-i32-max-ok", "function main(): i32 { let x: i32 = 2147483647; return 0; }\n", nil},
		{"literal-i32-maxplus1", "function main(): i32 { let x: i32 = 2147483648; return 0; }\n", []string{"E047"}},
		{"literal-fits-i32-ok", "function main(): i32 { let x: i32 = 2000000000; return 0; }\n", nil},
		// A typed suffix pins the width in the parser, so nothing settles the
		// literal and neither checker judged its range (#8639). The sign comes
		// from the enclosing unary, so the most negative value of each signed
		// width keeps its only spelling and a `-` on an unsigned one is refused.
		{"literal-suffix-u8-over", "function main(): i32 { let x: u8 = 300u8; return 0; }\n", []string{"E047"}},
		{"literal-suffix-u8-max-ok", "function main(): i32 { let x: u8 = 255u8; return 0; }\n", nil},
		{"literal-suffix-u8-negative", "function main(): i32 { let x: u8 = -1u8; return 0; }\n", []string{"E047"}},
		{"literal-suffix-u32-over", "function main(): i32 { let x: u32 = 4294967296u32; return 0; }\n", []string{"E047"}},
		{"literal-suffix-u32-max-ok", "function main(): i32 { let x: u32 = 4294967295u32; return 0; }\n", nil},
		{"literal-suffix-u64-max-ok", "function main(): i32 { let x: u64 = 18446744073709551615u64; return 0; }\n", nil},
		{"literal-suffix-u64-negative", "function main(): i32 { let x: u64 = -1u64; return 0; }\n", []string{"E047"}},
		{"literal-suffix-i32-over", "function main(): i32 { let x: i32 = 2147483648i32; return 0; }\n", []string{"E047"}},
		{"literal-suffix-i32-min-ok", "function main(): i32 { let x: i32 = -2147483648i32; return 0; }\n", nil},
		{"literal-suffix-i32-under", "function main(): i32 { let x: i32 = -2147483649i32; return 0; }\n", []string{"E047"}},
		{"literal-suffix-i64-over", "function main(): i32 { let x: i64 = 9223372036854775808i64; return 0; }\n", []string{"E047"}},
		{"literal-suffix-i64-min-ok", "function main(): i32 { let x: i64 = -9223372036854775808i64; return 0; }\n", nil},
		// Two minuses cancel, so the magnitude is judged as positive again.
		{"literal-suffix-double-neg", "function main(): i32 { let x: i64 = - -9223372036854775808i64; return 0; }\n", []string{"E047"}},
		{"literal-suffix-double-neg-ok", "function main(): i32 { let x: i64 = - -5i64; return 0; }\n", nil},
		// Hex spellings reach the same bounds.
		{"literal-suffix-hex-u32-over", "function main(): i32 { let x: u32 = 0x100000000u32; return 0; }\n", []string{"E047"}},
		{"literal-suffix-hex-u32-max-ok", "function main(): i32 { let x: u32 = 0xFFFFFFFFu32; return 0; }\n", nil},
		// Nothing settles a suffixed literal, so the rule has to reach every
		// position one can be written in — not only a var initialiser.
		{"literal-suffix-arg", "function take(v: u8): i32 { return 0; }\nfunction main(): i32 { return take(300u8); }\n", []string{"E047"}},
		{"literal-suffix-struct-field", "struct S { v: u8 }\nfunction main(): i32 { let s = S { v: 300u8 }; return 0; }\n", []string{"E047"}},
		{"literal-suffix-unannotated", "function main(): i32 { let x = 300u8; return 0; }\n", []string{"E047"}},
		// A quoted literal uses the same node with its spelling in `raw`; it is
		// not a written numeral and no range rule applies to it.
		{"literal-byte-quoted-ok", "function main(): i32 { let b: u8 = b'0'; return b as i32; }\n", nil},
		// A wide literal inside an unannotated binding's arithmetic, or a cast
		// operand, widens the whole expression to i64 rather than being judged
		// against the i32 default (#8668) — both checkers are silent.
		{"literal-wide-compound-unannot-ok", "function main(): i32 { let t = 3 - 4611686018427387904; let u: i64 = t; return 0; }\n", nil},
		{"literal-wide-compound-cast-ok", "function main(): i32 { let f = (3 - 4611686018427387904) as f64; return 0; }\n", nil},
		{"literal-wide-generic-arg-ok", "function id[T](v: T): T { return v; }\nfunction main(): i32 { let t = id(4611686018427387904); let u: i64 = t; return 0; }\n", nil},
		{"literal-wide-compare-ok", "function main(): i32 { let b = 4611686018427387904 > 1; if (b) { return 1; } return 0; }\n", nil},
		{"literal-wide-generic-tuple-ok", "function pair[A, B](a: A, b: B): (A, B) { return (a, b); }\nfunction main(): i32 { let p = pair(4611686018427387904, \"hello\"); if (p.0 == 4611686018427387904) { return 1; } return 0; }\n", nil},
		// #8640: the range rule reaches every integer WIDTH, not just i32, and
		// every destination a literal settles at. A const follows the same rule:
		// the self-host represents one as a `FuncDecl` whose body returns the
		// initialiser, so the return-position destination judges it. Each
		// refused row is paired with the value one step inside the bound, so
		// the rule cannot degenerate into refusing every wide literal.
		{"literal-u8-over", "function main(): i32 { let x: u8 = 256; return 0; }\n", []string{"E047"}},
		{"literal-u8-max-ok", "function main(): i32 { let x: u8 = 255; return 0; }\n", nil},
		{"literal-u32-over", "function main(): i32 { let x: u32 = 4294967296; return 0; }\n", []string{"E047"}},
		{"literal-u32-max-ok", "function main(): i32 { let x: u32 = 4294967295; return 0; }\n", nil},
		{"literal-u32-negative", "function main(): i32 { let x: u32 = -1; return 0; }\n", []string{"E047"}},
		{"literal-i64-over", "function main(): i32 { let x: i64 = 9223372036854775808; return 0; }\n", []string{"E047"}},
		{"literal-i64-max-ok", "function main(): i32 { let x: i64 = 9223372036854775807; return 0; }\n", nil},
		{"literal-u64-max-ok", "function main(): i32 { let x: u64 = 18446744073709551615; return 0; }\n", nil},
		// Past u64 no integer type holds at all, so every destination refuses
		// it — including the i64 an unannotated binding defaults to.
		{"literal-past-u64-u64", "function main(): i32 { let x: u64 = 18446744073709551616; return 0; }\n", []string{"E047"}},
		{"literal-past-u64-i32", "function main(): i32 { let x: i32 = 18446744073709551616; return 0; }\n", []string{"E047"}},
		{"literal-past-u64-unannotated", "function main(): i32 { let x = 18446744073709551616; return 0; }\n", []string{"E047"}},
		{"literal-past-u64-arg", "function take(v: u8): i32 { return 0; }\nfunction main(): i32 { return take(18446744073709551616); }\n", []string{"E047"}},
		{"literal-past-u64-return", "function f(): i64 { return 18446744073709551616; }\nfunction main(): i32 { return f() as i32; }\n", []string{"E047"}},
		{"literal-past-u64-array", "function main(): i32 { let xs: i64[] = [18446744073709551616]; return xs.len(); }\n", []string{"E047"}},
		{"literal-past-u64-hex", "function main(): i32 { let x: u64 = 0x10000000000000000; return 0; }\n", []string{"E047"}},
		// A const initialiser is judged before it folds: folding wraps at the
		// declared width, so a literal too wide for the type has already
		// wrapped by the time the folded value could be inspected.
		{"const-i32-over", "const B: i32 = 2147483648;\nfunction main(): i32 { return B; }\n", []string{"E047"}},
		{"const-i32-max-ok", "const B: i32 = 2147483647;\nfunction main(): i32 { return B; }\n", nil},
		{"const-i32-min-ok", "const B: i32 = -2147483648;\nfunction main(): i32 { return B; }\n", nil},
		{"const-i32-under", "const B: i32 = -2147483649;\nfunction main(): i32 { return B; }\n", []string{"E047"}},
		{"const-u8-over", "const B: u8 = 256;\nfunction main(): i32 { return B as i32; }\n", []string{"E047"}},
		{"const-u8-max-ok", "const B: u8 = 255;\nfunction main(): i32 { return B as i32; }\n", nil},
		{"const-u8-negative", "const B: u8 = -1;\nfunction main(): i32 { return B as i32; }\n", []string{"E047"}},
		{"const-u32-over", "const B: u32 = 4294967296;\nfunction main(): i32 { return B as i32; }\n", []string{"E047"}},
		{"const-u32-max-ok", "const B: u32 = 4294967295;\nfunction main(): i32 { return B as i32; }\n", nil},
		{"const-i64-over", "const B: i64 = 9223372036854775808;\nfunction main(): i32 { return B as i32; }\n", []string{"E047"}},
		{"const-i64-min-ok", "const B: i64 = -9223372036854775808;\nfunction main(): i32 { return B as i32; }\n", nil},
		{"const-u64-max-ok", "const B: u64 = 18446744073709551615;\nfunction main(): i32 { return B as i32; }\n", nil},
		{"const-past-u64", "const B: u64 = 18446744073709551616;\nfunction main(): i32 { return B as i32; }\n", []string{"E047"}},
		{"const-hex-u8-over", "const B: u8 = 0x100;\nfunction main(): i32 { return B as i32; }\n", []string{"E047"}},
		{"const-hex-u8-max-ok", "const B: u8 = 0xFF;\nfunction main(): i32 { return B as i32; }\n", nil},
		{"const-suffix-u8-over", "const B: u8 = 300u8;\nfunction main(): i32 { return B as i32; }\n", []string{"E047"}},
		{"const-earlier-ref-ok", "const A: i32 = 5;\nconst B: i32 = A + 1;\nfunction main(): i32 { return B; }\n", nil},
		// Every destination a settled literal can reach, at a non-i32 width.
		{"literal-u8-arg", "function take(v: u8): i32 { return 0; }\nfunction main(): i32 { return take(256); }\n", []string{"E047"}},
		{"literal-u8-arg-ok", "function take(v: u8): i32 { return 0; }\nfunction main(): i32 { return take(255); }\n", nil},
		{"literal-u8-struct-field", "struct S { v: u8 }\nfunction main(): i32 { let s = S { v: 256 }; return 0; }\n", []string{"E047"}},
		{"literal-u8-struct-field-ok", "struct S { v: u8 }\nfunction main(): i32 { let s = S { v: 255 }; return 0; }\n", nil},
		{"literal-u8-array-elem", "function main(): i32 { let xs: u8[] = [1, 256]; return xs.len(); }\n", []string{"E047"}},
		{"literal-u8-array-elem-ok", "function main(): i32 { let xs: u8[] = [1, 255]; return xs.len(); }\n", nil},
		{"literal-u8-return", "function f(): u8 { return 256; }\nfunction main(): i32 { return f() as i32; }\n", []string{"E047"}},
		{"literal-u8-return-ok", "function f(): u8 { return 255; }\nfunction main(): i32 { return f() as i32; }\n", nil},
		{"literal-u8-assign", "function main(): i32 { let a: u8 = 1; a = 256; return 0; }\n", []string{"E047"}},
		{"literal-u8-assign-ok", "function main(): i32 { let a: u8 = 1; a = 255; return 0; }\n", nil},
		// #8640: a destination hands its type to BOTH operands of every
		// arithmetic, bitwise and shift operator — the shift COUNT included —
		// so a literal under one is judged there. The self-host recursed into
		// `+ - * /` only, so `300 & 1` at u8 went unjudged. Comparison and
		// logical operators do not pass a type down, and their operands stay
		// unjudged in both checkers.
		{"literal-u8-mod-operand", "function main(): i32 { let a: u8 = 300 % 7; return 0; }\n", []string{"E047"}},
		{"literal-u8-mod-ok", "function main(): i32 { let a: u8 = 255 % 7; return 0; }\n", nil},
		{"literal-u8-and-operand", "function main(): i32 { let a: u8 = 300 & 1; return 0; }\n", []string{"E047"}},
		{"literal-u8-and-ok", "function main(): i32 { let a: u8 = 255 & 1; return 0; }\n", nil},
		{"literal-u8-or-operand", "function main(): i32 { let a: u8 = 300 | 1; return 0; }\n", []string{"E047"}},
		{"literal-u8-or-ok", "function main(): i32 { let a: u8 = 200 | 55; return 0; }\n", nil},
		{"literal-u8-xor-rhs", "function main(): i32 { let a: u8 = 1 ^ 256; return 0; }\n", []string{"E047"}},
		{"literal-u8-shl-operand", "function main(): i32 { let a: u8 = 300 << 1; return 0; }\n", []string{"E047"}},
		{"literal-u8-shl-count", "function main(): i32 { let a: u8 = 1 << 300; return 0; }\n", []string{"E047"}},
		{"literal-u8-shl-ok", "function main(): i32 { let a: u8 = 1 << 3; return 0; }\n", nil},
		{"literal-u8-shr-count", "function main(): i32 { let a: u8 = 255 >> 300; return 0; }\n", []string{"E047"}},
		{"literal-i32-and-operand", "function main(): i32 { let a: i32 = 3000000000 & 1; return 0; }\n", []string{"E047"}},
		{"literal-i64-and-operand", "function main(): i32 { let a: i64 = 9223372036854775808 & 1; return 0; }\n", []string{"E047"}},
		{"literal-u8-nested-and", "function main(): i32 { let a: u8 = (300 & 1) + 1; return 0; }\n", []string{"E047"}},
		{"literal-u8-neg-in-bitwise", "function main(): i32 { let a: u8 = -1 & 1; return 0; }\n", []string{"E047"}},
		{"const-u8-mod-operand", "const B: u8 = 300 % 7;\nfunction main(): i32 { return B as i32; }\n", []string{"E047"}},
		{"const-u8-and-operand", "const B: u8 = 300 & 1;\nfunction main(): i32 { return B as i32; }\n", []string{"E047"}},
		{"const-u8-shl-count", "const B: u8 = 1 << 300;\nfunction main(): i32 { return B as i32; }\n", []string{"E047"}},
		{"const-u8-arith-operand", "const B: u8 = 300 - 100;\nfunction main(): i32 { return B as i32; }\n", []string{"E047"}},
		// The SATURATING operators type their operands too — they are in
		// native's settleInt Binary case — so a wide literal under one is
		// judged at the destination's width like any other.
		{"literal-u8-sat-add-operand", "function main(): i32 { let a: u8 = 300 +| 1; return 0; }\n", []string{"E047"}},
		{"literal-u8-sat-sub-operand", "function main(): i32 { let a: u8 = 300 -| 1; return 0; }\n", []string{"E047"}},
		{"literal-u8-sat-mul-operand", "function main(): i32 { let a: u8 = 300 *| 1; return 0; }\n", []string{"E047"}},
		{"literal-u8-sat-shl-count", "function main(): i32 { let a: u8 = 1 <<| 300; return 0; }\n", []string{"E047"}},
		{"literal-u8-sat-add-in-range-ok", "function main(): i32 { let a: u8 = 200 +| 1; return 0; }\n", nil},
		// A CHECKED operator yields an Option, so it never reaches an integer
		// destination and no range rule applies — native answers E003 on the
		// Option instead, which is why settleInt omits these.
		{"literal-u8-checked-add-is-option", "function main(): i32 { let a: u8 = 300 +? 1; return 0; }\n", []string{"E003"}},
		// A const naming either family is refused by the const GRAMMAR, whose
		// diagnostic is uncoded on both sides — so it cannot be gated here and
		// lives in TestSelfHostConstGrammarX86_64, which compares message text.
		{"const-shr-still-ok", "const B: i32 = 6 >> 1;\nfunction main(): i32 { return B; }\n", nil},
		{"const-u8-arith-ok", "const B: u8 = 200 + 50;\nfunction main(): i32 { return B as i32; }\n", nil},
		// A comparison hands neither operand a type, so a wide literal under
		// one is not judged against the destination in either checker.
		{"literal-compare-operand-ok", "function main(): i32 { let b: boolean = 3000000000 > 1; if (b) { return 1; } return 0; }\n", nil},
		{"const-compare-operand-ok", "const B: boolean = 300 > 1;\nfunction main(): i32 { if (B) { return 1; } return 0; }\n", nil},
		{"enum-redeclared", "enum Opt { A, B }\nenum Opt { C, D }\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		{"enum-dup-variant", "enum Opt { A, A, B }\nfunction main(): i32 { return 0; }\n", []string{"E017"}},
		{"enum-clean-ok", "enum Opt { A, B }\nfunction main(): i32 { return 0; }\n", nil},
		// Struct form (#4363 item 4): the flat struct table also carries enum
		// variant payloads (enum_owner-tagged), which must not read as user
		// struct redeclarations — only two genuine `struct X` decls collide.
		{"struct-redeclared", "struct P { x: i32 }\nstruct P { y: i32 }\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		{"struct-beside-enum-payloads-ok", "enum A { X(i32) }\nenum B { Y(i32) }\nstruct S { n: i32 }\nfunction main(): i32 { return 0; }\n", nil},
		{"variant-multi-enum-ref", "enum A { X, Y }\nenum B { X, Z }\nfunction main(): i32 { let a: A = X; return 0; }\n", []string{"E036"}},
		{"variant-multi-enum-unref-ok", "enum A { X, Y }\nenum B { X, Z }\nfunction main(): i32 { return 0; }\n", nil},
		{"variant-disjoint-ref-ok", "enum A { P, Q }\nenum B { R, S }\nfunction main(): i32 { let a: A = P; return 0; }\n", nil},
		// E036 also covers a user-enum variant that collides with a BUILT-IN
		// enum variant (Option / Result / …): `enum O { Some(i32), None }`
		// shadows Option's Some / None, so a bare reference must be qualified.
		// Plain Option usage (no colliding user enum) stays clean.
		{"variant-builtin-collide-none", "enum O { Some(i32), None }\nfunction get(): O { return None; }\nfunction main(): i32 { return 0; }\n", []string{"E036"}},
		{"variant-builtin-collide-result", "enum E { Ok(i32), Bad }\nfunction get(): E { return Ok(1); }\nfunction main(): i32 { return 0; }\n", []string{"E036"}},
		{"variant-builtin-no-collide-ok", "function get(): Option[i32] { return None; }\nfunction main(): i32 { return 0; }\n", nil},
		// A QUALIFIED variant reference `Enum.Variant` is valid — the enum-name
		// qualifier is not a bare value, so it must not trip E001. Covers a
		// user enum, a collision resolved by qualifying, and a built-in enum.
		{"qualified-variant-user-ok", "enum Color { Red, Green }\nfunction get(): Color { return Color.Red; }\nfunction main(): i32 { return 0; }\n", nil},
		{"qualified-variant-collide-ok", "enum A { X, Y }\nenum B { X, Z }\nfunction get(): A { return A.X; }\nfunction main(): i32 { return 0; }\n", nil},
		{"qualified-variant-builtin-ok", "enum O { Some(i32), None }\nfunction get(): O { return O.None; }\nfunction main(): i32 { return 0; }\n", nil},
		// A qualified reference to a NON-existent variant is E036 ("enum X has
		// no variant Y") — for a user enum, a union alias, and a built-in enum.
		{"qualified-variant-enum-bad", "enum Color { Red, Green }\nfunction main(): i32 { return Color.Blue; }\n", []string{"E036"}},
		{"qualified-variant-union-bad", "struct A { x: i32 }\nstruct B { y: i32 }\ntype U = A | B;\nfunction main(): i32 { let u: U = U.Nope; return 0; }\n", []string{"E036"}},
		{"qualified-variant-builtin-bad", "function main(): i32 { let o: Option[i32] = Option.Foo; return 0; }\n", []string{"E036"}},
		// A local BINDING of a payload variant's name SHADOWS the variant, so
		// the read is of the local (#6658). The self-host asked only whether the
		// NAME was a payload-bearing variant, never whether it was bound, and
		// over-rejected the read with E036; the call typed to the enum, which
		// surfaced as an E002 return mismatch somewhere else entirely rather
		// than as native's E038.
		//
		// The last two rows are what stops the fix from simply disabling the
		// rule: unshadowed, the bare read is still E036 and the constructor call
		// still types as its enum.
		{"variant-shadowed-by-local-read-ok", "enum W { Wrap(i32), Empty }\nfunction main(): i32 { let Wrap = 3; return Wrap; }\n", nil},
		{"variant-shadowed-by-local-call", "enum W { Wrap(i32), Empty }\nfunction main(): i32 { let Wrap = 3; return Wrap(1); }\n", []string{"E038"}},
		{"variant-shadowed-builtin-read-ok", "function main(): i32 { let Some = 4; return Some; }\n", nil},
		{"variant-unshadowed-bare-read", "enum W { Wrap(i32), Empty }\nfunction main(): i32 { let x: W = Wrap; return 0; }\n", []string{"E036"}},
		{"variant-unshadowed-ctor-ok", "enum W { Wrap(i32), Empty }\nfunction f(): W { return Wrap(1); }\nfunction main(): i32 { return 0; }\n", nil},
		// A match on a SCALAR scrutinee needs an unguarded `_`, in both
		// frontends (#6594). The self-host had no rule at all here, so it
		// accepted these silently and then compiled and ran them — the
		// dangerous direction, since the same source builds under one compiler
		// and not the other.
		//
		// `boolean` is the interesting row: `true` / `false` really are the
		// whole domain, and native still requires the wildcard (its
		// checkLiteralMatch comment records that as deliberate). Widening the
		// language to accept it is a change to BOTH compilers; this is parity
		// with what native does today.
		//
		// The guarded-wildcard row is what stops `has_guard` being ignored — a
		// guarded `_` may fall through, so it does not make the match
		// exhaustive. The accept rows are what stop the rule being a blanket
		// rejection of every scalar match.
		{"match-bool-two-literals-expr-no-wildcard", "function main(): i32 {\n    let b: boolean = true;\n    return (match (b) { true => 7i32, false => 1i32 });\n}\n", []string{"E030"}},
		{"match-bool-two-literals-stmt-no-wildcard", "function main(): i32 { let b: boolean = true; match (b) { true => { return 1; }, false => { return 2; } } }\n", []string{"E030"}},
		{"match-bool-with-wildcard-ok", "function main(): i32 { let b: boolean = true; match (b) { true => { return 7; }, _ => { return 1; } } }\n", nil},
		{"match-int-with-wildcard-ok", "function main(): i32 { let n: i32 = 1; match (n) { 0 => { return 5; }, _ => { return 6; } } }\n", nil},
		{"match-enum-exhaustive-no-wildcard-ok", "enum Opt { Has(i32), Nil }\nfunction main(): i32 { let o: Opt = Nil; match (o) { Has(n) => { return n; }, Nil => { return 0; } } }\n", nil},
		// The NUMERIC and STRING forms of the same rule (#6683). `true`/`false`
		// arms parse variant-shaped and so kept their StmtMatch, but a numeric or
		// string literal arm had no Pattern shape to live in: build_literal_match
		// desugared the whole match to an if-chain, and with the arms gone the
		// rule could not see them. What the user got instead was E052 — "can fall
		// off the end", naming the wrong problem — and in a VOID function nothing
		// at all, so the self-host compiled and ran a program native rejects.
		{"match-int-literals-no-wildcard", "function main(): i32 { let n: i32 = 1; match (n) { 0 => { return 5; }, 1 => { return 6; } } }\n", []string{"E030"}},
		{"match-string-literals-no-wildcard", "function main(): i32 { let s: string = \"a\"; match (s) { \"a\" => { return 1; } } }\n", []string{"E030"}},
		{"match-float-literals-no-wildcard", "function main(): i32 { let x: f64 = 1.5; match (x) { 1.5 => { return 5; } } }\n", []string{"E030"}},
		{"match-range-arm-no-wildcard", "function main(): i32 { let n: i32 = 1; match (n) { 0..3 => { return 5; } } }\n", []string{"E030"}},
		// A guarded `_` may fall through, so it does not make the match
		// exhaustive — the numeric sibling of the boolean guarded-wildcard row.
		{"match-int-guarded-wildcard-only", "function main(): i32 { let n: i32 = 1; match (n) { 0 => { return 5; }, _ when n > 9 => { return 6; } } return 7; }\n", []string{"E030"}},
		// A misplaced wildcard must still report E026 alone, not E026+E030: the
		// same StmtMatch hand-back serves both, and reporting one code where
		// native reports one is the point.
		{"match-int-misplaced-wildcard", "function main(): i32 { let n: i32 = 1; match (n) { _ => { return 5; }, 1 => { return 6; } } }\n", []string{"E026"}},
		// Found while fixing the above: a GUARDED `_` before the end is E026 on
		// native too — the misplaced-wildcard rule does not care about the guard,
		// only exhaustiveness does. The self-host had accepted this outright.
		{"match-int-misplaced-guarded-wildcard", "function main(): i32 { let n: i32 = 12; match (n) { 0 => { return 5; }, _ when n > 9 => { return 11; }, _ => { return 6; } } }\n", []string{"E026"}},
		{"match-enum-misplaced-guarded-wildcard", "enum E { A, B }\nfunction main(): i32 { let e: E = A; match (e) { A => { return 5; }, _ when 1 > 2 => { return 6; }, _ => { return 7; } } }\n", []string{"E026"}},
		{"match-int-guarded-wildcard-last-ok", "function main(): i32 { let n: i32 = 1; match (n) { 0 => { return 5; }, _ when n > 9 => { return 6; } } return 7; }\n", []string{"E030"}},
		{"match-string-with-wildcard-ok", "function main(): i32 { let s: string = \"a\"; match (s) { \"a\" => { return 1; }, _ => { return 2; } } }\n", nil},
		{"match-range-with-wildcard-ok", "function main(): i32 { let n: i32 = 1; match (n) { 0..3 => { return 5; }, _ => { return 6; } } }\n", nil},
		{"match-wildcard-not-last", "enum Opt { Has(i32), Nil }\nfunction main(): i32 { let o: Opt = Nil; match (o) { _ => { return 0; }, Has(n) => { return n; } } }\n", []string{"E026"}},
		{"match-variant-twice", "enum Opt { Has(i32), Nil }\nfunction main(): i32 { let o: Opt = Nil; match (o) { Has(n) => { return n; }, Has(m) => { return m; }, Nil => { return 0; } } }\n", []string{"E028"}},
		{"match-clean-ok", "enum Opt { Has(i32), Nil }\nfunction main(): i32 { let o: Opt = Nil; match (o) { Has(n) => { return n; }, Nil => { return 0; } } }\n", nil},
		{"match-wildcard-last-ok", "enum Opt { Has(i32), Nil }\nfunction main(): i32 { let o: Opt = Nil; match (o) { Has(n) => { return n; }, _ => { return 0; } } }\n", nil},
		// E026 on a LITERAL match (i32 / string scrutinee), where the
		// non-enum arms desugar to an if/else chain. A non-last wildcard
		// must still be E026 — and ONLY E026 — for any `_` position
		// (#3612): wildcard-first (where every arm returns, so the old
		// variant-path mis-parse used to add a spurious E052) and
		// wildcard-in-the-middle (which the old desugar silently swallowed,
		// dropping the diagnostic entirely). Native (the oracle) emits a
		// lone E026 in both. The wildcard-last / no-wildcard forms stay
		// clean and still lower through build_literal_match unchanged.
		{"match-lit-wildcard-first", "function main(): i32 { let x = 1; match (x) { _ => { return 0; }, 1 => { return 1; } } }\n", []string{"E026"}},
		{"match-lit-wildcard-middle", "function main(): i32 { let x = 1; match (x) { 1 => { return 1; }, _ => { return 9; }, 2 => { return 2; } } }\n", []string{"E026"}},
		{"match-lit-wildcard-first-3arm", "function main(): i32 { let x = 1; match (x) { _ => { return 0; }, 1 => { return 1; }, 2 => { return 2; } } }\n", []string{"E026"}},
		{"match-str-wildcard-middle", "function f(s: string): i32 { match (s) { \"a\" => { return 1; }, _ => { return 0; }, \"b\" => { return 2; } } }\nfunction main(): i32 { return f(\"a\"); }\n", []string{"E026"}},
		{"match-lit-wildcard-last-ok", "function classify(x: i32): i32 { match (x) { 1 => { return 10; }, 2 => { return 20; }, _ => { return 99; } } }\nfunction main(): i32 { return classify(2); }\n", nil},
		{"type-arity-param", "struct Box[T] { v: T }\nfunction f(b: Box[i32, i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E019"}},
		{"type-arity-field", "struct Box[T] { v: T }\nstruct W { b: Box[i32, i32] }\nfunction main(): i32 { return 0; }\n", []string{"E019"}},
		{"type-arity-var-array", "struct Pair[A, B] { first: A, second: B }\nfunction main(): i32 { let xs: Pair[i32][] = []; return 0; }\n", []string{"E019"}},
		{"type-arity-param-ok", "struct Box[T] { v: T }\nfunction f(b: Box[i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		{"array-elem-string-in-i32", "function main(): i32 { let a = [1, \"x\", 3]; return 0; }\n", []string{"E034"}},
		{"array-elem-i32-in-string", "function main(): i32 { let a = [\"a\", 1]; return 0; }\n", []string{"E034"}},
		{"array-elem-homogeneous-i32-ok", "function main(): i32 { let a = [1, 2, 3]; return a[0]; }\n", nil},
		{"array-elem-homogeneous-string-ok", "function main(): i32 { let a = [\"p\", \"q\"]; return 0; }\n", nil},
		// E034 (index variant): an array / string index must be an i32.
		{"index-string", "function main(): i32 { let a = [1, 2, 3]; return a[\"x\"]; }\n", []string{"E034"}},
		{"index-string-on-string", "function main(): i32 { let s = \"abc\"; return s[\"x\"] as i32; }\n", []string{"E034"}},
		{"index-bool", "function main(): i32 { let a = [1, 2, 3]; let b = true; return a[b]; }\n", []string{"E034"}},
		{"index-i32-ok", "function main(): i32 { let a = [1, 2, 3]; let i = 1; return a[i]; }\n", nil},
		{"index-i64", "function main(): i32 { let a = [1, 2, 3]; let i: i64 = 1; return a[i]; }\n", []string{"E034"}},
		{"index-u8-on-string", "function main(): i32 { let s = \"abc\"; let i: u8 = 1; return s[i] as i32; }\n", []string{"E034"}},
		// E034 / E037 (non-array/string source): indexing or slicing a value
		// that isn't an array or string. Arrays / strings stay ok.
		{"index-non-array", "function main(): i32 { let x = 5; return x[0]; }\n", []string{"E034"}},
		{"index-struct", "struct P { x: i32 }\nfunction main(): i32 { let p = P { x: 1 }; return p[0]; }\n", []string{"E034"}},
		{"slice-non-array", "function main(): i32 { let x = 5; let y = x[1:2]; return 0; }\n", []string{"E037"}},
		{"slice-i64-low-bound", "function main(): i32 { let a = [1, 2, 3]; let i: i64 = 1; let b = a[i:2]; return 0; }\n", []string{"E037"}},
		{"slice-i64-high-bound", "function main(): i32 { let a = [1, 2, 3]; let j: i64 = 2; let b = a[0:j]; return 0; }\n", []string{"E037"}},
		{"index-string-source-ok", "function main(): i32 { let s = \"ab\"; return s[0] as i32; }\n", nil},
		{"slice-array-source-ok", "function main(): i32 { let a = [1, 2, 3, 4]; let b = a[1:3]; return b[0]; }\n", nil},
		{"slice-string-source-ok", "function main(): i32 { let s = \"abcd\"; let t = slice_unchecked(s, 1, 3); return t.len(); }\n", nil},
		{"match-variant-on-i32", "enum E { A, B }\nfunction main(): i32 { let n: i32 = 5; match (n) { A => { return 1; }, _ => { return 0; } } }\n", []string{"E035"}},
		{"match-variant-on-string", "enum E { A, B }\nfunction main(): i32 { let s: string = \"x\"; match (s) { A => { return 1; }, _ => { return 0; } } }\n", []string{"E035"}},
		{"match-i32-wildcard-only-ok", "function main(): i32 { let n: i32 = 5; match (n) { _ => { return 0; } } }\n", nil},
		{"union-match-non-exhaustive", "struct A { x: i32 }\nstruct B { y: i32 }\npub type U = A | B;\nfunction f(u: U): i32 { match (u) { A(a) => { return a.x; } } return 0; }\nfunction main(): i32 { return f(A { x: 1 }); }\n", []string{"E030"}},
		{"union-match-exhaustive-ok", "struct A { x: i32 }\nstruct B { y: i32 }\npub type U = A | B;\nfunction f(u: U): i32 { match (u) { A(a) => { return a.x; }, B(b) => { return b.y; } } return 0; }\nfunction main(): i32 { return f(A { x: 1 }); }\n", nil},
		{"union-match-wildcard-ok", "struct A { x: i32 }\nstruct B { y: i32 }\npub type U = A | B;\nfunction f(u: U): i32 { match (u) { A(a) => { return a.x; }, _ => { return 0; } } return 0; }\nfunction main(): i32 { return f(A { x: 1 }); }\n", nil},
		{"match-binding-field-ok", "struct A { x: i32 }\nstruct B { y: i32 }\npub type U = A | B;\nfunction f(u: U): i32 { match (u) { A(a) => { return a.x; }, B(b) => { return b.y; } } return 0; }\nfunction main(): i32 { return f(A { x: 1 }); }\n", nil},
		{"match-binding-bad-field", "struct A { x: i32 }\nstruct B { y: i32 }\npub type U = A | B;\nfunction f(u: U): i32 { match (u) { A(a) => { return a.nope; }, B(b) => { return b.y; } } return 0; }\nfunction main(): i32 { return f(A { x: 1 }); }\n", []string{"E043"}},
		{"enum-match-non-exhaustive", "enum E { A, B }\nfunction f(e: E): i32 { match (e) { A => { return 1; } } return 0; }\nfunction main(): i32 { return f(A); }\n", []string{"E030"}},
		{"enum-match-exhaustive-ok", "enum E { A, B }\nfunction f(e: E): i32 { match (e) { A => { return 1; }, B => { return 2; } } return 0; }\nfunction main(): i32 { return f(A); }\n", nil},
		{"enum-match-payload-exhaustive-ok", "enum Opt { Has(i32), Nil }\nfunction main(): i32 { let o: Opt = Nil; match (o) { Has(n) => { return n; }, Nil => { return 0; } } }\n", nil},
		{"union-match-foreign-variant", "struct A { x: i32 }\nstruct B { y: i32 }\nstruct C { z: i32 }\npub type U = A | B;\nfunction f(u: U): i32 { match (u) { A(a) => { return a.x; }, C(c) => { return c.z; }, _ => { return 0; } } return 0; }\nfunction main(): i32 { return f(A { x: 1 }); }\n", []string{"E001", "E014"}},
		{"match-qualifier-mismatch", "enum E { A, B }\nenum F { C, D }\nfunction f(e: E): i32 { match (e) { F.A => { return 1; }, _ => { return 0; } } return 0; }\nfunction main(): i32 { return f(A); }\n", []string{"E029"}},
		// The EXPRESSION form of the same mismatch (#6576). Native's expression
		// arm loop carried its own copy of the qualifier validation with the
		// enum-qualifier case missing, so it reported the module-mismatch
		// wording here and — worse — rejected the LEGAL `E.A` spelling outright,
		// while the self-host accepted it. The two frontends disagreed about
		// what the language is, with native as the odd one out; both call sites
		// now share checkVariantQualifier.
		// `F.A` rather than `F.C`, mirroring the statement row: the variant NAME
		// is one E really has, so the qualifier is the only mistake and E029 is
		// the only code. `F.C` additionally draws a cascading E014 from native
		// and not from the self-host — a real but separate difference in how far
		// each carries on after the first error, which this row is not about.
		{"match-qualifier-mismatch-expr", "enum E { A, B }\nenum F { C, D }\nfunction f(e: E): i32 { return match (e) { F.A => 1i32, _ => 0i32 }; }\nfunction main(): i32 { return f(A); }\n", []string{"E029"}},
		// The legal spelling, in both positions: a qualifier naming the
		// scrutinee's OWN enum. Neither checker may report anything.
		{"match-qualifier-own-enum-stmt", "enum E { A, B }\nfunction f(e: E): i32 { match (e) { E.A => { return 1; }, E.B => { return 2; } } return 0; }\nfunction main(): i32 { return f(A); }\n", nil},
		{"match-qualifier-own-enum-expr", "enum E { A, B }\nfunction f(e: E): i32 { return match (e) { E.A => 1i32, E.B => 2i32 }; }\nfunction main(): i32 { return f(A); }\n", nil},
		{"match-qualifier-correct-ok", "enum E { A, B }\nfunction f(e: E): i32 { match (e) { E.A => { return 1; }, E.B => { return 2; } } return 0; }\nfunction main(): i32 { return f(A); }\n", nil},
		{"union-struct-name-collision", "struct A { x: i32 }\nstruct B { y: i32 }\nstruct C { z: i32 }\npub type B = A | C;\nfunction main(): i32 { return 0; }\n", []string{"E016"}},
		{"union-distinct-name-ok", "struct A { x: i32 }\nstruct C { z: i32 }\npub type U = A | C;\nfunction main(): i32 { return 0; }\n", nil},
		{"missing-return", "function f(): i32 { let x = 1; }\nfunction main(): i32 { return 0; }\n", []string{"E052"}},
		{"missing-return-one-armed-if", "function f(c: boolean): i32 { if (c) { return 1; } }\nfunction main(): i32 { return 0; }\n", []string{"E052"}},
		{"return-while-true-ok", "function f(): i32 { while (true) { return 1; } }\nfunction main(): i32 { return 0; }\n", nil},
		{"return-loop-ok", "function f(): i32 { loop { return 1; } }\nfunction main(): i32 { return 0; }\n", nil},
		// A loop that can break does not diverge (#8447), and a break inside
		// a block-, if- or match-expression counts like any other (#8562);
		// one that belongs to a nested loop or to a lambda's own loop does not.
		{"missing-return-loop-breaks", "function f(): i32 { loop { break; } }\nfunction main(): i32 { return 0; }\n", []string{"E052"}},
		{"missing-return-while-true-breaks", "function f(): i32 { while (true) { break; } }\nfunction main(): i32 { return 0; }\n", []string{"E052"}},
		{"missing-return-loop-breaks-in-block-expr", "function f(): i32 { loop { let z: i32 = { break; 1 }; } }\nfunction main(): i32 { return 0; }\n", []string{"E052"}},
		{"missing-return-loop-breaks-in-if-expr", "function f(n: i32): i32 { loop { let z: i32 = if (n > 0) { break; 1 } else { 2 }; } }\nfunction main(): i32 { return 0; }\n", []string{"E052"}},
		{"missing-return-loop-breaks-in-match-expr", "function f(n: i32): i32 { loop { let z: i32 = match (n) { 0 => { break; 1 }, _ => 2 }; } }\nfunction main(): i32 { return 0; }\n", []string{"E052"}},
		{"loop-inner-break-ok", "function f(): i32 { loop { while (true) { break; } } }\nfunction main(): i32 { return 0; }\n", nil},
		{"loop-lambda-break-ok", "function f(): i32 { loop { let g: () => i32 = (): i32 => { while (true) { break; } return 1; }; let x: i32 = g(); } }\nfunction main(): i32 { return 0; }\n", nil},
		{"return-if-else-ok", "function f(c: boolean): i32 { if (c) { return 1; } else { return 2; } }\nfunction main(): i32 { return 0; }\n", nil},
		// An if on a literal takes one arm, the shape a pruned branch on the
		// target leaves: `if (true)` exits when its then arm does, `if (false)`
		// when its else arm does.
		{"return-if-true-ok", "function f(): i32 { if (true) { return 1; } }\nfunction main(): i32 { return 0; }\n", nil},
		{"return-if-false-else-ok", "function f(): i32 { if (false) { let z = 1; } else { return 2; } }\nfunction main(): i32 { return 0; }\n", nil},
		{"missing-return-if-false", "function f(): i32 { if (false) { return 1; } }\nfunction main(): i32 { return 0; }\n", []string{"E052"}},
		// A bare `{ … }` statement exits when its body does; the self-host
		// parses it as a scoping `if (true)` with no else.
		{"return-nested-block-ok", "function f(): i32 { { { return 1; } } }\nfunction main(): i32 { return 0; }\n", nil},
		{"missing-return-nested-block", "function f(c: boolean): i32 { { if (c) { return 1; } } }\nfunction main(): i32 { return 0; }\n", []string{"E052"}},
		// void return type: an empty body is fine (no E052 — falling off the
		// end is the normal exit), a bare `return;` is fine, and returning a
		// value is E002. Mirrors the Go checker's special handling of void.
		{"void-empty-ok", "function f(): void { }\nfunction main(): i32 { return 0; }\n", nil},
		{"void-bare-return-ok", "function f(): void { return; }\nfunction main(): i32 { return 0; }\n", nil},
		{"void-returns-value", "function f(): void { return 3; }\nfunction main(): i32 { return 0; }\n", []string{"E002"}},
		{"method-unknown-receiver", "function (r: Nope) m(): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"method-struct-receiver-ok", "struct P { x: i32 }\nfunction (p: P) m(): i32 { return p.x; }\nfunction main(): i32 { return 0; }\n", nil},
		{"method-builtin-receiver-ok", "function (n: i32) twice(): i32 { return n * 2; }\nfunction main(): i32 { return 0; }\n", nil},
		{"tuple-destructure-non-tuple", "function main(): i32 { let n = 5; let (a, b) = n; return 0; }\n", []string{"E024"}},
		{"tuple-destructure-ok", "function main(): i32 { let t = (1, 2); let (a, b) = t; return a + b; }\n", nil},
		{"cast-bool-to-i32", "function main(): i32 { let b: boolean = true; return b as i32; }\n", []string{"E033"}},
		{"cast-i32-to-bool", "function main(): i32 { let x: i32 = 1; let b: boolean = x as boolean; return 0; }\n", []string{"E033"}},
		{"cast-bool-to-string", "function main(): i32 { let b: boolean = true; let s: string = b as string; return 0; }\n", []string{"E033"}},
		{"cast-string-to-bool", "function main(): i32 { let s: string = \"x\"; let b: boolean = s as boolean; return 0; }\n", []string{"E033"}},
		{"cast-numeric-ok", "function main(): i32 { let x: i32 = 1; let y: f64 = x as f64; return 0; }\n", nil},
		{"cast-string-to-i32-ok", "function main(): i32 { let s: string = \"x\"; return s as i32; }\n", nil},
		{"cast-i32-to-string-e069", "function main(): i32 { let x: i32 = 1; let s: string = x as string; return 0; }\n", []string{"E069"}},
		{"cast-bool-to-bool-ok", "function main(): i32 { let b: boolean = true; let c: boolean = b as boolean; return 0; }\n", nil},
		{"cast-f64-to-string", "function main(): i32 { let f: f64 = 1.0; let s: string = f as string; return 0; }\n", []string{"E033"}},
		{"cast-string-to-f64", "function main(): i32 { let s: string = \"x\"; let f: f64 = s as f64; return 0; }\n", []string{"E033"}},
		{"cast-f64-to-i32-ok", "function main(): i32 { let f: f64 = 1.0; let x: i32 = f as i32; return 0; }\n", nil},
		{"cast-i32-to-f64-ok", "function main(): i32 { let x: i32 = 1; let y: f64 = x as f64; return 0; }\n", nil},
		// Numeric-literal settling (#6654): an unsuffixed integer literal reads
		// at the destination's type, so every one of these is clean on native
		// and had been an over-reject here — the self-host types every numeric
		// literal i32 and compared it directly against the declared type. One
		// row per position that got it wrong, plus the composite destinations
		// whose element types the settle has to recurse into.
		{"settle-var-init-ok", "function main(): i32 { let x: f64 = 1; return 0; }\n", nil},
		{"settle-assign-ok", "function main(): i32 { let x: f64 = 1.0; x = 2; return 0; }\n", nil},
		{"settle-return-ok", "function g(): f64 { return 1; }\nfunction main(): i32 { return 0; }\n", nil},
		{"settle-call-arg-ok", "function g(x: f64): i32 { return 1; }\nfunction main(): i32 { return g(1); }\n", nil},
		{"settle-method-arg-ok", "struct S { v: i32 }\nfunction (s: S) take(x: f64): i32 { return 1; }\nfunction main(): i32 { let s = S { v: 1 }; return s.take(1); }\n", nil},
		{"settle-struct-field-ok", "struct S { x: f64 }\nfunction main(): i32 { let s: S = S { x: 1 }; return 0; }\n", nil},
		{"settle-array-literal-ok", "function main(): i32 { let xs: f64[] = [1, 2]; return 0; }\n", nil},
		{"settle-tuple-literal-ok", "function main(): i32 { let t: (f64, i32) = (1, 2); return 0; }\n", nil},
		{"settle-nested-array-ok", "function main(): i32 { let xs: f64[][] = [[1], [2]]; return 0; }\n", nil},
		{"settle-struct-array-field-ok", "struct S { xs: f64[] }\nfunction main(): i32 { let s: S = S { xs: [1, 2] }; return 0; }\n", nil},
		{"settle-variant-payload-ok", "enum W { Wrap(f64), Empty }\nfunction f(): W { return Wrap(1); }\nfunction main(): i32 { return 0; }\n", nil},
		{"settle-f32-ok", "function main(): i32 { let x: f32 = 1; return 0; }\n", nil},
		// The expression set that settles: an unsuffixed literal, unary minus,
		// and the four arithmetic binaries over those. Same set native's
		// settleFloat descends.
		{"settle-unary-minus-ok", "function main(): i32 { let x: f64 = -5; return 0; }\n", nil},
		{"settle-arithmetic-ok", "function main(): i32 { let x: f64 = 2 * 3 + 1; return 0; }\n", nil},
		// A value-position if / match desugars into an immediately-invoked
		// closure typed from its arms, putting the literal one level below the
		// destination (#6679). The settle fans into the branches — every arm, an
		// `else if` chain, a branch with leading statements, and a nested one —
		// mirroring native's settleFloat IfExpr / MatchExpr / BlockExpr arms.
		{"settle-value-if-ok", "function main(): i32 { let x: f64 = if (1 < 2) { 1 } else { 2 }; return 0; }\n", nil},
		{"settle-value-match-ok", "enum E { A, B }\nfunction main(): i32 { let e: E = A; let x: f64 = match (e) { A => 1, B => 2 }; return 0; }\n", nil},
		{"settle-value-if-return-ok", "function g(): f64 { return if (1 < 2) { 1 } else { 2 }; }\nfunction main(): i32 { return 0; }\n", nil},
		{"settle-value-else-if-ok", "function main(): i32 { let x: f64 = if (1 < 2) { 1 } else if (2 < 3) { 2 } else { 3 }; return 0; }\n", nil},
		{"settle-value-if-block-tail-ok", "function main(): i32 { let x: f64 = if (1 < 2) { let q: i32 = 1; 1 } else { 2 }; return 0; }\n", nil},
		{"settle-value-if-nested-ok", "function main(): i32 { let x: f64 = if (1 < 2) { if (2 < 3) { 1 } else { 2 } } else { 3 }; return 0; }\n", nil},
		// Not settled: a branch whose type the pass cannot see keeps the whole
		// if unsettled, so the destination compare still reports it.
		{"settle-value-if-i32-arm", "function main(): i32 { let n: i32 = 1; let x: f64 = if (1 < 2) { n } else { 2 }; return 0; }\n", []string{"E003"}},
		{"settle-value-if-string-arm", "function main(): i32 { let x: f64 = if (1 < 2) { 1 } else { \"s\" }; return 0; }\n", []string{"E031"}},
		// Settling is literal-only, not an i32 -> f64 widening: native rejects
		// each of these too, and they are what stops the rule going too far. A
		// typed suffix has already picked its width, and an identifier is not a
		// literal however it was initialised.
		{"settle-not-an-ident", "function main(): i32 { let a: i32 = 1; let x: f64 = a; return 0; }\n", []string{"E003"}},
		{"settle-not-a-suffixed-literal", "function main(): i32 { let x: f64 = 1i64; return 0; }\n", []string{"E003"}},
		{"settle-variant-payload-ident", "enum W { Wrap(f64), Empty }\nfunction f(a: i32): W { return Wrap(a); }\nfunction main(): i32 { return 0; }\n", []string{"E036"}},
		{"settle-tuple-bad-element", "function main(): i32 { let t: (f64, i32) = (1, \"x\"); return 0; }\n", []string{"E003"}},
		{"settle-array-bad-element", "function main(): i32 { let xs: f64[] = [1, \"x\"]; return 0; }\n", []string{"E034"}},
		{"settle-not-a-string", "function main(): i32 { let x: f64 = \"a\"; return 0; }\n", []string{"E003"}},
		// An unsuffixed integer literal beside a concrete float operand reads at
		// that float, on either side and for comparisons too (native's
		// settleNumeric before requireFloat). An i32 operand is not a literal.
		{"settle-binary-sub-f64-ok", "function main(): i32 { let x: f64 = 100.5f64; let y: f64 = x - 100; if (y == 0.5f64) { return 0; } return 1; }\n", nil},
		{"settle-binary-mul-f32-ok", "function main(): i32 { let r: f32 = 1.5f32; let s: f32 = r * 2; if (s == 3.0f32) { return 0; } return 1; }\n", nil},
		{"settle-binary-left-literal-ok", "function main(): i32 { let r: f64 = 1.5f64; let s: f64 = 3 - r; if (s > 1) { return 0; } return 1; }\n", nil},
		{"settle-binary-guard-compare-ok", "enum Shape { Circle(f32), Square(f32) }\nfunction classify(s: Shape): i32 { match (s) { Circle(r) when r <= 0 => { return 1; }, Circle(_) => { return 2; }, Square(_) => { return 3; } } return 0; }\nfunction main(): i32 { if (classify(Circle(0.0f32)) != 1) { return 1; } if (classify(Circle(2.0f32)) != 2) { return 2; } return 0; }\n", nil},
		{"settle-binary-not-an-ident", "function main(): i32 { let n: i32 = 2; let r: f64 = 1.5f64; let s: f64 = r * n; return 0; }\n", []string{"E009"}},
		{"field-assign", "struct P { x: i32 }\nfunction main(): i32 { let p: P = P { x: 1 }; p.x = 5; return p.x; }\n", []string{"E048"}},
		{"field-compound-assign", "struct P { x: i32 }\nfunction main(): i32 { let p: P = P { x: 1 }; p.x += 5; return p.x; }\n", []string{"E048"}},
		{"nested-field-assign", "struct Q { a: i32 }\nstruct P { q: Q }\nfunction main(): i32 { let p: P = P { q: Q { a: 1 } }; p.q.a = 9; return 0; }\n", []string{"E048"}},
		{"index-assign-e056", "function main(): i32 { let a = [1, 2, 3]; a[0] = 9; return a[0]; }\n", []string{"E056"}},
		{"local-reassign-ok", "function main(): i32 { let x: i32 = 1; x = 5; return x; }\n", nil},
		{"struct-update-ok", "struct P { x: i32 }\nfunction main(): i32 { let p: P = P { x: 1 }; p = P { ...p, x: 5 }; return p.x; }\n", nil},
		{"value-undefined", "function main(): i32 { return z; }\n", []string{"E001"}},
		{"value-defined-ok", "function main(): i32 { let z: i32 = 5; return z; }\n", nil},
		// A bare STRUCT type name in value position is E001 (you construct with
		// `P { … }`); the struct literal and a field read stay clean.
		{"struct-name-as-value", "struct P { x: i32 }\nfunction main(): i32 { return P; }\n", []string{"E001"}},
		{"struct-name-as-value-var", "struct P { x: i32 }\nfunction main(): i32 { let q = P; return 0; }\n", []string{"E001"}},
		{"struct-literal-not-value-ok", "struct P { x: i32 }\nfunction main(): i32 { let p = P { x: 1 }; return p.x; }\n", nil},
		// A payload-bearing variant referenced bare (a union member, or an enum
		// variant with a payload) is E036 — it must be constructed/called. A
		// nullary variant, a constructor call, and a struct literal stay clean.
		{"payload-variant-bare", "enum E { A(i32), B }\nfunction f(): E { return A; }\nfunction main(): i32 { return 0; }\n", []string{"E036"}},
		{"union-member-bare", "struct P { x: i32 }\nstruct Q { y: i32 }\ntype U = P | Q;\nfunction f(): U { return P; }\nfunction main(): i32 { return 0; }\n", []string{"E036"}},
		{"payload-variant-call-ok", "enum E { A(i32), B }\nfunction f(): E { return A(5); }\nfunction main(): i32 { return 0; }\n", nil},
		// The QUALIFIED constructor call (#6657). Its callee is a field access,
		// so it used to reach method dispatch, resolve `W` as a receiver, find
		// nothing, and report E001 — on the valid call as much as the ill-typed
		// ones. It now goes through the same argument checks as the bare form,
		// which is what these rows pin: same codes as `Wrap(...)` on every shape.
		{"qualified-ctor-ok", "enum W { Wrap(i32), Empty }\nfunction f(): W { return W.Wrap(1); }\nfunction main(): i32 { return 0; }\n", nil},
		{"qualified-ctor-builtin-ok", "function f(): Option[i32] { return Option.Some(3); }\nfunction main(): i32 { return 0; }\n", nil},
		{"qualified-ctor-payload-type", "enum W { Wrap(i32), Empty }\nfunction f(): W { return W.Wrap(\"x\"); }\nfunction main(): i32 { return 0; }\n", []string{"E036"}},
		{"qualified-ctor-arity", "enum W { Wrap(i32), Empty }\nfunction f(): W { return W.Wrap(1, 2); }\nfunction main(): i32 { return 0; }\n", []string{"E036"}},
		{"qualified-ctor-no-such-variant", "enum W { Wrap(i32), Empty }\nfunction f(): W { return W.Nope(1); }\nfunction main(): i32 { return 0; }\n", []string{"E036"}},
		// The call now types to its enum, so the destination check sees a real
		// type instead of unknown and stops under-reporting.
		{"qualified-ctor-destination", "enum W { Wrap(i32), Empty }\nfunction main(): i32 { let x: i32 = W.Wrap(1); return 0; }\n", []string{"E003"}},
		// Neighbours the routing must not swallow: the nullary qualified
		// reference (not a call), a bad qualified reference, and an ordinary
		// method call whose receiver really is a value.
		{"qualified-nullary-ok", "enum W { Wrap(i32), Empty }\nfunction f(): W { return W.Empty; }\nfunction main(): i32 { return 0; }\n", nil},
		{"qualified-bad-ref", "enum W { Wrap(i32), Empty }\nfunction f(): i32 { let x = W.Nope; return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E036"}},
		{"qualified-not-a-method-call", "struct S { v: i32 }\nfunction (s: S) take(x: i32): i32 { return x; }\nfunction main(): i32 { let s = S { v: 1 }; return s.take(1); }\n", nil},
		// A constructor call's ARGUMENTS against the declared payloads (#6650):
		// wrong type, wrong type in a later slot, and either arity direction.
		// A builtin variant has no backing struct, so its payload type is
		// unknown and stays unchecked (E002 from the destination annotation is
		// what catches `Some("x")` for an `Option[i32]`); a generic payload
		// erases to unknown likewise. An integer literal in an f64 slot is
		// accepted — the self-host types every numeric literal i32.
		{"variant-payload-type", "enum W { Wrap(i32), Empty }\nfunction f(): W { return Wrap(\"x\"); }\nfunction main(): i32 { return 0; }\n", []string{"E036"}},
		{"variant-payload-type-second", "enum R { Rect(i32, i32), Empty }\nfunction f(): R { return Rect(1, \"x\"); }\nfunction main(): i32 { return 0; }\n", []string{"E036"}},
		{"variant-payload-type-struct", "struct S { x: i32 }\nstruct T2 { y: i32 }\nenum W { Wrap(S), Empty }\nfunction f(): W { return Wrap(T2 { y: 1 }); }\nfunction main(): i32 { return 0; }\n", []string{"E036"}},
		{"variant-ctor-arity-over", "enum W { Wrap(i32), Empty }\nfunction f(): W { return Wrap(1, 2); }\nfunction main(): i32 { return 0; }\n", []string{"E036"}},
		{"variant-ctor-arity-under", "enum R { Rect(i32, i32), Empty }\nfunction f(): R { return Rect(1); }\nfunction main(): i32 { return 0; }\n", []string{"E036"}},
		{"variant-payload-multi-ok", "enum R { Rect(i32, i32), Empty }\nfunction f(): R { return Rect(1, 2); }\nfunction main(): i32 { return 0; }\n", nil},
		{"variant-payload-widths-ok", "enum W { Wrap(i64), Empty }\nfunction f(): W { return Wrap(5); }\nfunction main(): i32 { return 0; }\n", nil},
		{"variant-payload-float-literal-ok", "enum W { Wrap(f64), Empty }\nfunction f(): W { return Wrap(1); }\nfunction main(): i32 { return 0; }\n", nil},
		{"variant-payload-generic-ok", "enum Box[T] { Wrap(T), Empty }\nfunction f(): Box[i32] { return Wrap(5); }\nfunction main(): i32 { return 0; }\n", nil},
		{"variant-payload-builtin-ok", "function f(): Option[i32] { return Some(3); }\nfunction main(): i32 { return 0; }\n", nil},
		{"variant-payload-struct-ok", "struct S { x: i32 }\nenum W { Wrap(S), Empty }\nfunction f(): W { return Wrap(S { x: 1 }); }\nfunction main(): i32 { return 0; }\n", nil},
		{"variant-payload-array-ok", "enum W { Wrap(i32[]), Empty }\nfunction f(): W { return Wrap([1, 2]); }\nfunction main(): i32 { return 0; }\n", nil},
		// Both codes with the set written out: a void argument to a declared
		// enum's typed payload is E072 (not a value) AND E036 (wrong type).
		// The differential cannot tell "both sides emit both" from "neither
		// emits anything"; this row can.
		{"variant-payload-void", "function nothing(): void { }\nenum W { Wrap(i32), Empty }\nfunction f(): W { return Wrap(nothing()); }\nfunction main(): i32 { return 0; }\n", []string{"E036", "E072"}},
		{"nullary-variant-bare-ok", "enum E { A, B }\nfunction f(): E { return A; }\nfunction main(): i32 { return 0; }\n", nil},
		{"value-param-ok", "function f(a: i32): i32 { return a; }\nfunction main(): i32 { return f(1); }\n", nil},
		{"value-function-as-value-ok", "function g(): i32 { return 1; }\nfunction run(fn: () => i32): i32 { return fn(); }\nfunction main(): i32 { return run(g); }\n", nil},
		{"value-enum-variant-ok", "enum E { A, B }\nfunction main(): i32 { let e: E = A; return 0; }\n", nil},
		{"value-builtin-variant-none-ok", "function f(): Option[i32] { return None; }\nfunction main(): i32 { return 0; }\n", nil},
		{"value-loop-var-ok", "function main(): i32 { let xs = [1, 2, 3]; let t = 0; for x in xs { t = t + x; } return t; }\n", nil},
		{"value-match-payload-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { let o: O = Nil; match (o) { Has(v) => { return v; }, Nil => { return 0; } } }\n", nil},
		{"assign-undefined-target", "function main(): i32 { y = 5; return 0; }\n", []string{"E001"}},
		{"assign-defined-target-ok", "function main(): i32 { let x: i32 = 1; x = 5; return x; }\n", nil},
		{"assign-param-target-ok", "function f(a: i32): i32 { a = 9; return a; }\nfunction main(): i32 { return f(1); }\n", nil},
		{"assign-loop-var-target-ok", "function main(): i32 { let xs = [1, 2, 3]; for x in xs { x = 9; } return 0; }\n", nil},
		{"assign-match-payload-target-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { let o: O = Nil; match (o) { Has(v) => { v = 7; }, Nil => { } } return 0; }\n", nil},
		{"assign-destructure-target-ok", "function main(): i32 { let t = (1, 2); let (a, b) = t; a = 9; return a + b; }\n", nil},
		{"try-on-i32", "function f(): Option[i32] { let x: i32 = 5; return x?; }\nfunction main(): i32 { return 0; }\n", []string{"E042"}},
		{"try-on-string", "function f(): Option[i32] { let s: string = \"x\"; return s?; }\nfunction main(): i32 { return 0; }\n", []string{"E042"}},
		{"try-on-option-ok", "function g(): Option[i32] { return Some(1); }\nfunction f(): Option[i32] { let o: Option[i32] = g(); let v: i32 = o?; return Some(v); }\nfunction main(): i32 { return 0; }\n", nil},
		// `0o` / `0b` literals are numerals like any other (#9091): in range
		// clean, out of range E047, suffixed or not.
		{"radix-literal-clean", "function main(): i32 { let m: i32 = 0o755; let b: u8 = 0b11111111u8; return m + (b as i32); }\n", nil},
		{"radix-literal-range", "function main(): i32 { let x: u8 = 0o777; return x as i32; }\n", []string{"E047"}},
		{"radix-literal-suffix-range", "function main(): i32 { let y: u8 = 0b111111111u8; return y as i32; }\n", []string{"E047"}},
		// An unannotated `?` binding takes the success payload's type.
		{"try-binding-typed-sink", "function g(): Option[string] { return Some(\"a\"); }\nfunction f(): Option[i32] { let s = g()?; let n: i32 = s; return Some(n); }\nfunction main(): i32 { return 0; }\n", []string{"E003"}},
		{"try-binding-typed-clean", "function g(): Result[string, i32] { return Ok(\"a\"); }\nfunction f(): Result[i32, i32] { let s = g()?; return Ok(s.len()); }\nfunction main(): i32 { return 0; }\n", nil},
		// E042 return-shape (#4363 item 1): `?` on a known Option/Result
		// operand inside a function whose declared return type is a known
		// primitive draws the return-shape E042 ("requires the surrounding
		// function to return Option[_]/Result[_, E]"), matching native. The
		// matching-return shapes stay clean, including inside a lambda whose
		// own declared return supplies the context (not the enclosing fn's).
		{"try-option-ret-i32", "function f(): i32 { let o: Option[i32] = Some(1); return o?; }\nfunction main(): i32 { return 0; }\n", []string{"E042"}},
		{"try-result-ret-i32", "function get(): Result[i32, string] { return Ok(3); }\nfunction f(): i32 { return get()?; }\nfunction main(): i32 { return 0; }\n", []string{"E042"}},
		{"try-option-ret-string", "function f(): string { return Some(3)?; }\nfunction main(): i32 { return 0; }\n", []string{"E042"}},
		{"try-option-ret-bool", "function f(): boolean { let o: Option[i32] = Some(1); let v: i32 = o?; return v > 0; }\nfunction main(): i32 { return 0; }\n", []string{"E042"}},
		{"try-result-ret-ok", "function get(): Result[i32, string] { return Ok(3); }\nfunction f(): Result[i32, string] { let v: i32 = get()?; return Ok(v + 1); }\nfunction main(): i32 { return 0; }\n", nil},
		{"try-lambda-ret-i32", "function f(): Option[i32] {\n    let g = (): i32 => { let o: Option[i32] = Some(1); let v: i32 = o?; return v; };\n    return Some(1);\n}\nfunction main(): i32 { return 0; }\n", []string{"E042"}},
		{"try-lambda-ret-option-ok", "function f(): i32 {\n    let g = (): Option[i32] => { let o: Option[i32] = Some(1); let v: i32 = o?; return Some(v); };\n    return 2;\n}\nfunction main(): i32 { return 0; }\n", nil},
		// E042 not-a-`?`-type on a NON-primitive operand (#9331). The rule
		// used to fire only for a known scalar, so an unmarked enum — which
		// types to a union here — was accepted where native refuses it, and
		// the self-host went on to lower it, since its `?` admits a marked
		// enum by SHAPE rather than by marker. A `type X = A | B` alias is
		// refused for the same reason and with the same message on both.
		{"try-on-unmarked-enum", "enum Flag { On(i32), Off }\nfunction pick(f: Flag): Flag { let v: i32 = f?; return On(v + 1); }\nfunction main(): i32 { return 0; }\n", []string{"E042"}},
		{"try-on-union-alias", "struct A { n: i32 }\nstruct B { n: i32 }\ntype Shape = A | B;\nfunction f(x: Shape): i32 { let y: i32 = x?; return y; }\nfunction main(): i32 { return 0; }\n", []string{"E042"}},
		{"try-on-marked-enum-ok", "@try\nenum MyOpt { Got(i32), Nope }\nfunction pick(f: MyOpt): MyOpt { let v: i32 = f?; return Got(v + 1); }\nfunction main(): i32 { return 0; }\n", nil},
		// E079 through a VALUE BLOCK (#9553). A value block desugars to a
		// zero-arg call of a zero-param lambda, and the lowering inlines it rather
		// than lowering a function, so a `?` inside one still leaves the
		// ENCLOSING function and is E079 — where the same `?` inside a real
		// lambda is an ordinary use. The existing e079-defer-try-op row only
		// covers the direct spelling, which is how every shape below went
		// unnoticed reporting E042 alone.
		{"e079-defer-match-expr", "function g(v: i32): Option[i32] { if (v < 100) { return Some(v + 1); } return None; }\nfunction f(k: i32): i32 {\n  let n: i32 = 1;\n  defer n = (match (k) { 0 => g(n)?, _ => 7 });\n  return n;\n}\nfunction main(): i32 { return f(0); }\n", []string{"E042", "E079"}},
		{"e079-defer-if-expr", "function g(v: i32): Option[i32] { if (v < 100) { return Some(v + 1); } return None; }\nfunction f(k: i32): i32 {\n  let n: i32 = 1;\n  defer n = (if (k == 0) { g(n)? } else { 7 });\n  return n;\n}\nfunction main(): i32 { return f(0); }\n", []string{"E042", "E079"}},
		{"e079-defer-block", "function g(v: i32): Option[i32] { if (v < 100) { return Some(v + 1); } return None; }\nfunction f(k: i32): i32 {\n  let n: i32 = 1;\n  defer { n = (match (k) { 0 => g(n)?, _ => 7 }); }\n  return n;\n}\nfunction main(): i32 { return f(0); }\n", []string{"E042", "E079"}},
		{"e079-errdefer-match-expr", "function g(v: i32): Option[i32] { if (v < 100) { return Some(v + 1); } return None; }\nfunction f(k: i32): i32 {\n  let n: i32 = 1;\n  errdefer n = (match (k) { 0 => g(n)?, _ => 7 });\n  return n;\n}\nfunction main(): i32 { return f(0); }\n", []string{"E042", "E079"}},
		{"e079-defer-nested-value-blocks", "function g(v: i32): Option[i32] { if (v < 100) { return Some(v + 1); } return None; }\nfunction f(k: i32): i32 {\n  let n: i32 = 1;\n  defer n = (match (k) { 0 => (if (k == 0) { g(n)? } else { 3 }), _ => 7 });\n  return n;\n}\nfunction main(): i32 { return f(0); }\n", []string{"E042", "E079"}},
		// E021 generic-bound conformance is an `impl` or a `@derive`, never a
		// receiver method that merely has the right name (#9486). The method-set
		// arm was there to reach the derive case, which the derive is now asked
		// about directly; it also admitted an INHERENT method as a trait impl,
		// which native refuses and the lowering had no dispatch for — so the
		// program ran and answered wrongly rather than being refused.
		{"e021-inherent-method-is-not-an-impl", "trait Feed[T] { function head(self: Self): T; }\nstruct Wide { v: f64 }\nfunction (w: Wide) head(): f64 { return w.v; }\nfunction first[T, I: Feed[T]](it: I): T { return it.head(); }\nfunction main(): i32 { return first(Wide { v: 6.5 }) as i32; }\n", []string{"E021"}},
		{"e021-no-method-at-all", "trait Feed[T] { function head(self: Self): T; }\nstruct Wide { v: f64 }\nfunction first[T, I: Feed[T]](it: I): T { return it.head(); }\nfunction main(): i32 { return first(Wide { v: 6.5 }) as i32; }\n", []string{"E021"}},
		{"e021-impl-args-disagree-with-destination", "trait Feed[T] { function head(self: Self): T; }\nstruct Wide { v: f64 }\nimpl Feed[f64] for Wide { function head(self: Self): f64 { return self.v; } }\nfunction first[T, I: Feed[T]](it: I): T { return it.head(); }\nfunction main(): i32 { let s: string = first(Wide { v: 6.5 }); return 0; }\n", []string{"E021"}},
		{"e021-impl-typed-result-e003", "trait Feed[T] { function head(self: Self): T; }\nstruct Wide { v: f64 }\nimpl Feed[f64] for Wide { function head(self: Self): f64 { return self.v; } }\nfunction first[T, I: Feed[T]](it: I): T { return it.head(); }\nfunction main(): i32 { let d = first(Wide { v: 6.5 }); let s: string = d; return 0; }\n", []string{"E003"}},
		{"e021-impl-backed-ok", "trait Feed[T] { function head(self: Self): T; }\nstruct Wide { v: f64 }\nimpl Feed[f64] for Wide { function head(self: Self): f64 { return self.v; } }\nfunction first[T, I: Feed[T]](it: I): T { return it.head(); }\nfunction main(): i32 { let d: f64 = first(Wide { v: 6.5 }); return d as i32; }\n", nil},
		// Cell[T]'s two methods are builtins, so they are in neither the
		// method table nor the struct table and the ordinary method path
		// resolved nothing — a call was accepted whatever it was handed,
		// a lambda included (#9518). Arity counts the RECEIVER, matching
		// native, which models these as functions carrying the cell, so
		// `c.set()` is one argument of the two it wants; a bad value on
		// `set` is numbered among the written arguments, "argument 1".
		{"cell-set-wrong-type", "function f(c: Cell[i32]): i32 { c.set(\"hi\"); return 0; }\nfunction main(): i32 { let c: Cell[i32] = cell_new(0); return f(c); }\n", []string{"E038"}},
		{"cell-set-lambda-arg", "function f(c: Cell[i32]): i32 { c.set((x: i32) => x + 1); return 0; }\nfunction main(): i32 { let c: Cell[i32] = cell_new(0); return f(c); }\n", []string{"E038"}},
		{"cell-set-too-few", "function f(c: Cell[i32]): i32 { c.set(); return 0; }\nfunction main(): i32 { let c: Cell[i32] = cell_new(0); return f(c); }\n", []string{"E004"}},
		{"cell-set-too-many", "function f(c: Cell[i32]): i32 { c.set(1, 2); return 0; }\nfunction main(): i32 { let c: Cell[i32] = cell_new(0); return f(c); }\n", []string{"E004"}},
		{"cell-get-too-many", "function f(c: Cell[i32]): i32 { let v: i32 = c.get(3); return 0; }\nfunction main(): i32 { let c: Cell[i32] = cell_new(0); return f(c); }\n", []string{"E004"}},
		{"cell-unknown-method", "function f(c: Cell[i32]): i32 { c.nosuch(); return 0; }\nfunction main(): i32 { let c: Cell[i32] = cell_new(0); return f(c); }\n", []string{"E043"}},
		{"cell-set-ok", "function f(c: Cell[i32]): i32 { c.set(5); return 0; }\nfunction main(): i32 { let c: Cell[i32] = cell_new(0); return f(c); }\n", nil},
		{"cell-get-ok", "function f(c: Cell[i32]): i32 { let v: i32 = c.get(); return 0; }\nfunction main(): i32 { let c: Cell[i32] = cell_new(0); return f(c); }\n", nil},
		{"callee-undefined", "function main(): i32 { return foo(1); }\n", []string{"E001"}},
		{"callee-user-fn-ok", "function g(): i32 { return 1; }\nfunction main(): i32 { return g(); }\n", nil},
		{"callee-builtin-ok", "function main(): i32 { print(\"hi\"); return 0; }\n", nil},
		{"callee-variant-ctor-ok", "function f(): Option[i32] { return Some(1); }\nfunction main(): i32 { return 0; }\n", nil},
		{"callee-closure-ok", "function main(): i32 { let f = (x: i32): i32 => { return x; }; return f(7); }\n", nil},
		{"value-builtin-as-value-ok", "function main(): i32 { let w = write; return 0; }\n", nil},
		{"shadow-option", "enum Option { A, B }\nfunction main(): i32 { return 0; }\n", []string{"E010"}},
		{"shadow-result", "enum Result { A, B }\nfunction main(): i32 { return 0; }\n", []string{"E010"}},
		{"shadow-ioerror", "enum IoError { A, B }\nfunction main(): i32 { return 0; }\n", []string{"E010"}},
		{"shadow-jsonvalue", "enum JsonValue { A, B }\nfunction main(): i32 { return 0; }\n", []string{"E010"}},
		// A value if's literal arm widens to the width another literal arm needs,
		// and a typed arm pins it, so the literal must fit (#10859).
		{"value-if-literal-arms-widen", "function main(): i32 {\n  let r: i64 = 5;\n  let p = if (r > 0) { (2, 4611686018427387905) } else { (3, 4) };\n  let q = if (r > 0) { 4 } else { 4611686018427387905 };\n  return p.0;\n}\n", nil},
		{"value-if-typed-arm-pins-a-tuple-literal", "function main(): i32 {\n  let r: i64 = 5;\n  let n: i32 = 4;\n  let p = if (r > 0) { (2, n) } else { (3, 4611686018427387905) };\n  return p.0;\n}\n", []string{"E047"}},
		{"value-match-typed-arm-pins-a-literal", "function main(): i32 {\n  let n: i32 = 4;\n  let q = match (n) { 4 => { n }, _ => { 4611686018427387905 } };\n  return q;\n}\n", []string{"E047"}},
		// A reserved name is reserved whatever kind takes it (#10855).
		{"enum-takes-builtin-struct-name", "import \"std/string\";\nenum Span { Empty, Wide(f64, string) }\nfunction f(s: Span): i32 {\n  match (s) { Wide(d, t) => { return (d * 4.0) as i32 + t.len(); }, Empty => { return 0; } }\n}\nfunction main(): i32 { return f(Wide(1.0, \"ab\")); }\n", []string{"E010"}},
		{"struct-takes-builtin-enum-name", "struct Option { n: i32 }\nfunction main(): i32 { return 0; }\n", []string{"E010"}},
		// A binding or a top-level function named like a builtin constructor
		// shadows it, so the call is a call of that and its result is judged
		// against the destination like any other (#10394).
		{"ctor-shadowed-by-function-arg", "function Some(n: i32): i32 { return n; }\nfunction takes(o: Option[i64]): i32 { return 0; }\nfunction main(): i32 { return takes(Some(5)); }\n", []string{"E038"}},
		{"ctor-shadowed-by-local-arg", "function takes(r: Result[i64, i32]): i32 { return 0; }\nfunction main(): i32 { let Err: (i64) => i32 = (v: i64) => 1; return takes(Err(5)); }\n", []string{"E038"}},
		{"ctor-shadowed-by-local-init", "function main(): i32 { let Some = (v: i64): i32 => 1; let o: Option[i64] = Some(1); return 0; }\n", []string{"E003"}},
		{"ctor-shadowed-by-function-ok", "function Some(n: i32): i32 { return n + 1; }\nfunction main(): i32 { let x: i32 = Some(5); return x - 6; }\n", nil},
		// A generic call nested in another one's argument takes the reading
		// position's width through both (#10508).
		{"nested-generic-call-destination-ok", "function id[T](a: T): T { return a; }\nfunction main(): i32 { let z: i64 = id(id(5000000000)); return (z / 1000000000) as i32; }\n", nil},
		{"nested-generic-call-argument-ok", "function id[T](a: T): T { return a; }\nfunction take(x: i64): i32 { return x as i32; }\nfunction main(): i32 { return take(id(id(1))); }\n", nil},
		{"nested-generic-call-compare-ok", "function id[T](a: T): T { return a; }\nfunction main(): i32 { if (id(id(1)) == 4611686018427387904) { return 1; } return 0; }\n", nil},
		{"nested-generic-call-out-of-range", "function id[T](a: T): T { return a; }\nfunction main(): i32 { let z: u8 = id(id(300)); return 0; }\n", []string{"E047"}},
		// A literal local beside a wide literal settles at the reading
		// position, and defaults to i64 with none (#10595); i32-min is i32.
		{"wide-literal-local-at-param-ok", "function wide(n: u64): u64 { return n / 1000000000u64; }\nfunction main(): i32 { let k = 3; let w: u64 = wide(k * 3000000000); return w as i32; }\n", nil},
		{"wide-literal-local-block-tail-ok", "function wide(n: u64): u64 { return n / 1000000000u64; }\nfunction main(): i32 { let w: u64 = wide({ let k = 3; k * 3000000000 }); return w as i32; }\n", nil},
		{"wide-literal-local-default-i64", "function main(): i32 { let k = 3; let x = k * 3000000000; let y: i32 = x; return 0; }\n", []string{"E003"}},
		{"wide-literal-local-at-i32-param", "function f(n: i32): i32 { return n; }\nfunction main(): i32 { let k = 3; return f(k * 3000000000); }\n", []string{"E047"}},
		// `Some(lit)?` reads its literal at the `?`'s destination, as native's
		// settleNumeric TryOp arm does (#10614).
		{"try-some-literal-settles-ok", "function h(): Option[i32] { let v: u8 = Some(200)?; let w: i64 = Some(5)?; let f: f32 = Some(3.5)?; return Some(v as i32 + w as i32 + f as i32); }\nfunction main(): i32 { return 0; }\n", nil},
		{"try-some-literal-out-of-range", "function h(): Option[i32] { let v: u8 = Some(300)?; return Some(v as i32); }\nfunction main(): i32 { return 0; }\n", []string{"E047"}},
		{"try-some-typed-payload", "function h(): Option[i32] { let x: i32 = 4; let v: f32 = Some(x)?; return Some(1); }\nfunction main(): i32 { return 0; }\n", []string{"E003"}},
		// Cell.set keeps its argument, so a str view is not lent (#10702).
		{"cell-set-str-view", "function main(): i32 { let b: string = \"abcdefgh\"; let u: str = slice_unchecked(b, 0, 8); let c: Cell[string] = cell_new(b); c.set(u); return c.get().len(); }\n", []string{"E038"}},
		// A return type naming no declared type takes no value (#10842).
		{"unknown-return-type-mismatch", "function g(): Undef { return 1; }\nfunction main(): i32 { return 0; }\n", []string{"E002", "E064"}},
		// An enum variant's name is no type (#10843).
		{"variant-name-as-field-type", "enum X { P, Q }\nstruct H { f: P }\nfunction main(): i32 { return 0; }\n", []string{"E064"}},
		{"variant-name-as-param-type", "enum X { P, Q }\nfunction g(p: P): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E064"}},
		// A str[] destination types its literal, so a string literal widens
		// beside a view; with none the elements must still agree (#10889).
		{"str-array-literal-mixed-ok", "function main(): i32 { let owned: string = \"ab\" + \"cd\"; let s: str = slice_unchecked(owned, 1, 3); let xs: str[] = [\"x\", s]; let ys: str[] = [s, \"y\"]; return xs.len() + ys.len(); }\n", nil},
		{"str-array-literal-non-string", "function main(): i32 { let owned: string = \"ab\" + \"cd\"; let s: str = slice_unchecked(owned, 1, 3); let xs: str[] = [\"x\", 5]; return xs.len(); }\n", []string{"E034"}},
		{"untyped-array-literal-mixed-str", "function main(): i32 { let owned: string = \"ab\" + \"cd\"; let s: str = slice_unchecked(owned, 1, 3); let xs = [\"x\", s]; return xs.len(); }\n", []string{"E034"}},
		{"i32-min-literal-local-ok", "function main(): i32 { let k = -2147483648; return k + 2147483647 + 1; }\n", nil},
		{"ctor-builtin-settles-ok", "function takes(o: Option[i64]): i32 { return 0; }\nfunction main(): i32 { return takes(Some(40)) + takes(Option.Some(1)); }\n", nil},
		{"enum-non-reserved-ok", "enum Color { Red, Green }\nfunction main(): i32 { return 0; }\n", nil},
		// Generic functions: a concrete argument must NOT be flagged against
		// the opaque type parameter (E038 false-positive guard).
		{"generic-call-infer-ok", "function id[T](x: T): T { return x; }\nfunction main(): i32 { return id(5); }\n", nil},
		{"generic-call-two-params-ok", "function fst[A, B](a: A, b: B): A { return a; }\nfunction main(): i32 { return fst(1, 2); }\n", nil},
		// A non-generic argument-type mismatch still fires E038 (regression
		// guard that the fix didn't disable the check).
		{"nongeneric-arg-mismatch", "function f(a: string): i32 { return 0; }\nfunction main(): i32 { return f(5); }\n", []string{"E038"}},
		// E040: explicit generic call-site type-argument arity.
		{"type-arg-too-many", "function id[T](x: T): T { return x; }\nfunction main(): i32 { return id[i32, i32](5); }\n", []string{"E040"}},
		{"type-arg-too-few", "function pair[A, B](a: A, b: B): A { return a; }\nfunction main(): i32 { return pair[i32](5, 6); }\n", []string{"E040"}},
		// E040: a type parameter nothing binds (#10507). An empty array
		// literal binds none (#10499); a later argument or the destination can.
		{"type-param-unbound-empty-array", "function first[T](own x: T, n: i32): i32 { return n; }\nfunction main(): i32 { return first([], 3); }\n", []string{"E040"}},
		{"type-param-unbound-no-args", "function mk[T](): i32 { return 1; }\nfunction main(): i32 { return mk(); }\n", []string{"E040"}},
		{"type-param-explicit", "function mk[T](): i32 { return 1; }\nfunction main(): i32 { return mk[i32](); }\n", nil},
		// A receiver binds only the variables its declared type names, so on a
		// concrete receiver the method's own parameter is unbound (#10991).
		{"method-type-param-unbound-array", "function (xs: u8[]) count[T](values: T[]): i32 { return values.len() + xs.len(); }\nfunction main(): i32 { let b: u8[] = [1 as u8]; return b.count([]); }\n", []string{"E040"}},
		{"method-type-param-unbound-view", "function (xs: [u8]) take[T](values: T[]): u8 { return xs[0]; }\nfunction main(): i32 { let b: [u8] = \"hi\".as_bytes(); return b.take([]) as i32; }\n", []string{"E040"}},
		{"method-type-param-unbound-string", "function (s: string) count[T](values: T[]): i32 { return values.len() + s.len(); }\nfunction main(): i32 { let s: string = \"ab\"; return s.count([]); }\n", []string{"E040"}},
		{"method-type-param-unbound-struct", "struct P { v: i32 }\nfunction (p: P) count[T](values: T[]): i32 { return values.len() + p.v; }\nfunction main(): i32 { let p: P = P { v: 5 }; return p.count([]); }\n", []string{"E040"}},
		{"method-type-param-unbound-generic-receiver", "struct Box[U] { v: U }\nfunction (b: Box[U]) count[T](values: T[]): i32 { return values.len(); }\nfunction main(): i32 { let b: Box[i32] = Box { v: 1 }; return b.count([]); }\n", []string{"E040"}},
		{"method-type-param-unbound-i32", "function (n: i32) count[T](values: T[]): i32 { return values.len(); }\nfunction main(): i32 { let x: i32 = 5; return x.count([]); }\n", []string{"E040"}},
		{"method-type-param-unbound-i64", "function (n: i64) count[T](values: T[]): i32 { return values.len(); }\nfunction main(): i32 { let x: i64 = 5 as i64; return x.count([]); }\n", []string{"E040"}},
		{"method-type-param-unbound-u8", "function (n: u8) count[T](values: T[]): i32 { return values.len(); }\nfunction main(): i32 { let x: u8 = 5 as u8; return x.count([]); }\n", []string{"E040"}},
		{"method-type-param-unbound-f64", "function (n: f64) count[T](values: T[]): i32 { return values.len(); }\nfunction main(): i32 { let x: f64 = 2.5; return x.count([]); }\n", []string{"E040"}},
		{"method-type-param-unbound-boolean", "function (n: boolean) count[T](values: T[]): i32 { return values.len(); }\nfunction main(): i32 { let x: boolean = true; return x.count([]); }\n", []string{"E040"}},
		{"method-type-param-unbound-char", "function (n: char) count[T](values: T[]): i32 { return values.len(); }\nfunction main(): i32 { let x: char = 'a'; return x.count([]); }\n", []string{"E040"}},
		{"method-type-param-bound-by-arg", "function (xs: u8[]) count[T](values: T[]): i32 { return values.len() + xs.len(); }\nfunction main(): i32 { let b: u8[] = [1 as u8]; return b.count([2 as u8]) + b.count[i32]([]); }\n", nil},
		{"method-receiver-binds-its-own", "function (xs: T[]) first(): T { return xs[0]; }\nfunction main(): i32 { let b: u8[] = [7 as u8]; return b.first() as i32; }\n", nil},
		{"type-param-later-arg-binds", "function fold[T](x: i32, own acc: T, f: (i32, own T) => T): T { return f(x, acc); }\nfunction add(x: i32, own acc: string[]): string[] { return acc.append(\"a\"); }\nfunction main(): i32 {\n    let r: string[] = fold(1, [], add);\n    return r.len();\n}\n", nil},
		// A parameter only a bound names is bound through the impl, never by an
		// argument, so the rule leaves it alone.
		{"type-param-bound-only", "trait Feed[T] { function head(self: Self): T; }\nstruct Wide { v: f64 }\nimpl Feed[f64] for Wide { function head(self: Self): f64 { return self.v; } }\nfunction tally[T, I: Feed[T]](it: I): i32 { return 0; }\nfunction main(): i32 { return tally(Wide { v: 6.5 }); }\n", nil},
		{"type-param-destination-binds", "function id[T](own x: T): T { return x; }\nfunction main(): i32 {\n    let r: i32[] = id([]);\n    return r.len();\n}\n", nil},
		{"type-arg-ok", "function id[T](x: T): T { return x; }\nfunction main(): i32 { return id[i32](5); }\n", nil},
		{"type-arg-nongeneric-ok", "function f(x: i32): i32 { return x; }\nfunction main(): i32 { return f[i32](5); }\n", nil},
		// E027: a match-arm guard (`Pat when <expr> =>`) must be boolean.
		{"match-guard-nonbool", "enum O { Has(i32), Nil }\nfunction main(): i32 { let o: O = Nil; match (o) { Has(n) when n => { return n; }, _ => { return 0; } } }\n", []string{"E027"}},
		{"match-guard-bool-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { let o: O = Nil; match (o) { Has(n) when n > 0 => { return n; }, _ => { return 0; } } }\n", nil},
		// E015: variant pattern binding count must match the variant's payload count.
		{"variant-too-many-bindings", "enum O { Has(i32), Nil }\nfunction main(): i32 { let o: O = Nil; match (o) { Has(a, b) => { return a; }, Nil => { return 0; } } }\n", []string{"E015"}},
		// A bare payload-less variant name in a GENERIC payload slot is a
		// binder too: which enum the slot holds comes from the scrutinee's
		// instantiation (#11368). A variant with a payload is a plain binder.
		{"generic-slot-payloadless-binder-option", "function f(): Option[IoError] { return None; }\nfunction main(): i32 { match (f()) { Some(Unsupported) => { return 0; }, _ => { return 1; } } }\n", []string{"E015"}},
		{"generic-slot-payloadless-binder-result", "function f(): Result[i32, IoError] { return Ok(1); }\nfunction main(): i32 { match (f()) { Ok(n) => { return n; }, Err(Interrupted) => { return 0; } } }\n", []string{"E015"}},
		{"generic-slot-payloadless-binder-user", "enum Box[T] { Full(T), Empty }\nenum C { Red, Blue }\nfunction main(): i32 { let b: Box[C] = Full(Red); match (b) { Full(Blue) => { return 1; }, Empty => { return 0; } } }\n", []string{"E015"}},
		{"generic-slot-payload-variant-name-binds", "function f(): Option[IoError] { return None; }\nfunction main(): i32 { match (f()) { Some(NotFound) => { return 0; }, _ => { return 1; } } }\n", nil},
		{"variant-missing-binding", "enum O { Has(i32), Nil }\nfunction main(): i32 { let o: O = Nil; match (o) { Has => { return 1; }, Nil => { return 0; } } }\n", []string{"E015"}},
		{"variant-binding-arity-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { let o: O = Nil; match (o) { Has(n) => { return n; }, Nil => { return 0; } } }\n", nil},
		// E015 continued: a RECORD-form variant (#6676) is destructured by field
		// name in any order, and every field must be bound exactly once. A
		// resolved pattern is rewritten to the positional one in declaration
		// order (resolve_variant_fields_module); anything that does not resolve
		// lands here. The rename row also draws E001 because both checkers bind
		// the DECLARED names, leaving the renamed local undefined.
		{"record-variant-ok", "enum S { Rect { w: i32, h: i32 }, Unit }\nfunction main(): i32 { let s: S = Unit; match (s) { Rect { h, w } => { return w * h; }, Unit => { return 0; } } }\n", nil},
		{"record-variant-missing-field", "enum S { Rect { w: i32, h: i32 }, Unit }\nfunction main(): i32 { let s: S = Unit; match (s) { Rect { w } => { return w; }, Unit => { return 0; } } }\n", []string{"E015"}},
		{"record-variant-unknown-field", "enum S { Rect { w: i32, h: i32 }, Unit }\nfunction main(): i32 { let s: S = Unit; match (s) { Rect { w, z } => { return w; }, Unit => { return 0; } } }\n", []string{"E015"}},
		{"record-variant-dup-field", "enum S { Rect { w: i32, h: i32 }, Unit }\nfunction main(): i32 { let s: S = Unit; match (s) { Rect { w, w } => { return w; }, Unit => { return 0; } } }\n", []string{"E015"}},
		{"record-variant-rename", "enum S { Rect { w: i32, h: i32 }, Unit }\nfunction main(): i32 { let s: S = Unit; match (s) { Rect { w: a, h: b } => { return a; }, Unit => { return 0; } } }\n", []string{"E001", "E015"}},
		{"record-pattern-on-positional", "enum S { Pair(i32, i32), Unit }\nfunction main(): i32 { let s: S = Unit; match (s) { Pair { a, b } => { return a; }, Unit => { return 0; } } }\n", []string{"E015"}},
		{"positional-pattern-on-record-ok", "enum S { Rect { w: i32, h: i32 }, Unit }\nfunction main(): i32 { let s: S = Unit; match (s) { Rect(w, h) => { return w * h; }, Unit => { return 0; } } }\n", nil},
		// E054: an `@export(...)` world-export function cannot be generic
		// (a world export has one concrete ABI) and cannot be a method.
		{"export-generic", "@export(\"example:app/run\", \"run\") function run[T](x: T): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E054"}},
		{"export-method", "struct P { x: i32 }\n@export(\"example:app/run\", \"run\") function (p: P) run(): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E054"}},
		{"export-plain-ok", "@export(\"example:app/run\", \"run\") function run(x: i32): i32 { return x; }\nfunction main(): i32 { return 0; }\n", nil},
		// E050: use of an owned parameter after it was consumed (moved).
		{"own-call-then-use", "function sink(own xs: i32[]): i32 { return xs[0]; }\nfunction f(own xs: i32[]): i32 { let a: i32 = sink(xs); return sink(xs); }\nfunction main(): i32 { return 0; }\n", []string{"E050"}},
		{"own-double-in-stmt", "function sink(own xs: i32[]): i32 { return xs[0]; }\nfunction f(own xs: i32[]): i32 { return sink(xs) + sink(xs); }\nfunction main(): i32 { return 0; }\n", []string{"E050"}},
		// A match consumes an owned scrutinee only when an arm takes a pointer
		// payload out of it (#9539): a scalar binding or a tag test leaves it
		// whole.
		{"own-match-then-use", "enum Box { Str(string), Nil }\nfunction bsink(b: Box): i32 { return 0; }\nfunction f(own b: Box): i32 { let r: i32 = match (b) { Str(s) => s.len(), Nil => 0 }; return r + bsink(b); }\nfunction main(): i32 { return 0; }\n", []string{"E050"}},
		{"own-scalar-match-then-use", "enum Lst { Cons(i32), Nil }\nfunction lsink(l: Lst): i32 { return 0; }\nfunction f(own l: Lst): i32 { let r: i32 = match (l) { Cons(h) => h, Nil => 0 }; return r + lsink(l); }\nfunction main(): i32 { return 0; }\n", nil},
		{"own-tag-only-match-then-match", "enum Box { Str(string), Arr(i32[]), Nil }\nfunction f(own b: Box): i32 { let t: i32 = 0; match (b) { Str(_) => { t = 1; }, Arr(_) => { t = 2; }, Nil => { t = 3; } } match (b) { Str(s) => { return t + s.len(); }, Arr(a) => { return t + a.len(); }, Nil => { return t; } } }\nfunction main(): i32 { return f(Nil); }\n", nil},
		{"own-consume-in-loop", "function sink(own xs: i32[]): i32 { return xs[0]; }\nfunction f(own xs: i32[]): i32 { let i: i32 = 0; while (i < 3) { let a: i32 = sink(xs); i = i + 1; } return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E050"}},
		// A bare block is not a branch: a consume inside one stands, however
		// the block ends.
		{"own-consume-in-block-then-continue", "function sink(own xs: i32[]): i32 { return xs[0]; }\nfunction f(own xs: i32[], c: boolean): i32 { let i: i32 = 0; while (i < 3) { i = i + 1; { let a: i32 = sink(xs); if (c) { return 1; } else { continue; } } } return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E050"}},
		{"own-consume-in-block-then-return", "function sink(own xs: i32[]): i32 { return xs[0]; }\nfunction f(own xs: i32[], c: boolean): i32 { let i: i32 = 0; while (i < 3) { i = i + 1; { let a: i32 = sink(xs); if (c) { return 1; } else { return 2; } } } return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E050"}},
		{"own-borrow-only-ok", "function f(own xs: i32[]): i32 { return xs[0] + xs[1]; }\nfunction main(): i32 { return 0; }\n", nil},
		{"own-borrow-arg-ok", "function peek(xs: i32[]): i32 { return xs[0]; }\nfunction f(own xs: i32[]): i32 { let a: i32 = peek(xs); return a + peek(xs); }\nfunction main(): i32 { return 0; }\n", nil},
		{"own-single-consume-ok", "function sink(xs: i32[]): i32 { return xs[0]; }\nfunction f(own xs: i32[]): i32 { return sink(xs); }\nfunction main(): i32 { return 0; }\n", nil},
		// E051: argument to an owned parameter must be an owned value.
		{"own-arg-borrowed-param", "function consume(own xs: i32[]): i32 { return xs[0]; }\nfunction f(xs: i32[]): i32 { return consume(xs); }\nfunction main(): i32 { return 0; }\n", []string{"E051"}},
		// A local handed over where it dies is a move (#9541); one read after
		// the call, or a handover inside a loop, is still a borrow.
		{"own-arg-plain-local-last-use", "function consume(own xs: i32[]): i32 { return xs[0]; }\nfunction f(): i32 { let xs: i32[] = [1, 2]; return consume(xs); }\nfunction main(): i32 { return 0; }\n", nil},
		{"own-arg-plain-local-read-after", "function consume(own xs: i32[]): i32 { return xs[0]; }\nfunction mk(): i32[] { return [1, 2]; }\nfunction f(): i32 { let xs: i32[] = mk(); let n: i32 = consume(xs); return n + xs.len(); }\nfunction main(): i32 { return 0; }\n", []string{"E051"}},
		{"own-arg-plain-local-in-loop", "function consume(own xs: i32[]): i32 { return xs[0]; }\nfunction mk(): i32[] { return [1, 2]; }\nfunction f(): i32 { let xs: i32[] = mk(); let n: i32 = 0; while (n < 3) { n = n + consume(xs); } return n; }\nfunction main(): i32 { return 0; }\n", []string{"E051"}},
		// A method's `own` receiver and `own` parameters draw E050 / E051 as a
		// plain function's do (#11558).
		{"own-receiver-borrowed-param", "struct B { xs: i32[] }\nfunction (own b: B) add(x: i32): B { return B { xs: b.xs.append(x) }; }\nfunction mk(): B { return B { xs: [1] }; }\nfunction f(b: B): B { return b.add(1); }\nfunction main(): i32 { return f(mk()).xs.len(); }\n", []string{"E051"}},
		{"own-receiver-local-last-use", "struct B { xs: i32[] }\nfunction (own b: B) add(x: i32): B { return B { xs: b.xs.append(x) }; }\nfunction mk(): B { return B { xs: [1] }; }\nfunction main(): i32 { let b: B = mk(); let c: B = b.add(1); return c.xs.len(); }\n", nil},
		{"own-receiver-local-read-after", "struct B { xs: i32[] }\nfunction (own b: B) add(x: i32): B { return B { xs: b.xs.append(x) }; }\nfunction mk(): B { return B { xs: [1] }; }\nfunction main(): i32 { let b: B = mk(); let c: B = b.add(1); return c.xs.len() + b.xs.len(); }\n", []string{"E051"}},
		{"own-receiver-self-reassign", "struct B { xs: i32[] }\nfunction (own b: B) add(x: i32): B { return B { xs: b.xs.append(x) }; }\nfunction mk(): B { return B { xs: [1] }; }\nfunction main(): i32 { let b: B = mk(); b = b.add(1); return b.xs.len(); }\n", nil},
		{"own-receiver-own-param", "struct B { xs: i32[] }\nfunction (own b: B) add(x: i32): B { return B { xs: b.xs.append(x) }; }\nfunction mk(): B { return B { xs: [1] }; }\nfunction f(own b: B): B { return b.add(1); }\nfunction main(): i32 { return f(mk()).xs.len(); }\n", nil},
		{"own-receiver-use-after-move", "struct B { xs: i32[] }\nfunction (own b: B) add(x: i32): B { return B { xs: b.xs.append(x) }; }\nfunction mk(): B { return B { xs: [1] }; }\nfunction f(own b: B): i32 { let c: B = b.add(1); return c.xs.len() + b.xs.len(); }\nfunction main(): i32 { return f(mk()); }\n", []string{"E050"}},
		{"own-receiver-generic", "struct Box[T] { v: T[] }\nfunction (own b: Box[T]) push(x: T): Box[T] { return Box[T] { v: b.v.append(x) }; }\nfunction f(b: Box[i32]): Box[i32] { return b.push(1); }\nfunction main(): i32 { return f(Box[i32] { v: [1] }).v.len(); }\n", []string{"E051"}},
		{"own-receiver-enum", "enum Sh { Sq(i32[]), No }\nfunction (own s: Sh) grow(): Sh {\n    match (s) {\n        Sq(xs) => { return Sq(xs.append(1)); },\n        No => { return No; }\n    }\n}\nfunction f(s: Sh): Sh { return s.grow(); }\nfunction main(): i32 { let s: Sh = f(Sq([1])); return 0; }\n", []string{"E051"}},
		{"own-receiver-dies-before-return", "struct B { xs: i32[] }\nfunction (own b: B) add(x: i32): B { return B { xs: b.xs.append(x) }; }\nfunction mk(): B { return B { xs: [1] }; }\nfunction f(c: boolean): i32 {\n  let b: B = mk();\n  if (c) {\n    let d: B = b.add(1);\n    return d.xs.len();\n  }\n  return b.xs.len();\n}\nfunction main(): i32 { return f(true); }\n", nil},
		{"own-receiver-dies-in-loop", "struct B { xs: i32[] }\nfunction (own b: B) add(x: i32): B { return B { xs: b.xs.append(x) }; }\nfunction mk(): B { return B { xs: [1] }; }\nfunction f(c: boolean): i32 {\n  let b: B = mk();\n  while (c) {\n    let d: B = b.add(1);\n    return d.xs.len();\n  }\n  return b.xs.len();\n}\nfunction main(): i32 { return f(true); }\n", []string{"E051"}},
		{"own-receiver-array", "function (own xs: i32[]) grow(x: i32): i32[] { return xs.append(x); }\nfunction f(xs: i32[]): i32 { let ys: i32[] = xs.grow(1); return ys.len(); }\nfunction main(): i32 { return f([1]); }\n", []string{"E051"}},
		{"own-receiver-array-last-use", "function (own xs: i32[]) grow(x: i32): i32[] { return xs.append(x); }\nfunction main(): i32 { let xs: i32[] = [1]; let ys: i32[] = xs.grow(1); return ys.len(); }\n", nil},
		{"own-receiver-slice", "function (own xs: [i32]) count(): i32 { return xs.len(); }\nfunction f(xs: [i32]): i32 { return xs.count(); }\nfunction main(): i32 { let a: i32[] = [1]; return f(a[0:1]); }\n", []string{"E051"}},
		{"own-receiver-string", "function (own s: string) shout(): string { return s + \"!\"; }\nfunction f(s: string): i32 { let t: string = s.shout(); return t.len(); }\nfunction main(): i32 { return f(\"a\"); }\n", []string{"E051"}},
		{"own-receiver-trait-borrowed", "struct Counter { n: i32 }\ntrait Consume { function take(own self: Self): i32; }\nimpl Consume for Counter { function take(own self: Self): i32 { return self.n; } }\nfunction f(c: Counter): i32 { return c.take(); }\nfunction main(): i32 { return f(Counter { n: 1 }); }\n", []string{"E051"}},
		{"own-receiver-trait-use-after-move", "struct Counter { n: i32 }\ntrait Consume { function take(own self: Self): i32; }\nimpl Consume for Counter { function take(own self: Self): i32 { return self.n; } }\nfunction f(own c: Counter): i32 { return c.take() + c.take(); }\nfunction main(): i32 { return f(Counter { n: 1 }); }\n", []string{"E050"}},
		{"own-receiver-dyn-use-after-move", "struct Counter { n: i32 }\ntrait Consume { function take(own self: Self): i32; }\nimpl Consume for Counter { function take(own self: Self): i32 { return self.n; } }\nfunction f(own c: dyn Consume): i32 { return c.take() + c.take(); }\nfunction main(): i32 { return f(Counter { n: 1 }); }\n", []string{"E050"}},
		{"own-receiver-dyn-borrowed", "struct Counter { n: i32 }\ntrait Consume { function take(own self: Self): i32; }\nimpl Consume for Counter { function take(own self: Self): i32 { return self.n; } }\nfunction f(c: dyn Consume): i32 { return c.take(); }\nfunction main(): i32 { return f(Counter { n: 1 }); }\n", nil},
		{"own-receiver-name-shared-with-array", "struct B { v: i32 }\nfunction (own b: B) take(): i32 { return b.v; }\nfunction (own xs: i32[]) take(): i32 { return xs.len(); }\nfunction f(c: boolean): i32 {\n  let b: B = B { v: 1 };\n  if (c) {\n    let n: i32 = b.take();\n    return n;\n  }\n  return b.v;\n}\nfunction main(): i32 { return f(true); }\n", nil},
		{"own-receiver-map", "function (own m: Map[string, i32]) count(): i32 { return 0; }\nfunction f(m: Map[string, i32]): i32 { return m.count(); }\nfunction main(): i32 { return 0; }\n", []string{"E051"}},
		{"method-own-param-borrowed", "struct B { xs: i32[] }\nfunction (b: B) put(own ys: i32[]): i32 { return b.xs.len() + ys.len(); }\nfunction f(b: B, ys: i32[]): i32 { return b.put(ys); }\nfunction main(): i32 { return f(B { xs: [1] }, [2]); }\n", []string{"E051"}},
		{"method-own-param-last-use", "struct B { xs: i32[] }\nfunction (b: B) put(own ys: i32[]): i32 { return b.xs.len() + ys.len(); }\nfunction f(b: B, xs: i32[]): i32 {\n  let ys: i32[] = xs;\n  return b.put(ys);\n}\nfunction main(): i32 { return f(B { xs: [1] }, [2]); }\n", nil},
		{"method-own-param-dies-before-return", "struct P { v: i32 }\nstruct B { n: i32 }\nfunction (b: B) put(own p: P): i32 { return b.n + p.v; }\nfunction f(b: B, k: boolean): i32 {\n  let p: P = P { v: 1 };\n  if (k) {\n    let n: i32 = b.put(p);\n    return n;\n  }\n  return p.v;\n}\nfunction main(): i32 { return f(B { n: 1 }, true); }\n", nil},
		{"method-own-array-param-dies-before-return", "struct B { n: i32 }\nfunction (b: B) put(own ys: i32[]): i32 { return b.n + ys.len(); }\nfunction f(b: B, k: boolean): i32 {\n  let ys: i32[] = [1];\n  if (k) {\n    let n: i32 = b.put(ys);\n    return n;\n  }\n  return ys.len();\n}\nfunction main(): i32 { return f(B { n: 1 }, true); }\n", []string{"E051"}},
		{"own-dyn-arg-borrowed", "trait Consume { function take(own self: Self, own xs: i32[]): i32; }\nstruct A { v: i32 }\nfunction (a: A) take(own xs: i32[]): i32 { return a.v + xs.len(); }\nfunction f(c: dyn Consume, xs: i32[]): i32 { return c.take(xs); }\nfunction main(): i32 { return 0; }\n", nil},
		{"own-dyn-arg-last-use", "trait Consume { function take(own self: Self, own xs: i32[]): i32; }\nstruct A { v: i32 }\nfunction (a: A) take(own xs: i32[]): i32 { return a.v + xs.len(); }\nfunction f(c: dyn Consume, xs: i32[]): i32 {\n  let ys: i32[] = xs;\n  return c.take(ys);\n}\nfunction main(): i32 { return 0; }\n", nil},
		{"own-arg-fresh-ok", "function consume(own xs: i32[]): i32 { return xs[0]; }\nfunction f(): i32 { return consume([1, 2]); }\nfunction main(): i32 { return 0; }\n", nil},
		{"own-arg-forward-ok", "function consume(own xs: i32[]): i32 { return xs[0]; }\nfunction f(own ys: i32[]): i32 { return consume(ys); }\nfunction main(): i32 { return 0; }\n", nil},
		// E049: assigning to a reference-typed variable captured by a closure.
		{"cap-assign-string", "function main(): i32 { let s: string = \"x\"; let f = (): i32 => { s = \"y\"; return 0; }; return f(); }\n", []string{"E049"}},
		{"cap-assign-array", "function main(): i32 { let a: i32[] = [1]; let f = (): i32 => { a = [2]; return 0; }; return f(); }\n", []string{"E049"}},
		{"cap-assign-struct", "struct P { x: i32 }\nfunction main(): i32 { let p: P = P { x: 1 }; let f = (): i32 => { p = P { x: 2 }; return 0; }; return f(); }\n", []string{"E049"}},
		{"cap-assign-param", "function g(s: string): i32 { let f = (): i32 => { s = \"y\"; return 0; }; return f(); }\nfunction main(): i32 { return 0; }\n", []string{"E049"}},
		{"cap-assign-scalar-ok", "function main(): i32 { let n: i32 = 1; let f = (): i32 => { n = 2; return n; }; return f(); }\n", nil},
		{"cap-read-ref-ok", "function main(): i32 { let s: string = \"x\"; let f = (): i32 => { return s.len(); }; return f(); }\n", nil},
		// #2673: an arrow lambda with a BLOCK body infers its return type.
		// The two checkers disagreed here — the self-host accepted it and
		// native reported E002, because the arrow desugar wraps the body in a
		// `return` and native unified the inner return's type against the
		// block's own `never`. Pinned so the convergence cannot come apart,
		// and so the anonymous `function` expression this form is meant to
		// replace is not the only spelling that infers.
		{"arrow-block-body-infer-ok", "function apply(f: (i32) => i32, v: i32): i32 { return f(v); }\nfunction main(): i32 {\n    let g = (x: i32) => { return x * 2; };\n    return apply(g, 4);\n}\n", nil},
		{"arrow-block-body-stmts-infer-ok", "function apply(f: (i32) => i32, v: i32): i32 { return f(v); }\nfunction main(): i32 {\n    let g = (x: i32) => { let y: i32 = x + 1; return y * 2; };\n    return apply(g, 3);\n}\n", nil},
		// A braced body that yields nothing is a VOID lambda, not a value-less
		// block in value position — the shape the `function` spelling has, and
		// the last one the arrow form could not express (#2673).
		{"arrow-block-body-void-ok", "function run(f: (i32) => void, v: i32): void { f(v); }\nfunction main(): i32 {\n    let seen: i32 = 0;\n    let g = (x: i32) => { seen = seen + x; };\n    run(g, 4);\n    return seen - 4;\n}\n", nil},
		{"arrow-block-body-empty-ok", "function run(f: (i32) => void, v: i32): void { f(v); }\nfunction main(): i32 {\n    let g = (x: i32) => {};\n    run(g, 4);\n    return 0;\n}\n", nil},
		// An ASSIGNMENT inside a value block is a statement, not the block's
		// trailing value: parse_expr stops in front of the `=`, and the item has
		// to be re-read as the statement it is.
		{"value-block-assign-ok", "function main(): i32 {\n    let a: i32 = 1;\n    let b: i32 = if (a > 0) { a = a + 1; a } else { 0 };\n    return b - 2;\n}\n", nil},
		// #8561: an `if` / `match` STATEMENT among a block body's statements.
		// Both stay on the block scanner's expression path — either can be the
		// block's trailing VALUE — so an item followed by neither `;` nor `}`
		// has to be re-read as the statement it is.
		{"arrow-block-body-match-stmt-ok", "function apply(f: (i32) => i32, v: i32): i32 { return f(v); }\nfunction main(): i32 {\n    let g = (x: i32) => {\n        match (x) {\n            0 => { return 100; },\n            _ => {}\n        }\n        return x * 2;\n    };\n    return apply(g, 4) - 8;\n}\n", nil},
		{"arrow-block-body-if-stmt-ok", "function apply(f: (i32) => i32, v: i32): i32 { return f(v); }\nfunction main(): i32 {\n    let g = (x: i32) => {\n        if (x > 0) { return x * 2; } else { return 0 - x; }\n    };\n    return apply(g, 4) - 8;\n}\n", nil},
		// #8593: `use` and `let … else` rewrite the REST of the block they
		// appear in, so they work only where the body is a real function body
		// — a value-block scanner has no remainder to give them.
		{"arrow-block-body-use-ok", "function give(x: i32, cb: (i32) => i32): i32 { return cb(x); }\nfunction main(): i32 {\n    let f = (): i32 => {\n        use n <- give(41);\n        return n + 1;\n    };\n    return f() - 42;\n}\n", nil},
		{"arrow-block-body-letelse-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 {\n    let f = (): i32 => {\n        let o: O = Nil;\n        let Has(v) = o else { return 0; };\n        return v;\n    };\n    return f();\n}\n", nil},
		// The other direction: a trailing `match` written without a `;` is
		// still the block's value.
		{"arrow-block-body-match-tail-ok", "function apply(f: (i32) => i32, v: i32): i32 { return f(v); }\nfunction main(): i32 {\n    let g = (x: i32) => {\n        let y: i32 = x + 1;\n        match (y) { 0 => 100, _ => y * 2 }\n    };\n    return apply(g, 3) - 8;\n}\n", nil},
		{"cap-assign-local-ok", "function main(): i32 { let f = (): i32 => { let t: string = \"a\"; t = \"b\"; return 0; }; return f(); }\n", nil},
		// #4410: the closure-capture contract (docs/CLOSURE-CAPTURE.md). The
		// scalar/reference split must be BYTE-identical to native's
		// ast.IsPointerType. These pin the two former divergences the parity
		// review surfaced: (1) the unsigned widths u8/u32/u64/usize are scalars
		// (native never flagged them; the self-host used to), and (2) an
		// unannotated var bound to a pointer-shaped LITERAL is reference-typed
		// (native infers it; the self-host used to skip unannotated captures).
		{"cap-assign-u32-ok", "function main(): i32 { let n: u32 = 1; let f = (): i32 => { n = 2; return 0; }; return f(); }\n", nil},
		{"cap-assign-u64-ok", "function main(): i32 { let n: u64 = 1; let f = (): i32 => { n = 2; return 0; }; return f(); }\n", nil},
		{"cap-assign-u8-ok", "function main(): i32 { let n: u8 = 1; let f = (): i32 => { n = 2; return 0; }; return f(); }\n", nil},
		{"cap-assign-usize-ok", "function main(): i32 { let n: usize = 1; let f = (): i32 => { n = 2; return 0; }; return f(); }\n", nil},
		{"cap-assign-f64-ok", "function main(): i32 { let x: f64 = 1.5; let f = (): i32 => { x = 2.5; return 0; }; return f(); }\n", nil},
		{"cap-assign-bool-ok", "function main(): i32 { let b: boolean = true; let f = (): i32 => { b = false; return 0; }; return f(); }\n", nil},
		{"cap-assign-unann-string", "function main(): i32 { let s = \"x\"; let f = (): i32 => { s = \"y\"; return 0; }; return f(); }\n", []string{"E049"}},
		{"cap-assign-unann-array", "function main(): i32 { let a = [1]; let f = (): i32 => { a = [2]; return 0; }; return f(); }\n", []string{"E049"}},
		{"cap-assign-unann-struct", "struct P { x: i32 }\nfunction main(): i32 { let p = P { x: 1 }; let f = (): i32 => { p = P { x: 2 }; return 0; }; return f(); }\n", []string{"E049"}},
		{"cap-assign-unann-tuple", "function main(): i32 { let t = (1, 2); let f = (): i32 => { t = (3, 4); return 0; }; return f(); }\n", []string{"E049"}},
		// A generic function named where a value is expected: nothing
		// determines its type parameters (#7040). A module const in a call
		// bracket is that value, not a type argument (#10427).
		{"generic-fn-as-value", "function id[T](a: T): T { return a; }\nfunction main(): i32 { let f = id; return 0; }\n", []string{"E040"}},
		{"module-const-in-call-bracket", "const T: i32 = 2;\nfunction id[T](a: T): T { return a; }\nfunction pass[T](a: T): T { return id[T](a); }\nfunction main(): i32 { return pass(3); }\n", []string{"E040"}},
		// An injected enum's variant payload is checked like a declared one:
		// JsonValue and IoError have no union declaration to find it by.
		{"injected-variant-payload-json", "function main(): i32 { let j: JsonValue = JNumber(1.0); return 0; }\n", []string{"E036"}},
		{"injected-variant-payload-ioerror", "function main(): i32 { let e: IoError = NotFound(3); return 0; }\n", []string{"E036"}},
		{"injected-variant-payload-ok", "function main(): i32 { let j: JsonValue = JNumber(\"1.0\"); let e: IoError = NotFound(\"p\"); return 0; }\n", nil},
		// IoError.Other is (path, message, errno), the errno an i32 (#11296).
		{"injected-other-three-payloads", "function main(): i32 { let e: IoError = Other(\"p\", \"m\", 21); match (e) { Other(p, m, n) => { return n; }, _ => { return 0; } } }\n", nil},
		{"injected-other-two-arguments", "function main(): i32 { let e: IoError = Other(\"p\", \"m\"); return 0; }\n", []string{"E036"}},
		{"injected-other-errno-is-i32", "function main(): i32 { let e: IoError = Other(\"p\", \"m\", \"21\"); return 0; }\n", []string{"E036"}},
		{"injected-other-two-bindings", "function main(): i32 { let e: IoError = Unsupported; match (e) { Other(p, m) => { return 1; }, _ => { return 0; } } }\n", []string{"E015"}},
		{"injected-other-errno-binding-is-i32", "function main(): i32 { let e: IoError = Unsupported; match (e) { Other(p, m, n) => { let s: string = n; return 1; }, _ => { return 0; } } }\n", []string{"E003"}},
		// The enclosing scope's store is judged on the VALUE's type, as native
		// judges it: a struct that reaches no function may be stored into a
		// captured `dyn`, a `dyn`-typed value may not (#8440).
		{"cap-dyn-outer-store-struct", "trait Shape { function area(self: Self): i32; }\nstruct Sq { s: i32 }\nimpl Shape for Sq { function area(self: Self): i32 { return self.s; } }\nfunction main(): i32 { let d: dyn Shape = Sq { s: 3 }; let f: () => i32 = (): i32 => { return d.area(); }; d = Sq { s: 5 }; return f(); }\n", nil},
		{"cap-dyn-outer-store-dyn", "trait Shape { function area(self: Self): i32; }\nstruct Sq { s: i32 }\nimpl Shape for Sq { function area(self: Self): i32 { return self.s; } }\nfunction main(): i32 { let d: dyn Shape = Sq { s: 3 }; let e: dyn Shape = Sq { s: 4 }; let f: () => i32 = (): i32 => { return d.area(); }; d = e; return f(); }\n", []string{"E049"}},
		// An if-expression's concrete arm does not coerce to its dyn arm;
		// a match-expression's does (#10601).
		{"if-arms-dyn-vs-struct", "trait Shape { function area(self: Self): i32; }\nstruct Sq { s: i32 }\nimpl Shape for Sq { function area(self: Self): i32 { return self.s; } }\nfunction pick(c: boolean, d: dyn Shape): dyn Shape { return if (c) { d } else { Sq { s: 3 } }; }\nfunction main(): i32 { return 0; }\n", []string{"E031"}},
		{"if-arms-struct-vs-dyn", "trait Shape { function area(self: Self): i32; }\nstruct Sq { s: i32 }\nimpl Shape for Sq { function area(self: Self): i32 { return self.s; } }\nfunction pick(c: boolean, d: dyn Shape): dyn Shape { return if (c) { Sq { s: 3 } } else { d }; }\nfunction main(): i32 { return 0; }\n", []string{"E031"}},
		{"match-arms-dyn-vs-struct-ok", "trait Shape { function area(self: Self): i32; }\nstruct Sq { s: i32 }\nimpl Shape for Sq { function area(self: Self): i32 { return self.s; } }\nfunction pick(k: i32, d: dyn Shape): dyn Shape { return match (k) { 0 => d, _ => Sq { s: 3 } }; }\nfunction main(): i32 { return 0; }\n", nil},
		{"if-arms-both-dyn-ok", "trait Shape { function area(self: Self): i32; }\nstruct Sq { s: i32 }\nimpl Shape for Sq { function area(self: Self): i32 { return self.s; } }\nfunction pick(c: boolean, d: dyn Shape): dyn Shape { let x: dyn Shape = Sq { s: 3 }; return if (c) { d } else { x }; }\nfunction main(): i32 { return 0; }\n", nil},
		{"cap-assign-unann-scalar-ok", "function main(): i32 { let n = 5; let f = (): i32 => { n = 7; return 0; }; return f(); }\n", nil},
		// E002 inside lambda bodies: a lambda's `return` is checked against
		// the lambda's OWN declared return type, not the enclosing function's
		// (ret_diags stops at the lambda boundary). lret_stmts/lret_expr fill
		// that gap.
		{"lambda-ret-mismatch", "function main(): i32 { let f = (): i32 => { return \"x\"; }; return f(); }\n", []string{"E002"}},
		{"lambda-ret-ok", "function main(): i32 { let f = (): i32 => { return 5; }; return f(); }\n", nil},
		{"lambda-in-void-fn", "function g(): void { let f = (): i32 => { return \"x\"; }; }\nfunction main(): i32 { return 0; }\n", []string{"E002"}},
		{"lambda-nested-if-mismatch", "function main(): i32 { let f = (): i32 => { if (1 < 2) { return \"x\"; } return 1; }; return f(); }\n", []string{"E002"}},
		{"lambda-bare-return", "function main(): i32 { let f = (): i32 => { return; }; return f(); }\n", []string{"E012"}},
		{"lambda-arg-mismatch", "function run(fn: () => i32): i32 { return fn(); }\nfunction main(): i32 { return run((): i32 => { return \"x\"; }); }\n", []string{"E002"}},
		{"lambda-nested-lambda-mismatch", "function main(): i32 { let f = (): i32 => { let g = (): i32 => { return \"x\"; }; return g(); }; return f(); }\n", []string{"E002"}},
		{"lambda-no-rettype-ok", "function main(): i32 { let f = () => { return; }; return 0; }\n", nil},
		{"rec-local-capture-ret-mismatch", "function main(): i32 { let base: string = \"x\"; function f(n: i32): i32 { if (n <= 0) { return base; } return f(n - 1); } return f(3); }\n", []string{"E002"}},
		// A `match` / `if` used in value position is desugared by the parser
		// into an IIFE — ((): RT => { … })() — whose RT is a coarse
		// heuristic tag (if_expr_rt, defaulting to "i32"). The lambda-body
		// E002 pass must NOT check those synthesized returns against that
		// tag, or a valid string-valued match/if-expression (whose first arm
		// isn't a string literal, so RT mis-tags as "i32") false-positives.
		{"match-expr-string-arms-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { let a: string = \"p\"; let b: string = \"q\"; let o: O = Nil; let s: string = match (o) { Has(n) => a, Nil => b }; return 0; }\n", nil},
		{"if-expr-string-arms-ok", "function main(): i32 { let a: string = \"p\"; let b: string = \"q\"; let s: string = if (1 < 2) { a } else { b }; return 0; }\n", nil},
		// A payload-bearing enum variant `V(T)` lowers to a struct `V` with a
		// marker field `__ev: T`; the pattern `V(n)` binds the PAYLOAD value
		// (type T), not the wrapper struct. Typing it as the wrapper struct
		// false-positived E038 when the payload was passed to a typed
		// function. variant_payload_type_at reads the real payload type.
		{"enum-payload-i32-arg-ok", "enum O { Has(i32), Nil }\nfunction f(n: i32): i32 { return n; }\nfunction main(): i32 { let o: O = Nil; match (o) { Has(n) => { let r: i32 = f(n); }, Nil => { } } return 0; }\n", nil},
		{"enum-payload-string-arg-ok", "enum S { Tag(string), Non }\nfunction h(s: string): i32 { return 0; }\nfunction main(): i32 { let x: S = Non; match (x) { Tag(t) => { let r: i32 = h(t); }, Non => { } } return 0; }\n", nil},
		// Regression direction: a real payload-type mismatch still fires E038
		// (n is i32, passed to a string parameter).
		{"enum-payload-arg-mismatch", "enum O { Has(i32), Nil }\nfunction g(s: string): i32 { return 0; }\nfunction main(): i32 { let o: O = Nil; match (o) { Has(n) => { let r: i32 = g(n); }, Nil => { } } return 0; }\n", []string{"E038"}},
		// A struct-union member still binds the whole struct (no `__ev`), so
		// field access on it stays clean.
		{"struct-union-member-field-ok", "struct A { x: i32 }\nstruct B { y: i32 }\npub type U = A | B;\nfunction f(u: U): i32 { match (u) { A(a) => { return a.x; }, B(b) => { return b.y; } } return 0; }\nfunction main(): i32 { return f(A { x: 1 }); }\n", nil},
		// E055: a bare value-returning collection mutator discards its result.
		{"unused-append-result", "function main(): i32 { let a: i32[] = [1]; a.append(2); return a[0]; }\n", []string{"E055"}},
		{"append-reassigned-ok", "function main(): i32 { let a: i32[] = [1]; a = a.append(2); return a[0]; }\n", nil},
		{"append-result-used-ok", "function main(): i32 { let a: i32[] = [1]; return a.append(2)[0]; }\n", nil},
		// E031: a `match` / `if` used in value position desugars to an IIFE,
		// so its arms' result types must be mutually compatible. The predicate
		// mirrors the Go checker's unifyIfArms — clear scalar mismatches fire;
		// numeric (f64/i32) widen; tuples/arrays unify element-wise; two
		// structs of the SAME enum family are compatible (but struct-union
		// members and unrelated structs are NOT); an unknown arm skips E031
		// (E001 owns it). Cross-checked against the Go checker.
		{"e031-if-i32-string", "function main(): i32 { let r = if (1 < 2) { 1 } else { \"x\" }; return 0; }\n", []string{"E031"}},
		{"e031-if-i32-i32-ok", "function main(): i32 { let r = if (1 < 2) { 1 } else { 2 }; return r; }\n", nil},
		{"e031-if-call-mismatch", "function a(): i32 { return 1; }\nfunction b(): string { return \"x\"; }\nfunction main(): i32 { let r = if (1 < 2) { a() } else { b() }; return 0; }\n", []string{"E031"}},
		{"e031-if-elseif-mismatch", "function main(): i32 { let r = if (1 < 2) { 1 } else if (2 < 3) { 2 } else { \"x\" }; return 0; }\n", []string{"E031"}},
		{"e031-if-f64-i32-ok", "function main(): i32 { let r = if (1 < 2) { 1.0 } else { 2 }; return 0; }\n", nil},
		{"e031-if-bool-arms-ok", "function main(): i32 { let r = if (1 < 2) { true } else { false }; return 0; }\n", nil},
		{"e031-if-struct-i32-mismatch", "struct P { x: i32 }\nfunction main(): i32 { let r = if (1 < 2) { P { x: 1 } } else { 2 }; return 0; }\n", []string{"E031"}},
		{"e031-if-struct-arms-ok", "struct P { x: i32 }\nfunction main(): i32 { let r = if (1 < 2) { P { x: 1 } } else { P { x: 2 } }; return r.x; }\n", nil},
		{"e031-if-arm-undefined", "function main(): i32 { let r = if (1 < 2) { undef } else { 1 }; return 0; }\n", []string{"E001"}},
		{"e031-match-i32-string", "enum O { A, B }\nfunction main(): i32 { let o: O = A; let r = match (o) { A => 1, B => \"x\" }; return 0; }\n", []string{"E031"}},
		{"e031-match-i32-i32-ok", "enum O { A, B }\nfunction main(): i32 { let o: O = A; let r = match (o) { A => 1, B => 2 }; return r; }\n", nil},
		{"e031-match-bool-i32", "enum O { A, B }\nfunction main(): i32 { let o: O = A; let r = match (o) { A => true, B => 1 }; return 0; }\n", []string{"E031"}},
		{"e031-match-payload-arm-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { let o: O = Nil; let r = match (o) { Has(n) => n, Nil => 0 }; return r; }\n", nil},
		{"e031-match-three-arms-last-bad", "enum O { A, B, C }\nfunction main(): i32 { let o: O = A; let r = match (o) { A => 1, B => 2, C => \"x\" }; return 0; }\n", []string{"E031"}},
		// A value if/match-expression has a real result type (the branches'
		// common type): a mismatched var annotation is E003, a mismatched
		// `return` is E002. A matching annotation, and a numeric-mix set (which
		// this port doesn't unify), stay clean.
		{"if-expr-value-assign-bad", "function main(): i32 { let x: string = if (1 < 2) { 1 } else { 2 }; return 0; }\n", []string{"E003"}},
		{"match-expr-value-assign-bad", "enum E { A, B }\nfunction main(): i32 { let e: E = A; let x: string = match (e) { A => 1, B => 2 }; return 0; }\n", []string{"E003"}},
		{"if-expr-value-assign-ok", "function main(): i32 { let x: i32 = if (1 < 2) { 1 } else { 2 }; return x; }\n", nil},
		{"if-expr-value-return-bad", "function f(): string { return if (1 < 2) { 1 } else { 2 }; }\nfunction main(): i32 { return 0; }\n", []string{"E002"}},
		// Same-enum-family / element-wise compatible arms are NOT flagged (Go
		// also reports nothing): Option Some/None, enum variants, a nested
		// if-expression arm, tuple arms with matching element types.
		{"e031-match-option-arms-ok", "enum O { A, B }\nfunction f(o: O): Option[i32] { let r = match (o) { A => Some(1), B => None }; return r; }\nfunction main(): i32 { return 0; }\n", nil},
		{"e031-if-enum-variant-arms-ok", "enum Sh { Circle(i32), Empty }\nfunction main(): i32 { let c = true; let r = if (c) { Circle(1) } else { Empty }; return 0; }\n", nil},
		{"e031-match-nested-if-arm-ok", "enum O { A, B }\nfunction main(): i32 { let o: O = A; let r = match (o) { A => if (1<2) { 1 } else { 2 }, B => 3 }; return r; }\n", nil},
		{"e031-match-tuple-arms-ok", "enum O { A, B }\nfunction main(): i32 { let o: O = A; let r = match (o) { A => (1,2), B => (3,4) }; return r.0; }\n", nil},
		// Faithful-precision cases — a conservative "all aggregates compatible"
		// predicate misses these; they must match the Go checker:
		{"e031-two-unrelated-structs", "struct P { x: i32 }\nstruct Q { y: i32 }\nfunction main(): i32 { let c = true; let r = if (c) { P { x: 1 } } else { Q { y: 2 } }; return 0; }\n", []string{"E031"}},
		{"e031-tuple-elem-mismatch", "function main(): i32 { let c = true; let r = if (c) { (1, \"x\") } else { (1, 2) }; return 0; }\n", []string{"E031"}},
		{"e031-array-elem-mismatch", "function main(): i32 { let c = true; let r = if (c) { [\"x\"] } else { [1] }; return 0; }\n", []string{"E031"}},
		{"e031-union-members-mismatch", "struct A { x: i32 }\nstruct B { y: i32 }\npub type U = A | B;\nfunction main(): i32 { let c = true; let r = if (c) { A { x: 1 } } else { B { y: 2 } }; return 0; }\n", []string{"E031"}},
		{"e031-same-enum-diff-payload-ok", "enum E { A(i32), B(string) }\nfunction main(): i32 { let c = true; let r = if (c) { A(1) } else { B(\"y\") }; return 0; }\n", nil},
		{"e031-tuple-elems-ok", "function main(): i32 { let c = true; let r = if (c) { (1, \"x\") } else { (2, \"y\") }; return r.0; }\n", nil},
		// E045: a map literal's first key fixes the key type, which must be
		// i32 or string (the only key kinds the runtime hash/compare
		// supports). Map programs need `import "core/map";` (Go reports E001
		// otherwise — a Go-only rule the self-host doesn't model, so kept out
		// of the corpus). Cross-checked against the Go checker.
		// A declared `str[]` keeps its view elements (#10201), so an element
		// read into a `string` is E003 as it is natively: from a local, a
		// parameter and a function result.
		{"e003-str-array-local-element-into-string", "function main(): i32 { let s: string = \"ab\"; let xs: str[] = [slice_unchecked(s, 0, 1)]; let t: string = xs[0]; return t.len(); }\n", []string{"E003"}},
		{"e003-str-array-param-element-into-string", "function f(xs: str[]): i32 { let t: string = xs[0]; return t.len(); }\nfunction main(): i32 { return 0; }\n", []string{"E003"}},
		{"e003-str-array-result-element-into-string", "function mk(s: string): str[] { let xs: str[] = [slice_unchecked(s, 0, 1)]; return xs; }\nfunction main(): i32 { let t: string = mk(\"ab\")[0]; return t.len(); }\n", []string{"E003"}},
		{"str-array-element-into-str-clean", "function f(xs: str[]): i32 { let t: str = xs[0]; return t.len(); }\nfunction main(): i32 { return 0; }\n", nil},
		// A lambda's `str` / `str[]` result keeps its views as a function's does (#10212).
		{"e003-lambda-str-array-result-element-into-string", "function main(): i32 { let s: string = \"ab\"; let f = (x: string): str[] => { let o: str[] = []; o = o.append(slice_unchecked(x, 0, 1)); return o; }; let t: string = f(s)[0]; return t.len(); }\n", []string{"E003"}},
		{"e003-lambda-str-result-into-string", "function main(): i32 { let s: string = \"ab\"; let f = (x: string): str => { return slice_unchecked(x, 0, 1); }; let t: string = f(s); return t.len(); }\n", []string{"E003"}},
		{"lambda-str-result-clean", "function main(): i32 { let s: string = \"ab\"; let f = (x: string): str => { return slice_unchecked(x, 0, 1); }; let t: str = f(s); return t.len(); }\n", nil},
		// A nested function declaration desugars to the same lambda.
		{"e003-nested-fn-str-result-into-string", "function main(): i32 { let s: string = \"ab\"; function f(x: string): str { return slice_unchecked(x, 0, 1); } let t: string = f(s); return t.len(); }\n", []string{"E003"}},
		{"e003-nested-fn-str-array-result-element-into-string", "function main(): i32 { let s: string = \"ab\"; function f(x: string): str[] { let o: str[] = []; o = o.append(slice_unchecked(x, 0, 1)); return o; } let t: string = f(s)[0]; return t.len(); }\n", []string{"E003"}},
		{"nested-fn-str-result-clean", "function main(): i32 { let s: string = \"ab\"; function f(x: string): str { return slice_unchecked(x, 0, 1); } let t: str = f(s); return t.len(); }\n", nil},
		// A builtin that stores its argument is an owning sink, so a `str` view
		// is not lent there: append, with, and a map insert's key and value.
		// Native's storesArgument; a `str[]` still takes the view.
		{"e038-str-appended-to-string-array", "function main(): i32 { let s: string = \"ab\"; let out: string[] = []; out = out.append(slice_unchecked(s, 0, 1)); return out.len(); }\n", []string{"E038"}},
		{"e038-str-with-into-string-array", "function main(): i32 { let s: string = \"ab\"; let out: string[] = [\"x\"]; out = out.with(0, slice_unchecked(s, 0, 1)); return out.len(); }\n", []string{"E038"}},
		{"e038-str-as-map-key", "import \"core/map\";\nfunction main(): i32 { let s: string = \"ab\"; let m: Map[string, i32] = map_new(2); m = m.insert(slice_unchecked(s, 0, 1), 1); return m.len(); }\n", []string{"E038"}},
		{"e038-str-as-map-value", "import \"core/map\";\nfunction main(): i32 { let s: string = \"ab\"; let m: Map[string, string] = map_new(2); m = m.insert(\"k\", slice_unchecked(s, 0, 1)); return m.len(); }\n", []string{"E038"}},
		{"str-appended-to-str-array-clean", "function main(): i32 { let s: string = \"ab\"; let out: str[] = []; out = out.append(slice_unchecked(s, 0, 1)); return out.len(); }\n", nil},
		// An array literal of views is a `str[]`, and its elements agree only
		// when their string tags do, in either order. The literal keeps its
		// first element's type, so the declared `string[]` is E003 exactly when
		// the view comes first. A map READ keeps nothing and still takes a view.
		{"str-view-array-literal-e003", "function main(): i32 { let t: string = \"abcdef\"; let xs: string[] = [slice_unchecked(t, 0, 3)]; return xs.len(); }\n", []string{"E003"}},
		{"str-view-array-literal-after-string-e034", "function main(): i32 { let t: string = \"abcdef\"; let xs: string[] = [\"a\", slice_unchecked(t, 0, 3)]; return xs.len(); }\n", []string{"E034"}},
		{"str-view-array-literal-before-string-e034", "function main(): i32 { let t: string = \"abcdef\"; let xs: string[] = [slice_unchecked(t, 0, 3), \"a\"]; return xs.len(); }\n", []string{"E003", "E034"}},
		{"mixed-array-literal-keeps-first-type", "function main(): i32 { let xs: string[] = [1, \"a\"]; return xs.len(); }\n", []string{"E003", "E034"}},
		{"str-view-map-read-get-or-clean", "import \"core/map\";\nfunction main(): i32 { let t: string = \"abcdef\"; let m: Map[string, i32] = map_new(4); return m.get_or(slice_unchecked(t, 0, 3), 0); }\n", nil},
		{"e045-maplit-float-key", "import \"core/map\";\nfunction main(): i32 { let m = Map { 1.0: 10 }; return 0; }\n", []string{"E045"}},
		{"e045-maplit-string-key-ok", "import \"core/map\";\nfunction main(): i32 { let m = Map { \"a\": 1, \"b\": 2 }; return 0; }\n", nil},
		{"e045-maplit-i32-key-ok", "import \"core/map\";\nfunction main(): i32 { let m = Map { 1: 10, 2: 20 }; return 0; }\n", nil},
		{"e045-maplit-used-ok", "import \"core/map\";\nfunction main(): i32 { let m = Map { \"a\": 1 }; return m.get_or(\"a\", 0); }\n", nil},
		// E003 regression guard: an annotated map var assigned a `Map { … }`
		// literal must NOT false-positive E003. The literal desugars to
		// `map_new[_i32](n)…` whose key/value type the self-host leaves
		// `unknown`; type_assignable now treats an unknown side as a wildcard
		// into the annotated Map[K,V] (matching the empty-array rule).
		{"map-ann-empty-ok", "import \"core/map\";\nfunction main(): i32 { let m: Map[string,i32] = Map {}; return 0; }\n", nil},
		{"map-ann-nonempty-ok", "import \"core/map\";\nfunction main(): i32 { let m: Map[string,i32] = Map { \"a\": 1 }; return 0; }\n", nil},
		{"map-ann-i32keys-ok", "import \"core/map\";\nfunction main(): i32 { let m: Map[i32,i32] = Map { 1: 2 }; return 0; }\n", nil},
		// An unannotated literal takes its key and value types from its entries.
		{"maplit-bool-keys-ok", "import \"core/map\";\nfunction main(): i32 { let m = Map { true: 5, false: 9 }; return m.get_or(true, 0); }\n", nil},
		{"maplit-value-type-from-entries", "import \"core/map\";\nfunction main(): i32 { let m = Map { 1: \"a\" }; let n: i32 = m.get_or(1, \"z\"); return n; }\n", []string{"E003"}},
		// `insert` takes the map's columns, as `append` takes the element type.
		{"map-insert-key-type-e038", "import \"core/map\";\nfunction main(): i32 { let m: Map[string, i32] = Map { \"k\": 1 }; m = m.insert(2, 3); return m.len(); }\n", []string{"E038"}},
		{"maplit-insert-key-type-e038", "import \"core/map\";\nfunction main(): i32 { let m = Map { \"k\": 1 }; m = m.insert(2, 3); return m.len(); }\n", []string{"E038"}},
		// A type variable stands for every type, so a value of one meets no
		// concrete destination, in the template or out of it (#11481).
		{"tvar-returned-as-i32-e002", "function f[T](x: T): i32 { return x; }\nfunction main(): i32 { return f(1); }\n", []string{"E002"}},
		{"tvar-let-as-string-e003", "function f[T](x: T): T { let s: string = x; return x; }\nfunction main(): i32 { return f(1); }\n", []string{"E003"}},
		{"tvar-assigned-to-string-e003", "function f[T](x: T): i32 { let s: string = \"a\"; s = x; return s.len(); }\nfunction main(): i32 { return f(1); }\n", []string{"E003"}},
		{"tvar-into-tvar-clean", "function f[T](x: T): T { let y: T = x; return y; }\nfunction main(): i32 { return f(1); }\n", nil},
		{"tvar-array-into-tvar-array-clean", "function f[T](xs: T[]): i32 { let ys: T[] = xs; return ys.len(); }\nfunction main(): i32 { return f([1, 2]); }\n", nil},
		// A bare `map_new(n)` binds neither column, so a written call handing it
		// a key or a value has no K or V to meet (#10214). A destination binds
		// both, and a literal's own inserts bind them from its entries.
		{"mapnew-chain-unbound-columns-e038", "import \"core/map\";\nfunction main(): i32 { return map_new(2).insert(\"a\", 1).len(); }\n", []string{"E038"}},
		{"mapnew-chain-annotated-result-e003-e038", "import \"core/map\";\nfunction main(): i32 { let m: Map[string, i32] = map_new(8).insert(\"x\", 5).insert(\"y\", 7); return m.get_or(\"y\", 0) + m.len(); }\n", []string{"E003", "E038"}},
		{"mapnew-cleared-annotated-result-e003", "import \"core/map\";\nfunction main(): i32 { let m: Map[string, i32] = map_new(8).cleared(); return m.len(); }\n", []string{"E003"}},
		{"mapnew-keys-annotated-result-e003", "import \"core/map\";\nfunction main(): i32 { let ks: string[] = map_new(8).keys(); return ks.len(); }\n", []string{"E003"}},
		{"mapnew-values-annotated-result-e003", "import \"core/map\";\nfunction main(): i32 { let vs: i32[] = map_new(8).values(); return vs.len(); }\n", []string{"E003"}},
		{"mapnew-get-return-e002-e038", "import \"core/map\";\nfunction f(): Option[i32] { return map_new(8).get(\"x\"); }\nfunction main(): i32 { return 0; }\n", []string{"E002", "E038"}},
		{"mapnew-get-or-return-e002-e038", "import \"core/map\";\nfunction f(): i32 { return map_new(8).get_or(\"x\", 0); }\nfunction main(): i32 { return 0; }\n", []string{"E002", "E038"}},
		{"mapnew-iter-annotated-result-e003", "import \"core/map\";\nfunction main(): i32 { let it: MapIter[string, i32] = map_new(8).iter(); return 0; }\n", []string{"E003"}},
		// Pins without's unbound-key E038; E003 is the tuple-to-Map mismatch,
		// independent of whether the result preserves unbound columns.
		{"mapnew-without-annotated-result-e003-e038", "import \"core/map\";\nfunction main(): i32 { let m: Map[string, i32] = map_new(8).without(\"x\"); return m.len(); }\n", []string{"E003", "E038"}},
		{"mapnew-annotated-chain-clean", "import \"core/map\";\nfunction main(): i32 { let m: Map[string, i32] = map_new(8); m = m.insert(\"x\", 5).insert(\"y\", 7); return m.get_or(\"y\", 0) + m.len(); }\n", nil},
		{"mapnew-local-unbound-key-e038", "import \"core/map\";\nfunction main(): i32 { let m = map_new(2); if (m.has(\"a\")) { return 1; } return 0; }\n", []string{"E038"}},
		{"mapnew-local-unbound-insert-e038", "import \"core/map\";\nfunction main(): i32 { let m = map_new(2); return m.insert(1, 2).len(); }\n", []string{"E038"}},
		{"mapnew-annotated-insert-clean", "import \"core/map\";\nfunction main(): i32 { let m: Map[string, i32] = map_new(2); m = m.insert(\"a\", 1); return m.get_or(\"a\", 0); }\n", nil},
		{"maplit-written-insert-clean", "import \"core/map\";\nfunction main(): i32 { let m = Map { \"a\": 1 }; return m.insert(\"b\", 2).get_or(\"b\", 0); }\n", nil},
		// E022: `if let` / `let … else` carry dedicated pattern-binding
		// diagnostics. The self-host parser desugars both to a StmtMatch
		// tagged with `origin` ("if_let" / "let_else"); the checker reads
		// that tag to emit E022 (instead of the generic E035 the desugared
		// shape would otherwise draw) when the source isn't an enum, and —
		// for let-else — when the else branch doesn't diverge. The binding
		// is left unreferenced in the error cases so the Go checker's E001
		// for the now-unbound name (a separate rule) stays out of the set.
		// Cross-checked against the Go checker.
		{"iflet-source-nonenum", "function main(): i32 { let n: i32 = 5; if let Has(v) = n { return 0; } return 0; }\n", []string{"E022"}},
		{"letelse-source-nonenum", "function main(): i32 { let n: i32 = 5; let Has(v) = n else { return 0; }; return 0; }\n", []string{"E022"}},
		{"letelse-source-struct", "struct P { x: i32 }\nfunction main(): i32 { let p: P = P { x: 1 }; let Has(v) = p else { return 0; }; return 0; }\n", []string{"E022"}},
		{"letelse-else-nondiverge", "enum O { Has(i32), Nil }\nfunction main(): i32 { let o: O = Nil; let Has(v) = o else { let x: i32 = 1; }; return 0; }\n", []string{"E022"}},
		// A `break` or `continue` diverges as well as a `return` does.
		{"letelse-else-break-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { let os: O[] = [Has(1), Nil]; let t: i32 = 0; for o in os { let Has(v) = o else { break; }; t = t + v; } return t; }\n", nil},
		{"letelse-else-continue-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { let os: O[] = [Has(1), Nil]; let t: i32 = 0; for o in os { let Has(v) = o else { continue; }; t = t + v; } return t; }\n", nil},
		{"letelse-else-nested-block-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { let o: O = Nil; let Has(v) = o else { { return 0; } }; return v; }\n", nil},
		{"letelse-else-loop-diverge-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { let o: O = Nil; let Has(v) = o else { loop { } }; return v; }\n", nil},
		{"iflet-enum-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { let o: O = Nil; if let Has(v) = o { return v; } return 0; }\n", nil},
		{"letelse-enum-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { let o: O = Nil; let Has(v) = o else { return 0; }; return v; }\n", nil},
		{"iflet-bad-variant", "enum O { Has(i32), Nil }\nfunction main(): i32 { let o: O = Nil; if let Bogus(v) = o { return 0; } return 0; }\n", []string{"E014"}},
		{"iflet-bad-arity", "enum O { Has(i32), Nil }\nfunction main(): i32 { let o: O = Nil; if let Has(a, b) = o { return 0; } return 0; }\n", []string{"E015"}},
		{"qualified-constructor-shadowing", qualifiedConstructorShadowingSource, nil},
		{"qualified-constructor-function-value-mismatch", `enum A { Value(i32) } enum B { Value(string) } function Value(n: i32): B { return B.Value("value"); } function f(): B { let call = Value; return call("wrong"); }`, []string{"E038"}},
		// Partial constructor inference keeps the payload's known type.
		{"result-conversion-ok-narrow", `function f(n: i64): Result[i32, string] { return Ok(n); }`, []string{"E002"}},
		{"result-conversion-err-literal-overflow", `function f(): Result[string, u8] { return Err(300); }`, []string{"E047"}},
		{"result-conversion-qualified-literal-overflow", `function f(): Result[u8, string] { return Result.Ok(300); }`, []string{"E047"}},
		{"result-conversion-nested-literal-overflow", `function f(): Result[Result[u8, string], string] { return Ok(Ok(300)); }`, []string{"E047"}},
		{"result-conversion-option-literal-overflow", `function f(): Option[u8] { return Some(300); }`, []string{"E047"}},
		{"result-conversion-qualified-option-narrow", `function f(n: i64): Option[i32] { return Option.Some(n); }`, []string{"E002"}},
		{"result-conversion-same-name-method", `struct Factory {} impl Factory { function Ok(self: Self, n: i32): Result[u8, string] { return Result.Ok(1u8); } } function f(): Result[u8, string] { let x = Factory {}; return x.Ok(300); }`, nil},
		{"result-conversion-same-name-function", `function Ok(n: i32): Result[u8, string] { return Err("unused"); } function f(): Result[u8, string] { return Ok(300); }`, nil},
		{"result-conversion-unsigned-small-widen", `function f(n: u8): Result[u32, string] { return Ok(n); }`, nil},
		{"result-conversion-unsigned-small-narrow", `function f(n: u32): Result[string, u8] { return Err(n); }`, []string{"E002"}},
		{"result-conversion-signedness-wide", `function f(n: u32): Result[i64, string] { return Ok(n); }`, []string{"E002"}},
		{"result-conversion-pointer-width", `function f(n: usize): Result[u64, string] { return Ok(n); }`, []string{"E002"}},
		{"result-conversion-float-narrow", `function f(n: f64): Result[f32, string] { return Ok(n); }`, []string{"E002"}},
		{"result-conversion-option-widen", `function f(n: i32): Option[i64] { return Some(n); }`, []string{"E002"}},
		{"result-conversion-binding-narrow", `function f(n: i64): void { let r: Result[i32, string] = Ok(n); }`, []string{"E003"}},
		{"result-conversion-argument-narrow", `function take(r: Result[i32, string]): void {} function f(n: i64): void { take(Ok(n)); }`, []string{"E038"}},
		{"result-conversion-err-narrow", `function f(n: i64): Result[string, i32] { return Err(n); }`, []string{"E002"}},
		{"result-conversion-unsigned-narrow", `function f(n: u64): Result[u8, string] { return Result.Ok(n); }`, []string{"E002"}},
		{"result-conversion-sign-change", `function f(n: i32): Result[u32, string] { return Ok(n); }`, []string{"E002"}},
		{"result-conversion-ok-widen", `function f(n: i32): Result[i64, string] { return Ok(n); }`, nil},
		{"result-conversion-err-widen", `function f(n: u8): Result[string, u64] { return Result.Err(n); }`, nil},
		{"result-conversion-literal", `function f(): Result[u8, string] { return Ok(3); }`, nil},
		{"result-conversion-literal-overflow", `function f(): Result[u8, string] { return Ok(300); }`, []string{"E047"}},
		{"result-conversion-option-narrow", `function f(n: i64): Option[i32] { return Some(n); }`, []string{"E002"}},
		{"result-conversion-existing-result", `function f(n: Result[i64, string]): Result[i32, string] { return n; }`, []string{"E002"}},
		{"partial-result-inferred-return", "function main(): i32 { let f = () => { return Ok(3); }; match(f()) { Ok(v) => { return v; }, Err(_) => { return 0; } } }", nil},
		{"partial-result-array-void-exit", `function f(c: boolean): i32 { let g = () => { if(c) { return [Ok(1)]; } return; }; return 0; }`, []string{"E012"}},
		{"partial-result-tuple-void-exit", `function f(c: boolean): i32 { let g = () => { if(c) { return (Ok(1), 2); } return; }; return 0; }`, []string{"E012"}},
		{"partial-result-nested-void-exit", `function f(c: boolean): i32 { let g = () => { if(c) { return [(Ok(1), 2)]; } return; }; return 0; }`, []string{"E012"}},
		{"partial-result-struct-void-exit", `struct Box[T] { v: T } function f(c: boolean): i32 { let g = () => { if(c) { return Box { v: Ok(1) }; } return; }; return 0; }`, []string{"E012"}},
		{"partial-result-inferred-join", "function exercise(flag: boolean): void { let f = () => { if (flag) { return Ok(3); } return Err(\"failure\"); }; match(f()) { Ok(v) => { assert(v == 3); }, Err(e) => { assert(e == \"failure\"); } } }", nil},
		{"partial-result-inferred-conflict", "function exercise(flag: boolean): void { let f = () => { if (flag) { return Ok(3); } return Ok(\"failure\"); }; }", []string{"E002"}},
		{"partial-result-direct-extracted", "function make(): i64 { match (Ok(3)) { Ok(v) => { return v; }, Err(_) => { return 0i64; } } } function main(): i32 { assert(make() == 3i64); return 0; }", nil},
		{"partial-result-direct-range", "function make(): u8 { match (Ok(300)) { Ok(v) => { return v; }, Err(_) => { return 0u8; } } } function main(): i32 { return 0; }", []string{"E047"}},
		{"partial-result-same-parameter", "enum Choice[T, E] { Pair(T, T), Reject(E) } function main(): i32 { let o = Pair(1, 4294967297i64); match(o) { Pair(a, b) => { assert(a == 1i64); assert(b == 4294967297i64); }, Reject(_) => { return 1; } } return 0; }", nil},
		{"partial-result-same-parameter-reversed", "enum Choice[T, E] { Pair(T, T), Reject(E) } function main(): i32 { let o = Pair(4294967297i64, 1); match(o) { Pair(a, b) => { assert(a == 4294967297i64); assert(b == 1i64); }, Reject(_) => { return 1; } } return 0; }", nil},
		{"partial-result-first-use-conflict", "function a(o: Result[i32, string]): void {} function b(o: Result[i64, string]): void {} function main(): i32 { let o = Ok(3); a(o); b(o); return 0; }", []string{"E038"}},
		{"partial-result-closure-context", "function main(): i32 { let o = Ok(2147483647 + 1); let f = (): i64 => { match(o) { Ok(v) => { return v; }, Err(_) => { return 0i64; } } }; assert(f() == 2147483648i64); return 0; }", nil},
		{"partial-result-qualified-collision", "enum Left[T, E] { Pick(T), Reject(E) } enum Right[T, E] { Pick(T), Reject(E) } function make(): Right[i64, string] { let o = Right.Pick(2147483647 + 1); return o; } function main(): i32 { match(make()) { Pick(v) => { assert(v == 2147483648i64); }, Reject(_) => { return 1; } } return 0; }", nil},
		{"partial-result-extracted-range", "function f(): u8 { let o = Ok(300); match (o) { Ok(v) => { return v; }, Err(_) => { return 0u8; } } } function main(): i32 { return 0; }\n", []string{"E047"}},
		{"partial-result-literal-narrow", "function f(): Result[u8, string] { let o = Ok(3); return o; } function main(): i32 { return 0; }\n", nil},
		{"partial-result-literal-range", "function f(): Result[u8, string] { let o = Ok(300); return o; } function main(): i32 { return 0; }\n", []string{"E047"}},
		{"partial-result-literal-negative", "function f(): Result[u32, string] { let o = Ok(-3); return o; } function main(): i32 { return 0; }\n", []string{"E047"}},
		{"partial-result-literal-context", "function f(): Result[i64, string] { let o = Ok(3); return o; } function main(): i32 { return 0; }\n", nil},
		{"partial-result-literal-width-join", "function main(): i32 { let o = if (true) { Ok(1) } else { Ok(4294967297i64) }; match (o) { Ok(v) => { assert(v == 1i64); }, Err(_) => { return 1; } } return 0; }\n", nil},
		{"partial-result-foreign-constructor-widening", "enum Other[T] { Ok(T), No } function f(n: i32): Result[i64, string] { return Other.Ok(n); } function main(): i32 { return 0; }\n", []string{"E002"}},
		{"partial-result-foreign-constructor-literal", "enum Other[T] { Ok(T), No } function f(): Result[i64, string] { return Other.Ok(3); } function main(): i32 { return 0; }\n", []string{"E002"}},
		{"partial-result-method-widening", "struct Factory {} impl Factory { function Ok(self: Self, n: i32): Result[i32, string] { return Result.Ok(n); } } function f(n: i32): Result[i64, string] { let x = Factory {}; return x.Ok(n); } function main(): i32 { return 0; }\n", []string{"E002"}},
		{"partial-result-concrete-width-conflict", "function f(): Result[i64, string] { let n: i32 = 3; let o = Ok(n); return o; } function main(): i32 { return 0; }\n", []string{"E002"}},
		{"partial-result-explicit-width-conflict", "function f(): Result[i64, string] { let o = Ok(3i32); return o; } function main(): i32 { return 0; }\n", []string{"E002"}},
		{"partial-result-ok-local", "function main(): i32 { let o = Ok(3); match (o) { Ok(v) => { return v + 1; }, Err(e) => { return 0; } } }\n", nil},
		{"partial-result-ok-direct", "function main(): i32 { match (Ok(3)) { Ok(v) => { return v + 1; }, Err(e) => { return 0; } } }\n", nil},
		{"partial-result-err-local", "function main(): i32 { let o = Err(\"x\"); match (o) { Ok(v) => { return 0; }, Err(e) => { return e.len(); } } }\n", nil},
		{"partial-result-err-direct", "function main(): i32 { match (Err(\"x\")) { Ok(v) => { return 0; }, Err(e) => { return e.len(); } } }\n", nil},
		{"partial-result-user-enum", "enum Choice[T, E] { Pick(T), Reject(E) } function main(): i32 { let p = Pick(3); match (p) { Pick(n) => { return n + 1; }, Reject(e) => { return 0; } } }\n", nil},
		{"partial-result-qualified-enum", "enum Choice[T, E] { Pick(T), Reject(E) } function main(): i32 { let p = Choice.Reject(\"x\"); match (p) { Pick(_) => { return 0; }, Reject(e) => { return e.len(); } } }\n", nil},
		{"partial-result-qualified-known-error", "enum Choice[T, E] { Pick(T), Reject(E) } function main(): i32 { let p = Choice.Reject(3); match (p) { Pick(_) => { return 0; }, Reject(e) => { return e.len(); } } }\n", []string{"E043"}},
		{"partial-result-context-known-conflict", "enum Choice[T, E] { Pick(T), Reject(E) } function take(p: Choice[i32, string]): i32 { return 0; } function main(): i32 { let p = Choice.Pick(\"x\"); return take(p); }\n", []string{"E038"}},
		{"partial-result-context-nominal-conflict", "enum Choice[T, E] { Pick(T), Reject(E) } enum Other[T, E] { Pick(T), Reject(E) } function take(p: Other[i32, string]): i32 { return 0; } function main(): i32 { let p = Choice.Pick(3); return take(p); }\n", []string{"E038"}},
		{"partial-result-ok-bad", "function main(): i32 { let o = Ok(\"x\"); match (o) { Ok(v) => { return v + 1; }, Err(e) => { return 0; } } }\n", []string{"E009"}},
		{"partial-result-err-bad", "function main(): i32 { let o = Err(3); match (o) { Ok(v) => { return 0; }, Err(e) => { return e.len(); } } }\n", []string{"E043"}},
		{"partial-result-branch-join", "function main(): i32 { let o = if (true) { Ok(3) } else { Err(\"x\") }; match (o) { Ok(v) => { return v + 1; }, Err(e) => { return e.len(); } } }\n", nil},
		{"partial-result-join-known-error", "function main(): i32 { let o = if (true) { Ok(3) } else { Err(5) }; match (o) { Ok(v) => { return v + 1; }, Err(e) => { return e.len(); } } }\n", []string{"E043"}},
		{"partial-result-generic-join-known-error", "function first[T](a: T, b: T): T { return a; } function main(): i32 { let o = first(Ok(3), Err(5)); match (o) { Ok(v) => { return v + 1; }, Err(e) => { return e.len(); } } }\n", []string{"E043"}},
		// E057: `cell_new(v)` constructs a Cell[T]; T must be cycle-free:
		// a scalar, string or owned scalar array. Other composite / reference
		// arguments (struct, tuple, another cell) are E057, reported
		// at the argument. Cross-checked against the Go checker.
		{"cellnew-i32-ok", "function main(): i32 { let c = cell_new(5); return 0; }\n", nil},
		{"cellnew-bytes-ok", "function main(): i32 { let c = cell_new([255 as u8]); return 0; }\n", nil},
		{"cellnew-empty-bytes-ok", "function main(): i32 { let a: u8[] = []; let c: Cell[u8[]] = cell_new(a); return 0; }\n", nil},
		{"cellnew-nested-bytes-bad", "function main(): i32 { let a: u8[][] = [[255 as u8]]; let c = cell_new(a); return 0; }\n", []string{"E057"}},
		{"cellnew-byte-view-bad", "function f(c: Cell[[u8]]): i32 { return 0; } function main(): i32 { return 0; }\n", []string{"E057"}},
		{"cellnew-string-ok", "function main(): i32 { let c = cell_new(\"x\"); return 0; }\n", nil},
		{"cellnew-bool-ok", "function main(): i32 { let c = cell_new(1 < 2); return 0; }\n", nil},
		{"cellnew-struct-bad", "struct P { x: i32 }\nfunction main(): i32 { let p: P = P { x: 1 }; let c = cell_new(p); return 0; }\n", []string{"E057"}},
		{"cellnew-array-ok", "function main(): i32 { let a: i32[] = [1]; let c = cell_new(a); return 0; }\n", nil},
		{"cellnew-reference-array-bad", "function main(): i32 { let a: string[] = [\"a\"]; let c = cell_new(a); return 0; }\n", []string{"E057"}},
		{"cellnew-tuple-bad", "function main(): i32 { let t = (1, 2); let c = cell_new(t); return 0; }\n", []string{"E057"}},
		{"cellnew-nested-bad", "function main(): i32 { let c = cell_new(cell_new(5)); return 0; }\n", []string{"E057"}},
		// E057 ANNOTATION form (#4363 item 2): `Cell[<composite>]` in a
		// param / field / body-var / return annotation — including a Cell
		// nested inside a generic argument, tuple element, or array element
		// spelling — draws E057, anchored at the annotation (the native
		// checker now reports the use site instead of the synthesised Cell
		// decl at 0:0, so the code is visible to this differential). A
		// generic's `Cell[T]` over an in-scope type parameter stays clean
		// (natively a ParamType element; the self-host scopes the walk to
		// non-generic decls). Cross-checked against the Go checker.
		{"cell-annot-param-bad", "struct P { x: i32 }\nfunction f(c: Cell[P]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E057"}},
		{"cell-annot-field-bad", "struct P { x: i32 }\nstruct H { c: Cell[P] }\nfunction main(): i32 { return 0; }\n", []string{"E057"}},
		{"cell-annot-var-bad", "struct P { x: i32 }\nfunction main(): i32 { let c: Cell[P] = cell_new(P { x: 1 }); return 0; }\n", []string{"E057"}},
		{"cell-annot-array-ok", "function f(c: Cell[i32[]]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		{"cell-annot-char-array-bad", "function f(c: Cell[char[]]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E057"}},
		{"cell-inferred-char-array-bad", "function main(): i32 { let a: char[] = [65 as char]; let c = cell_new(a); return 0; }\n", []string{"E057"}},
		{"cell-inferred-empty-char-array-bad", "function main(): i32 { let a: char[] = []; let c = cell_new(a); return 0; }\n", []string{"E057"}},
		{"cell-annot-float-alias-array-ok", "function main(): i32 { let a: float[] = []; let c: Cell[float[]] = cell_new(a); return c.get().len(); }\n", nil},
		{"cell-inferred-float-alias-array-ok", "function main(): i32 { let a: float[] = []; let c = cell_new(a); return c.get().len(); }\n", nil},
		{"cell-annot-reference-array-bad", "function f(c: Cell[string[]]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E057"}},
		{"cell-annot-scalar-arrays-ok", "function f(a: Cell[u32[]], b: Cell[i64[]], c: Cell[u64[]], d: Cell[usize[]], e: Cell[f32[]], f: Cell[f64[]], g: Cell[boolean[]]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		{"cell-annot-tuple-elem-bad", "struct P { x: i32 }\nfunction f(t: (i32, Cell[P])): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E057"}},
		{"cell-annot-generic-arg-bad", "struct P { x: i32 }\nfunction f(o: Option[Cell[P]]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E057"}},
		{"cell-annot-cell-array-bad", "struct P { x: i32 }\nfunction f(a: Cell[P][]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E057"}},
		{"cell-annot-str-bad", "function f(c: Cell[str]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E057"}},
		// Every integer and float width is a scalar element, as natively.
		{"cell-annot-scalars-ok", "function f(a: (i32, Cell[f32]), b: Cell[u8], c: Cell[usize]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		// E051 self-reassign move admission (#4873 step 0): a LOCAL passed
		// exactly once, directly, in an `own` position of its OWN
		// reassignment's RHS is a transfer — admitted by both checkers
		// (native SelfReassignOwnMoveArg / self-host ow_self_move_admits).
		// Binding the result to a different name is a move too when nothing
		// reads the local afterwards (#10864); a second read of the local in
		// the same RHS stays E051.
		{"own-self-reassign-ok", "struct B { items: i32[] }\nfunction grow(own b: B, x: i32): B { return B { items: b.items.append(x) }; }\nfunction main(): i32 {\n    let a: B = B { items: [] };\n    a = grow(a, 1);\n    a = grow(a, 2);\n    return a.items.len();\n}\n", nil},
		{"own-other-name-last-use-ok", "struct B { items: i32[] }\nfunction grow(own b: B, x: i32): B { return B { items: b.items.append(x) }; }\nfunction main(): i32 {\n    let a: B = B { items: [] };\n    let c: B = grow(a, 1);\n    return c.items.len();\n}\n", nil},
		{"own-second-read-bad", "struct B { items: i32[] }\nfunction grow(own b: B, x: i32): B { return B { items: b.items.append(x) }; }\nfunction main(): i32 {\n    let a: B = B { items: [7] };\n    a = grow(a, a.items[0]);\n    return a.items.len();\n}\n", []string{"E051"}},
		// The store may come after straight-line statements that do not name
		// the local (#11093); a read or a branch between keeps it live.
		{"own-store-after-gap-ok", "struct P { a: i32[], b: boolean[] }\nfunction step(own xs: i32[], own fl: boolean[]): P { return P { a: xs, b: fl }; }\nfunction take(own fl: boolean[]): i32 { return fl.len(); }\nfunction main(): i32 {\n    let xs: i32[] = [];\n    let fl: boolean[] = [false];\n    let i: i32 = 0;\n    while (i < 3) {\n        let p: P = step(xs, fl);\n        xs = p.a;\n        fl = p.b;\n        i = i + 1;\n    }\n    return xs.len() + fl.len();\n}\n", nil},
		{"own-store-after-unrelated-var-ok", "struct P { a: i32[], b: boolean[] }\nfunction step(own xs: i32[], own fl: boolean[]): P { return P { a: xs, b: fl }; }\nfunction take(own fl: boolean[]): i32 { return fl.len(); }\nfunction main(): i32 {\n    let fl: boolean[] = [false];\n    let n: i32 = take(fl);\n    let m: i32 = 3;\n    fl = [true];\n    return n + m + fl.len();\n}\n", nil},
		{"own-store-after-read-bad", "struct P { a: i32[], b: boolean[] }\nfunction step(own xs: i32[], own fl: boolean[]): P { return P { a: xs, b: fl }; }\nfunction take(own fl: boolean[]): i32 { return fl.len(); }\nfunction main(): i32 {\n    let fl: boolean[] = [false];\n    let n: i32 = take(fl);\n    let m: i32 = fl.len();\n    fl = [true];\n    return n + m + fl.len();\n}\n", []string{"E051"}},
		{"own-store-after-branch-bad", "struct P { a: i32[], b: boolean[] }\nfunction step(own xs: i32[], own fl: boolean[]): P { return P { a: xs, b: fl }; }\nfunction take(own fl: boolean[]): i32 { return fl.len(); }\nfunction main(): i32 {\n    let k: i32 = 1;\n    let fl: boolean[] = [false];\n    let n: i32 = take(fl);\n    if (k > 0) { k = k + 1; }\n    fl = [true];\n    return n + k + fl.len();\n}\n", []string{"E051"}},
		// The same three, one nesting level in: the admission is a
		// STATEMENT-level fact, so a walk that reaches a nested body as one
		// flat expression loses it and flags the transfer (#7452).
		{"own-self-reassign-local-func-ok", "struct B { items: i32[] }\nfunction grow(own b: B, x: i32): B { return B { items: b.items.append(x) }; }\nfunction main(): i32 {\n    function build(): i32 {\n        let a: B = B { items: [] };\n        a = grow(a, 1);\n        return a.items.len();\n    }\n    return build();\n}\n", nil},
		{"own-other-name-last-use-local-func-ok", "struct B { items: i32[] }\nfunction grow(own b: B, x: i32): B { return B { items: b.items.append(x) }; }\nfunction main(): i32 {\n    function build(): i32 {\n        let a: B = B { items: [] };\n        let c: B = grow(a, 1);\n        return c.items.len();\n    }\n    return build();\n}\n", nil},
		// A local function's OWN `own` parameter is owned inside its body, so
		// it transfers onward like a top-level one's.
		{"own-local-func-param-transfer-ok", "function consume(own xs: i32[]): i32 { return xs[0]; }\nfunction main(): i32 {\n    function inner(own b: i32[]): i32 { return consume(b); }\n    return inner([1]);\n}\n", nil},
		// E051 superseded-field move admission (#8186): `a = S { ...a, f:
		// g(.., a.f, ..) }` — or the literal returned — with `a` an own
		// param or a local moves the one field the store supersedes
		// (native SupersededFieldOwnMoveArgs / self-host
		// ow_field_move_fields). A borrowed base, a second read of the
		// field, the base handed to the callee by another route, the
		// result stored into a DIFFERENT field, or a different binding
		// name stay E051.
		{"own-field-move-own-base-ok", "struct Cfi { rules: i32[], n: i32 }\nstruct Asm { code: i32[], cfi: Cfi }\nfunction record(own s: Cfi, v: i32): Cfi { return Cfi { rules: s.rules.append(v), n: s.n + 1 }; }\nfunction step(own a: Asm, v: i32): Asm {\n    a = Asm { ...a, cfi: record(a.cfi, v) };\n    return a;\n}\nfunction main(): i32 { return 0; }\n", nil},
		{"own-field-move-local-ok", "struct Cfi { rules: i32[], n: i32 }\nstruct Asm { code: i32[], cfi: Cfi }\nfunction record(own s: Cfi, v: i32): Cfi { return Cfi { rules: s.rules.append(v), n: s.n + 1 }; }\nfunction main(): i32 {\n    let a: Asm = Asm { code: [], cfi: Cfi { rules: [], n: 0 } };\n    a = Asm { ...a, cfi: record(a.cfi, a.code.len()) };\n    return a.cfi.n;\n}\n", nil},
		{"own-field-move-return-ok", "struct Cfi { rules: i32[], n: i32 }\nstruct Asm { code: i32[], cfi: Cfi }\nfunction record(own s: Cfi, v: i32): Cfi { return Cfi { rules: s.rules.append(v), n: s.n + 1 }; }\nfunction step(own a: Asm, v: i32): Asm {\n    return Asm { ...a, cfi: record(a.cfi, v) };\n}\nfunction main(): i32 { return 0; }\n", nil},
		{"own-field-move-borrowed-base-bad", "struct Cfi { rules: i32[], n: i32 }\nstruct Asm { code: i32[], cfi: Cfi }\nfunction record(own s: Cfi, v: i32): Cfi { return Cfi { rules: s.rules.append(v), n: s.n + 1 }; }\nfunction step(a: Asm, v: i32): Asm {\n    a = Asm { ...a, cfi: record(a.cfi, v) };\n    return a;\n}\nfunction main(): i32 { return 0; }\n", []string{"E051"}},
		{"own-field-move-borrowed-return-bad", "struct Cfi { rules: i32[], n: i32 }\nstruct Asm { code: i32[], cfi: Cfi }\nfunction record(own s: Cfi, v: i32): Cfi { return Cfi { rules: s.rules.append(v), n: s.n + 1 }; }\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, cfi: record(a.cfi, v) };\n}\nfunction main(): i32 { return 0; }\n", []string{"E051"}},
		{"own-field-move-second-read-bad", "struct Cfi { rules: i32[], n: i32 }\nstruct Asm { code: i32[], cfi: Cfi }\nfunction record(own s: Cfi, v: i32): Cfi { return Cfi { rules: s.rules.append(v), n: s.n + 1 }; }\nfunction step(own a: Asm): Asm {\n    a = Asm { ...a, cfi: record(a.cfi, a.cfi.n) };\n    return a;\n}\nfunction main(): i32 { return 0; }\n", []string{"E051"}},
		{"own-field-move-base-aliased-bad", "struct Cfi { rules: i32[], n: i32 }\nstruct Asm { code: i32[], cfi: Cfi }\nfunction record(own s: Cfi, v: i32): Cfi { return Cfi { rules: s.rules.append(v), n: s.n + 1 }; }\nfunction record2(own s: Cfi, a: Asm): Cfi { return Cfi { rules: s.rules.append(a.code.len()), n: s.n + 1 }; }\nfunction step(own a: Asm): Asm {\n    a = Asm { ...a, cfi: record2(a.cfi, a) };\n    return a;\n}\nfunction main(): i32 { return 0; }\n", []string{"E051"}},
		{"own-field-move-other-field-bad", "struct Cfi { rules: i32[], n: i32 }\nstruct Asm { code: i32[], cfi: Cfi, cfi2: Cfi }\nfunction record(own s: Cfi, v: i32): Cfi { return Cfi { rules: s.rules.append(v), n: s.n + 1 }; }\nfunction step(own a: Asm): Asm {\n    a = Asm { ...a, cfi2: record(a.cfi, 1) };\n    return a;\n}\nfunction main(): i32 { return 0; }\n", []string{"E051"}},
		{"own-field-move-kept-alive-bad", "struct Cfi { rules: i32[], n: i32 }\nstruct Asm { code: i32[], cfi: Cfi }\nfunction record(own s: Cfi, v: i32): Cfi { return Cfi { rules: s.rules.append(v), n: s.n + 1 }; }\nfunction main(): i32 {\n    let a: Asm = Asm { code: [], cfi: Cfi { rules: [], n: 0 } };\n    let b: Asm = Asm { ...a, cfi: record(a.cfi, 1) };\n    return b.cfi.n + a.cfi.n;\n}\n", []string{"E051"}},
		{"own-field-move-nested-borrowed-bad", "struct Cfi { rules: i32[], n: i32 }\nstruct Asm { code: i32[], cfi: Cfi }\nfunction record(own s: Cfi, v: i32): Cfi { return Cfi { rules: s.rules.append(v), n: s.n + 1 }; }\nfunction main(): i32 {\n    let a: Asm = Asm { code: [], cfi: Cfi { rules: [], n: 0 } };\n    function inner(a: Asm): Asm {\n        a = Asm { ...a, cfi: record(a.cfi, 1) };\n        return a;\n    }\n    return inner(a).cfi.n;\n}\n", []string{"E051"}},
		{"cell-annot-i32-ok", "function f(c: Cell[i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		{"cell-annot-string-ok", "function f(c: Cell[string]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		{"cell-annot-generic-param-ok", "function f[T](c: Cell[T]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		// `str` inside a generic ARGUMENT is a real native type, so no E064
		// (regression pin for the generic-arg-widening false positive fixed
		// alongside item 2).
		{"generic-arg-str-ok", "function f(o: Option[str]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		// E063: returning a `[T]` slice that views function-local storage is a
		// use-after-free (the backing array dies with the frame). The check is
		// conservative — only slices provably viewing local storage fire.
		// String slices copy, slices of a parameter stay valid with the
		// caller's owner, and returning the owned array itself is a move.
		// Cross-checked against the Go checker.
		{"e063-slice-local-array", "function f(): [i32] { let xs: i32[] = [1, 2, 3]; return xs[0:2]; }\nfunction main(): i32 { return 0; }\n", []string{"E063"}},
		{"e063-slice-array-literal", "function f(): [i32] { return [1, 2, 3][0:2]; }\nfunction main(): i32 { return 0; }\n", []string{"E063"}},
		{"e063-slice-local-bound", "function f(): [i32] { let xs: i32[] = [1, 2, 3]; let s = xs[0:2]; return s; }\nfunction main(): i32 { return 0; }\n", []string{"E063"}},
		{"e063-slice-of-param-ok", "function f(xs: i32[]): [i32] { return xs[0:2]; }\nfunction main(): i32 { return 0; }\n", nil},
		{"e063-slice-of-param-bound-ok", "function f(xs: i32[]): [i32] { let s = xs[0:2]; return s; }\nfunction main(): i32 { return 0; }\n", nil},
		{"e063-string-slice-ok", "function f(s: string): str { return slice_unchecked(s, 0, 2); }\nfunction main(): i32 { return 0; }\n", nil},
		{"e063-return-owned-array-ok", "function f(): i32[] { let xs: i32[] = [1, 2, 3]; return xs; }\nfunction main(): i32 { return 0; }\n", nil},
		{"e063-slice-local-not-returned-ok", "function f(): i32 { let xs: i32[] = [1, 2, 3]; let s = xs[0:2]; return s[0]; }\nfunction main(): i32 { return 0; }\n", nil},
		// E063 through a CALLEE that hands back a view of one of its own
		// parameters — the same indirect route E065 has, in the sibling
		// rule. The owned-array row is the one a slice-value chase misses:
		// `a` passed straight in IS the storage, not a slice of it. The
		// param row is the precision control.
		{"e063-callee-launder", "function idsl(x: [i32]): [i32] { return x; }\nfunction f(): [i32] { let a: i32[] = [1, 2, 3]; return idsl(a[0:2]); }\nfunction main(): i32 { return 0; }\n", []string{"E063"}},
		{"e063-callee-two-hop", "function idsl(x: [i32]): [i32] { return x; }\nfunction hop(x: [i32]): [i32] { return idsl(x); }\nfunction f(): [i32] { let a: i32[] = [1, 2, 3]; return hop(a[0:2]); }\nfunction main(): i32 { return 0; }\n", []string{"E063"}},
		// The caller is declared FIRST and the chain runs through METHODS, so
		// the summary fixpoint needs a second round, reached only through the
		// call names a method call contributes to the worklist.
		{"e063-callee-method-chain-caller-first", "struct Box { n: i32 }\nfunction g(): [i32] { let a: i32[] = [1, 2, 3]; let b: Box = Box { n: 0 }; return b.pick(a[0:2]); }\nfunction (b: Box) pick(x: [i32]): [i32] { return b.launder(x); }\nfunction (b: Box) launder(x: [i32]): [i32] { return x; }\nfunction main(): i32 { return 0; }\n", []string{"E063"}},
		{"e063-callee-owned-array-arg", "function idarr(x: i32[]): i32[] { return x; }\nfunction f(): [i32] { let a: i32[] = [1, 2, 3]; return idarr(a)[0:1]; }\nfunction main(): i32 { return 0; }\n", []string{"E063"}},
		{"e063-callee-other-arg-ok", "function second(a: [i32], b: [i32]): [i32] { return b; }\nfunction f(p: i32[]): [i32] { let a: i32[] = [1, 2, 3]; return second(a[0:2], p[0:1]); }\nfunction main(): i32 { return 0; }\n", nil},
		{"e063-callee-param-ok", "function idsl(x: [i32]): [i32] { return x; }\nfunction f(p: i32[]): [i32] { return idsl(p[0:2]); }\nfunction main(): i32 { return 0; }\n", nil},
		// An element-polymorphic receiver method is HOISTED to a free
		// function, and the hoist rebuilds the decl rather than copying it
		// — so the `[T]`-vs-`T[]` flag the report filter reads has to be carried
		// along or this stops being reported with no other symptom.
		{"e063-receiver-method-slice-ret", "function (xs: T[]) danger(): [T] { let local: T[] = [1, 2, 3]; return local[0:1]; }\nfunction main(): i32 { return 0; }\n", []string{"E063"}},
		// Slicing an unbound TEMPORARY. The callee hands back storage it
		// built, so the CALLER's frame owns it and the slice dangles —
		// binding it first (`let t = mkarr(); return t[0:1];`) was already
		// rejected, and the temporary is the same storage without a name.
		// The param row is the control: a callee handing back its ARGUMENT
		// is handing back storage that outlives the call.
		{"e063-sliced-temporary", "function mkarr(): i32[] { let a: i32[] = [1, 2, 3]; return a; }\nfunction f(): [i32] { return mkarr()[0:1]; }\nfunction main(): i32 { return 0; }\n", []string{"E063"}},
		{"e063-sliced-temporary-param-ok", "function idarr(x: i32[]): i32[] { return x; }\nfunction f(p: i32[]): [i32] { return idarr(p)[0:1]; }\nfunction main(): i32 { return 0; }\n", nil},
		// The E065 twin: a view of a string the CALLEE allocated. The
		// param row and the literal row are the controls — a callee handing
		// back its argument hands back storage that outlives the call, and
		// a literal is immortal however many calls it is passed through.
		{"e065-sliced-temporary", "function mkstr(): string { return \"a\" + \"b\"; }\nfunction f(): str { return slice_unchecked(mkstr(), 0, 1); }\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		{"e065-sliced-temporary-param-ok", "function idstr(s: string): string { return s; }\nfunction f(p: string): str { return slice_unchecked(idstr(p), 0, 1); }\nfunction main(): i32 { return 0; }\n", nil},
		{"e065-sliced-literal-ok", "function lit(): string { return \"hello\"; }\nfunction f(): str { return slice_unchecked(lit(), 0, 1); }\nfunction main(): i32 { return 0; }\n", nil},
		// E065 for a MapIter cursor (#9920): the cursor reads its map's columns
		// through a raw pointer, so one over a map this frame built dangles
		// once returned. A cursor over a parameter, or a parameter's field,
		// is anchored to the caller's map and stays accepted.
		{"e065-cursor-local-map", "import \"core/map\";\nfunction f(): MapIter[i32, i32] { let m: Map[i32, i32] = map_new(4); m = m.insert(1, 2); return m.iter(); }\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		{"e065-cursor-through-local", "import \"core/map\";\nfunction f(): MapIter[i32, i32] { let m: Map[i32, i32] = map_new(4); let it = m.iter(); return it; }\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		{"e065-cursor-in-array-literal", "import \"core/map\";\nfunction f(): MapIter[i32, i32][] { let m: Map[i32, i32] = map_new(4); return [m.iter()]; }\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		{"e065-cursor-shadowed", "import \"core/map\";\nfunction f(m: Map[i32, i32]): MapIter[i32, i32] { let mm: Map[i32, i32] = map_new(4); let it: MapIter[i32, i32] = mm.iter(); if (m.len() > 0) { let it2: MapIter[i32, i32] = m.iter(); let it: MapIter[i32, i32] = it2; } return it; }\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		{"e065-cursor-reassigned", "import \"core/map\";\nfunction f(m: Map[i32, i32]): MapIter[i32, i32] { let mm: Map[i32, i32] = map_new(4); let it: MapIter[i32, i32] = m.iter(); if (m.len() > 0) { it = mm.iter(); } return it; }\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		{"e065-cursor-param-ok", "import \"core/map\";\nfunction f(m: Map[i32, i32]): MapIter[i32, i32] { return m.iter(); }\nfunction main(): i32 { return 0; }\n", nil},
		{"e065-cursor-param-field-ok", "import \"core/map\";\nstruct B { m: Map[i32, i32] }\nfunction f(b: B): MapIter[i32, i32] { let it = b.m.iter(); return it; }\nfunction main(): i32 { return 0; }\n", nil},
		// An owned `T[]` return MOVES its storage to the caller, so a
		// function handing a local array back THROUGH A CALLEE that passes
		// one through is not returning a view and must stay accepted.
		// Every function is summarised, so without a return-type filter on
		// the REPORT this draws E063 — the shape that failed 154 tests
		// across a dozen lanes once a program importing `core/bigint`
		// reached the self-host compiler, while every stdlib-free row
		// above stayed green. The direct form is e063-return-owned-array-ok
		// and passes either way, so it does not gate this.
		{"e063-owned-return-through-callee-ok", "function idarr(x: i32[]): i32[] { return x; }\nfunction f(): i32[] { let a: i32[] = [1, 2, 3]; return idarr(a); }\nfunction main(): i32 { return 0; }\n", nil},
		// E023 (unknown enum): an unknown-BASE generic annotation survives
		// type resolution as an "unknown enum" (native resolveType keeps
		// ast.EnumType), so a match / if-let on the value draws E023 at the
		// scrutinee — alongside the E064 the annotation itself draws. A
		// known enum / builtin Option match stays clean.
		{"e023-match-unknown-enum", "function main(s: Statuus[i32]): i32 {\n    match (s) {\n        _ => { return 0; }\n    }\n}\n", []string{"E023", "E064"}},
		{"e023-iflet-unknown-enum", "function main(s: Statuus[i32]): i32 {\n    if let Some(v) = s {\n        return 0;\n    }\n    return 0;\n}\n", []string{"E023", "E064"}},
		{"e023-known-enum-match-ok", "enum Color { Red, Green }\nfunction f(c: Color): i32 { match (c) { Red => { return 1; }, Green => { return 2; } } }\nfunction main(): i32 { return 0; }\n", nil},
		{"e023-option-match-ok", "function main(): i32 { let o: Option[i32] = Some(3); match (o) { Some(v) => { return v; }, None => { return 0; } } }\n", nil},
		// E044 (unsupported capture type): a lambda capturing a value with
		// no runtime representation — a void call result — draws E044, the
		// one shape the native captureSink rejects. A generic parameter's
		// value is concrete once the function is instantiated, so capturing
		// one is clean; so are scalar captures and shadowing lambda params.
		{"e044-capture-generic-param-ok", "function f[T](x: T): i32 {\n    let g = () => x;\n    return 0;\n}\nfunction main(): i32 { return f(1); }\n", nil},
		{"e044-capture-void", "function v(): void { return; }\nfunction main(): i32 {\n    let x = v();\n    let g = () => x;\n    return 0;\n}\n", []string{"E044"}},
		{"e044-capture-scalar-ok", "function main(): i32 {\n    let x = 5;\n    let g = () => x;\n    return g();\n}\n", nil},
		{"e044-capture-shadowed-ok", "function v(): void { return; }\nfunction main(): i32 {\n    let x = v();\n    let g = (x: i32) => x;\n    return g(1);\n}\n", nil},
		// A nested `function` declaration desugars to a lambda, and the rule
		// applies to it the same way. It is the one parser-synthesised lambda
		// the programmer did write, so the E044 gate admits it by origin
		// (parser.is_written_lambda_origin) rather than by an empty one.
		{"e044-capture-void-nested-fn", "function v(): void { return; }\nfunction main(): i32 {\n    let x = v();\n    function pick(): i32 { x; return 0; }\n    return pick();\n}\n", []string{"E044"}},
		{"e044-nested-fn-shadowed-ok", "function v(): void { return; }\nfunction main(): i32 {\n    let x = v();\n    function pick(x: i32): i32 { return x; }\n    return pick(1);\n}\n", nil},
		// A `use` callback is the other lambda the programmer wrote that the
		// parser gives an origin. Native desugars `use` to a local function and
		// runs the same capture sink over it, so the rule applies there too.
		// A `use` into a generic callee whose type parameter nothing binds:
		// E032 for the binding, E038 for the callback, E040 for the parameter
		// (#10833).
		{"use-unbound-generic-callback", "function apply[T, I](it: I, cb: (T) => i32): i32 { return 0; }\nfunction main(): i32 {\n    use n <- apply(5);\n    return 1;\n}\n", []string{"E032", "E038", "E040"}},
		{"e044-capture-void-use-callback", "function v(): void { return; }\nfunction apply(n: i32, cb: (i32) => i32): i32 { return cb(n); }\nfunction main(): i32 {\n    let x = v();\n    use n <- apply(41);\n    x;\n    return n;\n}\n", []string{"E044"}},
		{"e044-capture-generic-use-callback-ok", "function apply(n: i32, cb: (i32) => i32): i32 { return cb(n); }\nfunction f[T](x: T): i32 {\n    use n <- apply(41);\n    x;\n    return n;\n}\nfunction main(): i32 { return f(1); }\n", nil},
		// A suspect declared AFTER the `use` lives inside the callback body, so
		// it is not a capture — and the walk must not report it as one.
		{"e044-use-callback-declares-suspect-ok", "function v(): void { return; }\nfunction apply(n: i32, cb: (i32) => i32): i32 { return cb(n); }\nfunction main(): i32 {\n    use n <- apply(41);\n    let x = v();\n    return n;\n}\n", nil},
		// The rule asks what a lambda CAPTURES, not what its body mentions. A
		// suspect whose only appearance is a NESTED binder's own name is not a
		// capture, in any of the three syntaxes that reach the check — native
		// accepts all three, and a syntactic mention test rejected them.
		{"e044-nested-lambda-shadow-ok", "function v(): void { return; }\nfunction main(): i32 {\n    let x = v();\n    let outer: () => i32 = (): i32 => {\n        let g: (i32) => i32 = (x: i32) => x;\n        return g(1);\n    };\n    return outer();\n}\n", nil},
		{"e044-nested-fn-inner-shadow-ok", "function v(): void { return; }\nfunction main(): i32 {\n    let x = v();\n    function outer(): i32 {\n        function helper(x: i32): i32 { return x; }\n        return helper(1);\n    }\n    return outer();\n}\n", nil},
		{"e044-use-callback-nested-shadow-ok", "function v(): void { return; }\nfunction apply(n: i32, cb: (i32) => i32): i32 { return cb(n); }\nfunction main(): i32 {\n    let x = v();\n    use n <- apply(41);\n    let g: (i32) => i32 = (x: i32) => x;\n    return n;\n}\n", nil},
		// Writing a suspect is capturing it. An assignment target is a field on
		// the statement rather than an ExprIdent, so a mention test could not
		// see one; the free-variable walk counts it, as native does.
		{"e044-capture-void-write-only", "function v(): void { return; }\nfunction main(): i32 {\n    let x = v();\n    let g: () => i32 = (): i32 => {\n        x = 42;\n        return 0;\n    };\n    return g();\n}\n", []string{"E003", "E044"}},
		// The suspect DECLARED inside a lambda, captured by one nested in it
		// (#9777). The in-lambda sweep reaches this shape for every row it
		// wraps, but only by accident of the wrapping; written out, it is the
		// case the descent exists for and it belongs in the corpus on its own.
		{"e044-capture-void-declared-in-a-lambda", "function v(): void { return; }\nfunction main(): i32 {\n    let f = (): i32 => {\n        let x = v();\n        let g = () => x;\n        return 0;\n    };\n    return f();\n}\n", []string{"E044"}},
		// A lambda's own type-variable parameter is as concrete as the
		// enclosing generic's, so a lambda nested in its body may capture it.
		{"e044-capture-generic-lambda-param-ok", "function outer[T](a: T): i32 {\n    let f = (x: T): i32 => {\n        let g = () => x;\n        return 0;\n    };\n    return f(a);\n}\nfunction main(): i32 { return outer(1); }\n", nil},
		// The negative: an ordinary binding captured by a nested lambda draws
		// nothing. The descent widens what is walked, so without this a rule
		// that reported on every nested lambda would look correct.
		{"e044-nested-lambda-clean", "function main(): i32 {\n    let f = (): i32 => {\n        let y = 1;\n        let g = () => y;\n        return g();\n    };\n    return f();\n}\n", nil},
		// E053 (`fip` no-allocation): array literals, string concatenation
		// and calls to non-fip functions are rejected inside a `fip
		// function`; scalar arithmetic and fip→fip calls are clean.
		//
		// A CONSTRUCTOR is not an E053 shape violation in any tier (#9602):
		// the checker cannot tell a rebuild that reuses a dead donor's box
		// from one that allocates a fresh box, so E068 at the IR decides and
		// both checkers stay quiet here. internal/oracle/ir/fip_verify_test.go owns
		// the budget half; this file pins only that the two CHECKERS agree.
		{"e053-fip-array-lit", "fip function mk(): i32 {\n    let a = [1, 2];\n    return a[0];\n}\nfunction main(): i32 { return mk(); }\n", []string{"E053"}},
		{"e053-fip-struct-lit-ok", "struct P { x: i32 }\nfip function mk(a: i32): i32 {\n    let p = P { x: a };\n    return p.x;\n}\nfunction main(): i32 { return mk(1); }\n", nil},
		{"e053-fip-struct-rebuild-ok", "struct P { x: i32, y: i32 }\nfip function bump(own p: P): P {\n    return P { ...p, x: p.x + 1 };\n}\nfunction main(): i32 { let q: P = bump(P { x: 1, y: 2 }); return q.x; }\n", nil},
		{"e053-fip-concat", "fip function j(a: string, b: string): string {\n    return a + b;\n}\nfunction main(): i32 { return 0; }\n", []string{"E053"}},
		{"e053-fip-nonfip-call", "function g(): i32 { return 1; }\nfip function f(): i32 {\n    return g();\n}\nfunction main(): i32 { return f(); }\n", []string{"E053"}},
		{"e053-fip-arith-ok", "fip function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n", nil},
		{"e053-fip-fipcall-ok", "fip function g(): i32 { return 1; }\nfip function f(): i32 { return g() + 1; }\nfunction main(): i32 { return f(); }\n", nil},
		// `fbip` and the graded `fip(n)` / `fbip(n)` run the SAME walk with the
		// constructor rule relaxed — the IR's E068 budget owns those sites
		// (#6639 slice 3). Everything else stays rejected for them, including
		// array literals, for which no reuse pairing exists.
		{"e053-fbip-struct-lit-ok", "struct P { x: i32 }\nfbip function mk(a: i32): i32 {\n    let p = P { x: a };\n    return p.x;\n}\nfunction main(): i32 { return mk(1); }\n", nil},
		{"e053-graded-fip-struct-lit-ok", "struct P { x: i32 }\nfip(1) function mk(a: i32): i32 {\n    let p = P { x: a };\n    return p.x;\n}\nfunction main(): i32 { return mk(1); }\n", nil},
		{"e053-fbip-enum-ctor-ok", "enum L { C(i32), N }\nfbip function mk(a: i32): i32 {\n    match (C(a)) { C(v) => { return v; }, N => { return 0; } }\n}\nfunction main(): i32 { return mk(1); }\n", nil},
		{"e053-fbip-array-lit", "fbip function mk(): i32 {\n    let a = [1, 2];\n    return a[0];\n}\nfunction main(): i32 { return mk(); }\n", []string{"E053"}},
		{"e053-fbip-concat", "fbip function j(a: string, b: string): string {\n    return a + b;\n}\nfunction main(): i32 { return 0; }\n", []string{"E053"}},
		// The call rule is asymmetric: `fbip` may call `fip` or `fbip`, but a
		// `fip` function may not call a `fbip` one — its claim is stronger.
		{"e053-fbip-calls-fip-ok", "fip function g(): i32 { return 1; }\nfbip function f(): i32 { return g() + 1; }\nfunction main(): i32 { return f(); }\n", nil},
		{"e053-fbip-calls-fbip-ok", "fbip function g(): i32 { return 1; }\nfbip function f(): i32 { return g() + 1; }\nfunction main(): i32 { return f(); }\n", nil},
		{"e053-fip-calls-fbip", "fbip function g(): i32 { return 1; }\nfip function f(): i32 { return g() + 1; }\nfunction main(): i32 { return f(); }\n", []string{"E053"}},
		// E065 (returning a `str` view of a function-local string): a local
		// owned string (annotated or inferred) escaping through a str return
		// draws E065, incl. through a local `str` binding chase. A literal,
		// a parameter view, and a str-of-param binding stay clean.
		{"e065-local-string", "function f(): str {\n    let s: string = \"hello\";\n    return s;\n}\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		{"e065-inferred-local", "function f(): str {\n    let s = \"hi\";\n    return s;\n}\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		{"e065-str-binding-chase", "function f(): str {\n    let s: string = \"hello\";\n    let t: str = s;\n    return t;\n}\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		{"e065-literal-ok", "function f(): str {\n    return \"hi\";\n}\nfunction main(): i32 { return 0; }\n", nil},
		{"e065-param-slice-ok", "function f(p: string): str {\n    return slice_unchecked(p, 0, 2);\n}\nfunction main(): i32 { return 0; }\n", nil},
		{"e065-str-of-param-ok", "function f(p: str): str {\n    let t: str = p;\n    return t;\n}\nfunction main(): i32 { return 0; }\n", nil},
		// E032 (`use` binding-type inference): an un-annotated `use` whose
		// callee has no signature (the E001 is also reported) or whose last
		// parameter isn't a function draws E032 (the E038 arg-type mismatch
		// is also reported on the desugared call); an inferrable or annotated
		// `use` is clean.
		{"e032-use-nosig", "function main(): i32 {\n    use n <- q(1);\n    return n;\n}\n", []string{"E001", "E032"}},
		{"e032-use-lastparam-not-fn", "function add(x: i32, y: i32): i32 { return x + y; }\nfunction main(): i32 {\n    use n <- add(1);\n    return n;\n}\n", []string{"E032", "E038"}},
		{"e032-use-ok", "function apply(x: i32, cb: (i32) => i32): i32 { return cb(x); }\nfunction main(): i32 {\n    use n <- apply(41);\n    return n + 1;\n}\n", nil},
		{"e032-use-annotated-ok", "function apply(x: i32, cb: (i32) => i32): i32 { return cb(x); }\nfunction main(): i32 {\n    use n: i32 <- apply(41);\n    return n + 1;\n}\n", nil},
		// An inferred `use` binding is typed from the callee's callback slot
		// (native inferUseParam), so a read of it at the wrong type is E003
		// rather than an unknown that passes: through a module function, a
		// fn-typed local shadowing nothing, and inside a lambda's block body.
		{"e003-use-binding-mistyped", "function apply(x: i32, cb: (i32) => i32): i32 { return cb(x); }\nfunction main(): i32 {\n    use n <- apply(41);\n    let s: string = n;\n    return n;\n}\n", []string{"E003"}},
		{"e003-use-binding-through-local-mistyped", "function taker(f: (string) => i32): i32 { return f(\"hi\"); }\nfunction g(): i32 {\n    let t = taker;\n    use x <- t();\n    let n: i32 = x;\n    return n;\n}\nfunction main(): i32 { return g(); }\n", []string{"E003"}},
		{"e003-use-binding-inside-lambda-mistyped", "function give(x: i32, cb: (i32) => i32): i32 { return cb(x); }\nfunction main(): i32 {\n    let f = (): i32 => {\n        use n <- give(41);\n        let s: string = n;\n        return n + 1;\n    };\n    return f() - 42;\n}\n", []string{"E003"}},
		// @inline / @noinline function attributes (#4412 Rec §14): native
		// consults them in the IR inliner; the self-host parser
		// parse-tolerates and drops them. Both checkers must accept the
		// annotated program cleanly.
		{"inline-hints-ok", "@inline\nfunction fast(x: i32): i32 { return x + x; }\n@noinline\nfunction slow(x: i32): i32 { return x + 1; }\nfunction main(): i32 { return fast(3) + slow(4); }\n", nil},
		// E067 (@must_consume obligation, docs/MUST-CONSUME.md): a value of a
		// marked struct/enum type must be consumed on every control-flow path
		// before its binding leaves scope — call-argument, return, match,
		// transfer to another binding, or store into another marked type.
		// Laundering into an unmarked container (array/tuple/unmarked struct)
		// is a violation at the store site; loop bodies are opaque; `own`
		// params are exempt. Mirrors internal/check/checker/mustconsume_test.go;
		// cross-checked against the Go oracle by the differential leg.
		{"mc-plain-leak", "@must_consume\nstruct Ticket { id: i32 }\nfunction sink(t: Ticket): Ticket { return t; }\nfunction f(): void { let t: Ticket = Ticket { id: 1 }; }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-one-arm-leak", "@must_consume\nstruct Ticket { id: i32 }\nfunction sink(t: Ticket): Ticket { return t; }\nfunction f(n: i32): void { let t: Ticket = Ticket { id: 1 }; if (n > 0) { sink(t); } }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-early-return-leak", "@must_consume\nstruct Ticket { id: i32 }\nfunction sink(t: Ticket): Ticket { return t; }\nfunction f(n: i32): i32 { let t: Ticket = Ticket { id: 1 }; if (n > 5) { return 1; } sink(t); return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-both-arms-ok", "@must_consume\nstruct Ticket { id: i32 }\nfunction sink(t: Ticket): Ticket { return t; }\nfunction f(n: i32): void { let t: Ticket = Ticket { id: 1 }; if (n > 0) { sink(t); } else { sink(t); } }\nfunction main(): i32 { return 0; }\n", nil},
		{"mc-transfer-ok", "@must_consume\nstruct Ticket { id: i32 }\nfunction f(): Ticket { let t: Ticket = Ticket { id: 1 }; let u: Ticket = t; return u; }\nfunction main(): i32 { return 0; }\n", nil},
		{"mc-unannotated-leak", "@must_consume\nstruct Ticket { id: i32 }\nfunction f(): void { let t = Ticket { id: 1 }; }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-match-consumes-ok", "@must_consume\nenum Pending { Reply(string), Close }\nfunction f(p: Pending): i32 { match (p) { Reply(s) => { print(s); }, Close => { print(\"closed\"); } } return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		{"mc-enum-leak", "@must_consume\nenum Pending { Reply(string), Close }\nfunction f(): void { let p: Pending = Close; }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-laundered-array", "@must_consume\nstruct Ticket { id: i32 }\nfunction f(): void { let t: Ticket = Ticket { id: 1 }; let arr: Ticket[] = [t]; }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-laundered-struct", "@must_consume\nstruct Ticket { id: i32 }\nstruct Box { inner: Ticket }\nfunction f(): void { let t: Ticket = Ticket { id: 1 }; let b: Box = Box { inner: t }; }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-laundered-tuple", "@must_consume\nstruct Ticket { id: i32 }\nfunction f(): (Ticket, i32) { let t: Ticket = Ticket { id: 1 }; return (t, 3); }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-marked-envelope-ok", "@must_consume\nstruct Ticket { id: i32 }\n@must_consume\nstruct Envelope { inner: Ticket }\nfunction open_env(e: Envelope): Envelope { return e; }\nfunction f(): void { let t: Ticket = Ticket { id: 1 }; let e: Envelope = Envelope { inner: t }; open_env(e); }\nfunction main(): i32 { return 0; }\n", nil},
		{"mc-closure-capture", "@must_consume\nstruct Ticket { id: i32 }\nfunction f(): void { let t: Ticket = Ticket { id: 1 }; let g = (): i32 => { return t.id; }; print(\"captured\"); }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-overwrite", "@must_consume\nenum Pending { Reply(string), Close }\nfunction f(): void { let p: Pending = Close; p = Reply(\"again\"); match (p) { Reply(s) => { }, Close => { } } }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-nonown-param-leak", "@must_consume\nstruct Ticket { id: i32 }\nfunction f(t: Ticket): void { print(\"ignored\"); }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-own-param-ok", "@must_consume\nstruct Ticket { id: i32 }\nfunction take(own t: Ticket): void { print(\"own\"); }\nfunction f(): void { take(Ticket { id: 9 }); }\nfunction main(): i32 { return 0; }\n", nil},
		{"mc-loop-consume-leak", "@must_consume\nstruct Ticket { id: i32 }\nfunction sink(t: Ticket): Ticket { return t; }\nfunction f(n: i32): void { let t: Ticket = Ticket { id: 1 }; let i: i32 = 0; while (i < n) { sink(t); i = i + 1; } }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-field-read-neutral-ok", "@must_consume\nstruct Ticket { id: i32 }\nfunction sink(t: Ticket): Ticket { return t; }\nfunction f(): i32 { let t: Ticket = Ticket { id: 7 }; let n: i32 = t.id; sink(t); return n; }\nfunction main(): i32 { return 0; }\n", nil},
		{"mc-match-expr-consumes-ok", "@must_consume\nenum Pending { Reply(string), Close }\nfunction f(p: Pending): i32 { let r: i32 = match (p) { Reply(s) => 1, Close => 0 }; return r; }\nfunction main(): i32 { return 0; }\n", nil},
		{"mc-nested-block-leak", "@must_consume\nstruct Ticket { id: i32 }\nfunction f(c: boolean): void { if (c) { let t: Ticket = Ticket { id: 1 }; } }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-unmarked-unaffected-ok", "struct Plain { id: i32 }\nfunction f(): void { let p: Plain = Plain { id: 1 }; }\nfunction main(): i32 { return 0; }\n", nil},
		// Mixed-SIGNEDNESS integer operands (native's commonIntegerWidth).
		// The self-host tested only "both are integers", so `i32 + u32` and
		// every sibling were accepted where native rejects them — a whole
		// class of programs the two compilers disagreed on, invisible to the
		// differential suites because they compile the same (valid) sources
		// through both. One row per operator family: the rule lives in six
		// arms and each was widened separately.
		{"e009-mixed-sign-add", "function main(): i32 { let n: i32 = 1; let u: u32 = 2; return (n + u) as i32; }\n", []string{"E009"}},
		{"e009-mixed-sign-sub", "function main(): i32 { let n: i32 = 1; let u: u32 = 2; return (n - u) as i32; }\n", []string{"E009"}},
		{"e009-mixed-sign-shift", "function main(): i32 { let n: i32 = 1; let u: u32 = 2; return (u << n) as i32; }\n", []string{"E009"}},
		{"e009-mixed-sign-bitand", "function main(): i32 { let n: i32 = 1; let u: u32 = 2; return (n & u) as i32; }\n", []string{"E009"}},
		{"e009-mixed-sign-order", "function main(): i32 { let n: i32 = 1; let u: u32 = 2; if (n < u) { return 1; } return 0; }\n", []string{"E009"}},
		{"e009-mixed-sign-saturating", "function main(): i32 { let n: i32 = 1; let u: u32 = 2; return (n +| u) as i32; }\n", []string{"E009"}},
		{"e009-mixed-sign-u8", "function main(): i32 { let s: string = \"abc\"; let n: i32 = 1; return n + s[0]; }\n", []string{"E009"}},
		// The three shapes that must STAY accepted, which is what stops the
		// rule from being a blanket "widths must match". Same signedness at
		// different widths auto-widens; an unsuffixed literal is read at the
		// other operand's width (native's Polymorphic, unsettled_int_literal
		// here); an explicit cast settles it.
		{"mixed-width-same-sign-clean", "function main(): i32 { let n: i32 = 1; let q: i64 = 5; return (q + n) as i32; }\n", nil},
		{"unsigned-plus-literal-clean", "function main(): i32 { let s: string = \"abc\"; let b: u8 = s[0]; return (b + 1) as i32; }\n", nil},
		{"mixed-sign-cast-clean", "function main(): i32 { let n: i32 = 1; let u: u32 = 2; return (u as i32) + n; }\n", nil},
		// E076: a parameter default must be a constant expression, because it
		// is pasted into each CALL SITE and a name inside it resolves in the
		// caller's scope (#8445). Native's Fill declines the whole program
		// then, so the defaulted call also draws E004; the self-host fills
		// inside check_module and declines the same way. The positive rows
		// pin that a checker driver no longer reports E004 for a defaulted
		// call it never filled.
		{"default-reads-parameter", "function f(a: i32, b: i32 = a * 2): i32 { return a + b; }\nfunction main(): i32 { let a: i32 = 100; return f(1); }\n", []string{"E004", "E076"}},
		{"default-calls-function", "function size(): i32 { return 8; }\nfunction f(n: i32 = size()): i32 { return n; }\nfunction main(): i32 { return f(); }\n", []string{"E004", "E076"}},
		{"default-reads-bare-name", "function f(n: i32 = limit): i32 { return n; }\nfunction main(): i32 { return f(); }\n", []string{"E004", "E076"}},
		{"default-nested-in-arithmetic", "function f(a: i32, b: i32 = 1 + (a * 2)): i32 { return a + b; }\nfunction main(): i32 { return f(1); }\n", []string{"E004", "E076"}},
		{"default-field-access", "struct Config { timeout: i32 }\nfunction f(a: i32, b: i32 = config.timeout): i32 { return a + b; }\nfunction main(): i32 { let config: Config = Config { timeout: 41 }; return f(1); }\n", []string{"E004", "E076"}},
		{"default-index", "function f(a: i32, b: i32 = xs[0]): i32 { return a + b; }\nfunction main(): i32 { let xs: i32[] = [41, 9]; return f(1); }\n", []string{"E004", "E076"}},
		{"default-cast", "function f(a: i32, b: i32 = n as i32): i32 { return a + b; }\nfunction main(): i32 { let n: i64 = 41; return f(1); }\n", []string{"E004", "E076"}},
		{"default-lambda", "function f(a: i32, g: (i32) => i32 = (x: i32) => x + n): i32 { return g(a); }\nfunction main(): i32 { let n: i32 = 41; return f(1); }\n", []string{"E004", "E076"}},
		{"default-struct-literal", "struct P { v: i32 }\nfunction f(a: i32, p: P = P { v: n }): i32 { return a + p.v; }\nfunction main(): i32 { let n: i32 = 41; return f(1); }\n", []string{"E004", "E076"}},
		{"default-array-literal", "function f(a: i32, xs: i32[] = [n]): i32 { return a + xs[0]; }\nfunction main(): i32 { let n: i32 = 41; return f(1); }\n", []string{"E004", "E076"}},
		{"default-field-access-under-arithmetic", "struct Config { timeout: i32 }\nfunction f(a: i32, b: i32 = 1 + config.timeout): i32 { return a + b; }\nfunction main(): i32 { let config: Config = Config { timeout: 41 }; return f(1); }\n", []string{"E004", "E076"}},
		{"default-number-ok", "function listen(port: i32, backlog: i32 = 128): i32 { return port + backlog; }\nfunction main(): i32 { return listen(80); }\n", nil},
		{"default-string-ok", "function greet(name: string, greeting: string = \"hello\"): string { return greeting + name; }\nfunction main(): i32 { let s: string = greet(\"x\"); return s.len(); }\n", nil},
		{"default-bool-ok", "function go(a: i32, verbose: boolean = true): i32 { if (verbose) { return a; } return 0; }\nfunction main(): i32 { return go(1); }\n", nil},
		{"default-arithmetic-ok", "function scale(x: i32, factor: i32 = 2 * 3): i32 { return x * factor; }\nfunction main(): i32 { return scale(1); }\n", nil},
		{"default-negative-ok", "function off(x: i32, delta: i32 = -1): i32 { return x + delta; }\nfunction main(): i32 { return off(1); }\n", nil},
		{"default-two-one-supplied-ok", "function f(a: i32, b: i32 = 2, c: i32 = 3): i32 { return a + b + c; }\nfunction main(): i32 { return f(1); }\n", nil},
		// A default may name a top-level const: both compilers fold consts
		// into defaults ahead of the E076 check, so the name never reaches
		// the whitelist, and never reaches a call site where a caller local
		// of the same name would capture it.
		{"default-names-const-ok", "const LIMIT: i32 = 128;\nfunction listen(port: i32, backlog: i32 = LIMIT): i32 { return port + backlog; }\nfunction main(): i32 { return listen(80); }\n", nil},
		{"default-names-const-caller-shadows-ok", "const LIMIT: i32 = 128;\nfunction listen(port: i32, backlog: i32 = LIMIT): i32 { return port + backlog; }\nfunction main(): i32 { let LIMIT: i32 = 1; return listen(80) + LIMIT; }\n", nil},
		{"default-const-of-const-in-arithmetic-ok", "const LIMIT: i32 = 128;\nconst STEP: i32 = LIMIT / 2;\nfunction advance(n: i32, by: i32 = STEP + 1): i32 { return n + by; }\nfunction main(): i32 { return advance(1); }\n", nil},
		{"default-names-string-const-ok", "const GREETING: string = \"hello\";\nfunction greet(name: string, greeting: string = GREETING): string { return greeting + name; }\nfunction main(): i32 { let s: string = greet(\"x\"); return s.len(); }\n", nil},
		// E077: named arguments that do not resolve — a callee that is not a
		// named free function, a positional after a named one, a name no
		// parameter has, a parameter given twice — and E004 for a required
		// parameter nothing supplied. A call that does not resolve is left as
		// written, so the arity check still sees its argument count, as native.
		{"named-on-method", "struct S { v: i32 }\nimpl S { function m(self: Self, a: i32): i32 { return self.v + a; } }\nfunction main(): i32 { let s: S = S { v: 1 }; return s.m(a = 1); }\n", []string{"E077"}},
		{"named-on-fn-value", "function main(): i32 { let g: (i32) => i32 = (x: i32) => x + 1; return g(x = 1); }\n", []string{"E077"}},
		{"named-unknown-param", "function listen(port: i32, backlog: i32 = 128): i32 { return port + backlog; }\nfunction main(): i32 { return listen(port = 80, backlogg = 5); }\n", []string{"E077"}},
		{"named-duplicate", "function f(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return f(a = 1, a = 2); }\n", []string{"E077"}},
		{"positional-after-named", "function f(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return f(a = 1, 2); }\n", []string{"E077"}},
		{"named-missing-required", "function f(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return f(b = 2); }\n", []string{"E004"}},
		{"named-too-many-positional", "function f(a: i32, b: i32 = 2): i32 { return a + b; }\nfunction main(): i32 { return f(1, 2, b = 3); }\n", []string{"E004", "E077"}},
		{"named-reorder-ok", "function f(a: i32, b: i32 = 2): i32 { return a - b; }\nfunction main(): i32 { return f(b = 1, a = 3); }\n", nil},
		{"named-fills-default-ok", "function f(a: i32, b: i32 = 2, c: i32 = 3): i32 { return a + b + c; }\nfunction main(): i32 { return f(1, c = 5); }\n", nil},
		// The unit `()` is void's one value, so every destination spelled
		// void takes it: a local, a variant payload, a tuple element, a
		// parameter (#8759). The self-host records it as the constant 0 it
		// lowers to, which is why it typed i32 and drew E003 at each of them.
		{"unit-value-destinations-ok", "function sink(u: ()): i32 { return 0; }\nfunction main(): i32 { let u: () = (); let v = (); let r: Result[(), i32] = Ok(()); let t: ((), i32) = ((), 1); return sink(u) + sink(v) + sink(t.0) + t.1; }\n", nil},
		// And a void-returning CALL is still not a value: it produced nothing
		// to store, which is the whole reason the unit has a spelling.
		{"e072-void-call-payload", "function nothing(): void { return; }\nfunction main(): i32 { let r: Result[(), i32] = Ok(nothing()); return 0; }\n", []string{"E072"}},
		// The unit is not a numeral: it settles at no destination width.
		{"unit-not-a-number", "function main(): i32 { let f: f64 = (); return 0; }\n", []string{"E003"}},
		// #8461: five rules that fired on programs native accepts, which is
		// what held E001 / E036 / E044 off the compile path. Each is silent
		// now, so each is a row that must stay silent.
		//
		// A nested position in a `for` pattern uses the binder encoding as a
		// parenthesised group, and the split ran over every comma — binding
		// "(a" and "b)" and leaving `a` and `b` undefined.
		{"for-nested-tuple-pattern", "function main(): i32 { let deep: ((i32, i32), string)[] = [((2, 3), \"xy\")]; let s: i32 = 0; for ((a, b), c) in deep { s = s + a * b + c.len(); } return s; }\n", nil},
		// The `@` whole-value binder names the scrutinee; nothing bound it, so
		// every read of the name was an undefined E001.
		{"at-binder-names-the-scrutinee", "enum One { Only(string) }\nfunction whole(o: One): i32 { return 1; }\nfunction main(): i32 { let o: One = Only(\"e\"); match (o) { w @ Only(v) => { return whole(w) + v.len(); } } }\n", nil},
		// An if-EXPRESSION is an IIFE here and has no counterpart in the
		// native AST, so its erased-`T` operands read as closure captures with
		// no runtime representation (E044). Only a lambda the PROGRAMMER wrote
		// is a capture site.
		{"if-expr-iife-is-not-a-capture", "function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }\nfunction main(): i32 { return pick(true, 1, 2); }\n", nil},
		// An associated function reached through its ENUM: the qualified
		// -variant arm claimed the call before the associated resolution ran,
		// and reported a variant nobody wrote (E036).
		{"enum-qualified-associated-fn", "trait Empty { function empty(): Self; }\nenum Opt { Nothing, Just(i32) }\nimpl Empty for Opt { function empty(): Self { return Nothing; } }\nfunction main(): i32 { let o: Opt = Opt.empty(); match (o) { Nothing => { return 0; }, Just(n) => { return n; } } }\n", nil},
		// A tuple / struct / literal match is REPLACED at parse time by a
		// done-flag if/else chain that falls through by construction, so
		// reading the chain said every such function could fall off its end
		// (E052). The `if` carries the arms as written.
		// WIT resource handles: `own R` lends to `borrow R`, a bare `R` is an
		// owned handle, and a handle and an integer convert in neither direction.
		{"resource-handle-owned-lent-to-a-borrow", "@import(\"wasi:io/poll@0.2.0\", \"pollable\")\nresource Pollable;\n@import(\"wasi:clocks/monotonic-clock@0.2.0\", \"subscribe-duration\")\nfunction subscribe(ns: u64): own Pollable;\n@import(\"wasi:io/poll@0.2.0\", \"[method]pollable.ready\")\nfunction ready(h: borrow Pollable): boolean;\n@import(\"wasi:io/poll@0.2.0\", \"[resource-drop]pollable\")\nfunction drop_pollable(h: own Pollable): void;\n" + "function main(): i32 { let p: own Pollable = subscribe(0 as u64); if (ready(p)) { drop_pollable(p); return 1; } drop_pollable(p); return 0; }\n", nil},
		{"resource-handle-bare-name-is-owned", "@import(\"wasi:io/poll@0.2.0\", \"pollable\")\nresource Pollable;\n@import(\"wasi:clocks/monotonic-clock@0.2.0\", \"subscribe-duration\")\nfunction subscribe(ns: u64): own Pollable;\n@import(\"wasi:io/poll@0.2.0\", \"[method]pollable.ready\")\nfunction ready(h: borrow Pollable): boolean;\n@import(\"wasi:io/poll@0.2.0\", \"[resource-drop]pollable\")\nfunction drop_pollable(h: own Pollable): void;\n" + "function f(): Pollable { return subscribe(0 as u64); }\nfunction main(): i32 { let p: Pollable = f(); drop_pollable(p); return 0; }\n", nil},
		{"resource-handle-borrow-into-an-owned-parameter", "@import(\"wasi:io/poll@0.2.0\", \"pollable\")\nresource Pollable;\n@import(\"wasi:clocks/monotonic-clock@0.2.0\", \"subscribe-duration\")\nfunction subscribe(ns: u64): own Pollable;\n@import(\"wasi:io/poll@0.2.0\", \"[method]pollable.ready\")\nfunction ready(h: borrow Pollable): boolean;\n@import(\"wasi:io/poll@0.2.0\", \"[resource-drop]pollable\")\nfunction drop_pollable(h: own Pollable): void;\n" + "function f(b: borrow Pollable): void { drop_pollable(b); }\nfunction main(): i32 { return 0; }\n", []string{"E038"}},
		{"resource-handle-into-an-integer", "@import(\"wasi:io/poll@0.2.0\", \"pollable\")\nresource Pollable;\n@import(\"wasi:clocks/monotonic-clock@0.2.0\", \"subscribe-duration\")\nfunction subscribe(ns: u64): own Pollable;\n@import(\"wasi:io/poll@0.2.0\", \"[method]pollable.ready\")\nfunction ready(h: borrow Pollable): boolean;\n@import(\"wasi:io/poll@0.2.0\", \"[resource-drop]pollable\")\nfunction drop_pollable(h: own Pollable): void;\n" + "function main(): i32 { let p: own Pollable = subscribe(0 as u64); let x: i32 = p; return x; }\n", []string{"E003"}},
		{"resource-handle-lent-to-a-borrow-of-another-resource", "@import(\"wasi:io/poll@0.2.0\", \"pollable\")\nresource Pollable;\n@import(\"local:test/res@0.1.0\", \"thing\")\nresource Thing;\n@import(\"wasi:clocks/monotonic-clock@0.2.0\", \"subscribe-duration\")\nfunction subscribe(ns: u64): own Pollable;\n@import(\"local:test/res@0.1.0\", \"[method]thing.poke\")\nfunction poke(t: borrow Thing): void;\nfunction main(): i32 { let p: own Pollable = subscribe(0 as u64); poke(p); return 0; }\n", []string{"E038"}},
		{"integer-into-a-resource-handle", "@import(\"wasi:io/poll@0.2.0\", \"pollable\")\nresource Pollable;\n@import(\"wasi:clocks/monotonic-clock@0.2.0\", \"subscribe-duration\")\nfunction subscribe(ns: u64): own Pollable;\n@import(\"wasi:io/poll@0.2.0\", \"[method]pollable.ready\")\nfunction ready(h: borrow Pollable): boolean;\n@import(\"wasi:io/poll@0.2.0\", \"[resource-drop]pollable\")\nfunction drop_pollable(h: own Pollable): void;\n" + "function main(): i32 { let p: own Pollable = 5; return 0; }\n", []string{"E003"}},
		// A resource shares the nominal namespace with structs, enums and union
		// aliases: a collision is E006 and the name keeps its other meaning.
		{"resource-redeclared", "resource Pollable;\nresource Pollable;\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		{"resource-beside-a-struct", "@import(\"wasi:io/poll@0.2.0\", \"pollable\")\nresource Pollable;\nstruct Pollable { v: i32 }\nfunction main(): i32 { let p: Pollable = Pollable { v: 1 }; return p.v; }\n", []string{"E006"}},
		{"resource-beside-an-enum", "@import(\"wasi:io/poll@0.2.0\", \"pollable\")\nresource Pollable;\nenum Pollable { A, B }\nfunction main(): i32 { let p: Pollable = Pollable.A; return 0; }\n", []string{"E006"}},
		{"resource-beside-a-generic-struct", "@import(\"local:test/res@0.1.0\", \"box\")\nresource Box;\nstruct Box[T] { v: T }\nfunction main(): i32 { let b: Box[i32] = Box { v: 1 }; return b.v; }\n", []string{"E006"}},
		{"resource-beside-a-union-alias", "struct A { v: i32 }\nstruct B { w: i32 }\ntype X = A | B;\n@import(\"local:test/res@0.1.0\", \"x\")\nresource X;\nfunction main(): i32 { let x: X = A { v: 1 }; return 0; }\n", []string{"E006"}},
		// A union whose members are not all distinct non-generic structs is E016
		// and never becomes an enum, so a resource may take its name.
		{"union-over-enums", "enum A { P, Q }\nenum B { R, T }\ntype X = A | B;\nfunction main(): i32 { return 0; }\n", []string{"E016"}},
		{"union-duplicate-member", "struct A { v: i32 }\ntype X = A | A;\nfunction main(): i32 { return 0; }\n", []string{"E016"}},
		{"union-generic-member-without-arguments", "struct Box[T] { v: T }\nstruct B { w: i32 }\ntype X = Box | B;\nfunction main(): i32 { return 0; }\n", []string{"E016"}},
		{"union-plain-member-with-arguments", "struct A { v: i32 }\nstruct B { w: i32 }\ntype X = A[i32] | B;\nfunction main(): i32 { return 0; }\n", []string{"E016"}},
		{"union-generic-member-arity", "struct Two[T, U] { a: T, b: U }\nstruct B { w: i32 }\ntype X = Two[i32] | B;\nfunction main(): i32 { return 0; }\n", []string{"E016"}},
		{"union-generic-member-with-arguments", "struct Box[T] { v: T }\nstruct B { w: i32 }\ntype X = Box[i32] | B;\nfunction main(): i32 { return 0; }\n", nil},
		{"union-generic-alias", "struct Leaf[T] { v: T }\nstruct Lit { v: i32 }\ntype Tree[T] = Leaf[T] | Lit;\nfunction main(): i32 { let t: Tree[i32] = Lit { v: 1 }; return 0; }\n", nil},
		{"union-generic-alias-arity", "struct Leaf[T] { v: T }\nstruct Lit { v: i32 }\ntype Tree[T] = Leaf[T] | Lit;\nfunction main(): i32 { let t: Tree[i32, i32] = Lit { v: 1 }; return 0; }\n", []string{"E019"}},
		// A built-in's annotation is held to the built-in's arity (#10897).
		{"builtin-struct-arity-map", "import \"core/map\";\nfunction f(x: Map[string]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E019"}},
		{"builtin-struct-arity-cell", "function f(x: Cell[i32, i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E019"}},
		{"builtin-enum-arity-option", "function f(x: Option[i32, i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E019"}},
		{"builtin-enum-arity-result", "function f(x: Result[i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E019"}},
		{"builtin-arity-ok", "function f(x: Cell[i32], y: Option[i32], z: Result[i32, string], w: IoError): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		{"enum-generic-arity", "struct Lit { v: i32 }\nenum Tree[T] { Leaf(T), Lit(Lit) }\nfunction f(t: Tree[i32, i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E019"}},
		// A user enum taking a built-in struct's name is E010, as a struct would
		// be, so `Cell[i32]` never has to choose between them (#10855).
		{"enum-shadows-builtin-arity", "enum Cell { Text(string), Num(i32) }\nfunction f(c: Cell[i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E010"}},
		{"union-bare-cell-member", "struct B { w: i32 }\ntype X = Cell | B;\nfunction main(): i32 { return 0; }\n", []string{"E016"}},
		{"union-bare-map-member", "struct B { w: i32 }\ntype X = Map | B;\nfunction main(): i32 { return 0; }\n", []string{"E016"}},
		{"union-bare-mapiter-member", "struct B { w: i32 }\ntype X = MapIter | B;\nfunction main(): i32 { return 0; }\n", []string{"E016"}},
		// A union that desugars joins the enums after the declared ones, so a
		// name already an enum's is E006 on the alias, in either source order.
		{"union-alias-after-an-enum-of-its-name", "struct A { v: i32 }\nstruct B { w: i32 }\nenum X { P, Q }\ntype X = A | B;\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		{"union-alias-after-an-enum-of-its-name-used", "struct A { v: i32 }\nstruct B { w: i32 }\nenum X { P, Q }\ntype X = A | B;\nfunction main(): i32 { let x: X = A { v: 1 }; return 0; }\n", []string{"E003", "E006"}},
		{"union-alias-before-an-enum-of-its-name", "struct A { v: i32 }\nstruct B { w: i32 }\ntype X = A | B;\nenum X { P, Q }\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		{"union-alias-named-option", "struct A { v: i32 }\nstruct B { w: i32 }\ntype Option = A | B;\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		{"union-alias-redeclared", "struct A { v: i32 }\nstruct B { w: i32 }\ntype X = A | B;\ntype X = A | B;\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		{"union-alias-after-a-refused-one", "enum A { P, Q }\nstruct B { w: i32 }\nstruct C { u: i32 }\ntype X = A | B;\ntype X = B | C;\nfunction main(): i32 { return 0; }\n", []string{"E016"}},
		{"resource-beside-a-union-over-enums", "enum A { P, Q }\nenum B { R, T }\ntype X = A | B;\n@import(\"local:test/res@0.1.0\", \"x\")\nresource X;\nfunction main(): i32 { return 0; }\n", []string{"E016"}},
		// A built-in struct or enum name is taken before any resource.
		{"resource-named-like-builtin-reader", "@import(\"local:test/res@0.1.0\", \"reader\")\nresource Reader;\nfunction f(r: Reader): i32 { return r.fd; }\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		{"resource-named-like-builtin-cell", "@import(\"local:test/res@0.1.0\", \"cell\")\nresource Cell;\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		{"resource-named-like-builtin-option", "@import(\"local:test/res@0.1.0\", \"option\")\nresource Option;\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		{"resource-named-like-builtin-map", "@import(\"local:test/res@0.1.0\", \"map\")\nresource Map;\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		{"tuple-match-exhausts-the-function", "function f(t: (i32, i32)): i32 { match (t) { (0, b) => { return b; }, (a, _) => { return a; } } }\nfunction main(): i32 { return f((0, 7)); }\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(checkerBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], checkerBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			out := runCheckerDriver(t, cmd, tc.name)
			got := driverCodes(out)

			want := uniqueSortedCodes(tc.want)
			if !equalStrings(got, want) {
				t.Errorf("%s: self-host codes = %v, want %v", tc.name, got, want)
			}
			// Differential: the self-host codes must match what the Go
			// checker reports for the same source — the FULL, unfiltered
			// code set (the historical selfHostImplementedCodes filter is
			// gone; the port covers every code the Go checker emits).
			goCodes := goCheckerCodes(t, dir, tc.src)
			if !equalStrings(got, goCodes) {
				t.Errorf("%s: self-host codes %v disagree with Go checker %v (unfiltered)", tc.name, got, goCodes)
			}
		})
	}

	// Every row again, with main's body inside a lambda. See the comment on
	// wrapMainBodyInLambda: this is the gate that makes "does the class of
	// lambda-body holes still exist?" a count rather than a judgement.
	applied := 0
	for _, tc := range cases {
		wrapped := wrapMainBodyInLambda(tc.src)
		if wrapped == "" {
			continue
		}
		applied++
		t.Run("in-lambda/"+tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(checkerBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], checkerBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(wrapped))
			got := driverCodes(runCheckerDriver(t, cmd, tc.name))
			want := goCheckerCodes(t, dir, wrapped)
			why, listed := lambdaBodyDivergences[tc.name]
			switch {
			case equalStrings(got, want) && listed:
				t.Errorf("%s: listed as a lambda-body divergence (%s) but now AGREES with the Go checker (%v).\n"+
					"    Remove it from lambdaBodyDivergences — an exception that no longer applies hides the next one.", tc.name, why, got)
			case !equalStrings(got, want) && !listed:
				t.Errorf("%s: in a lambda body the self-host reports %v, the Go checker %v.\n"+
					"    A pass that walks statements and never enters an expression cannot see inside a lambda —\n"+
					"    and a nested named function is one. Reach the body, or list the row with its owner.", tc.name, got, want)
			}
		})
	}
	if applied < 600 {
		t.Errorf("lambda-body parity applied to only %d rows; the transform has stopped matching the corpus "+
			"and this gate is passing vacuously", applied)
	}
}

// ---- lambda-body parity ---------------------------------------------------
//
// A checker pass that walks STATEMENTS and never enters an expression cannot
// see anything written inside a lambda body — and since a nested named function
// parses as a StmtVar holding an ExprLambda, that means inside any local
// function too. Six passes have had this hole (#7363, #7387, #7390, #7395,
// #7410, and stmts_assign_diags here), each found one at a time.
//
// Finding them one at a time is the problem this gate solves. It reuses the
// corpus above — every row whose main returns i32 and contains a return — and
// re-runs it with main's BODY moved into a lambda. The diagnostics must not
// change: a lambda is not a place where checks stop applying.
//
// Sizing matters. Eight hand-picked triggers put the differing count at 1 and
// read as a closed class; the same transform over the whole corpus put it at
// 37. Hand-picked rows cannot answer "is this class closed?" — a corpus can.
var mainBodyRE = regexp.MustCompile(`(?s)function main\(\):\s*i32\s*\{(.*)\}\s*$`)

// wrapMainBodyInLambda rewrites `function main(): i32 { BODY }` so BODY runs
// inside a lambda main immediately calls. Returns "" when the row does not fit
// the shape — a non-i32 main, or a body with no return to give the lambda.
func wrapMainBodyInLambda(src string) string {
	m := mainBodyRE.FindStringSubmatchIndex(src)
	if m == nil {
		return ""
	}
	body := src[m[2]:m[3]]
	if !strings.Contains(body, "return") {
		return ""
	}
	return src[:m[0]] + "function main(): i32 {\n    let __lam = (): i32 => {\n" +
		strings.Trim(body, "\n") + "\n    };\n    return __lam();\n}\n"
}

// lambdaBodyDivergences are the rows whose diagnostics the self-host still
// drops once the body moves into a lambda. Each is a KNOWN under-report with a
// named owner, not a row that may quietly differ: the map is exact in both
// directions, so a listed row that starts agreeing fails too and must be
// removed. Emptying this map closes the class.
// EMPTY, and that is the state to keep it in. The four E044 rows that used to
// live here were one defect — the capture walk never entered a lambda body, so
// a capture site inside one was invisible whatever syntax held it — and #9777
// closed the class by descending with a scope of the lambda's own. A row added
// back names a new hole, not a new exception.
var lambdaBodyDivergences = map[string]string{}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSelfHostCheckerDifferentialX86_64 is the pure-differential verification
// harness for the self-host checker. For each program it runs BOTH the
// self-host checker (the codes driver) and the production Go checker, and
// asserts they agree on the diagnostic code SET (unfiltered). Unlike TestSelfHostCheckerCodesX86_64 it carries NO
// hardcoded expected codes — the Go checker is the sole oracle — so it catches
// a self-host FALSE POSITIVE (or missing diagnostic) on any construct in the
// corpus without the test author having to predict the codes.
//
// It is seeded with the real-world tuple / generic / union / method-chain
// constructs the stdlib leans on (annotated tuples, tuple-of-union, nested
// tuples, generic tuple containers, …). The whole-compiler fixpoint/bundle
// gate never runs the self-host CHECKER over the stdlib, so before teaching
// checker.fern a rule that touches these shapes (e.g. tuple-type assignability,
// builtin-method return types) add the relevant valid programs here: a
// regression then fails loudly in this differential instead of remaining a
// latent false positive only triggered when someone runs the self-host checker
// over real code.
// lastUsePrelude declares the consumers and producers the e051-last-use rows
// share.
const lastUsePrelude = "struct W { d: i32[], n: i32 }\nstruct Pair { a: W, b: W }\n" +
	"function keep(own xs: i32[], k: i32): i32 { return xs[0] + k; }\n" +
	"function eat(own w: W): i32 { return w.n; }\n" +
	"function grow(own xs: i32[], v: i32): i32[] { return xs.append(v); }\n" +
	"function mk(n: i32): i32[] { return [n]; }\n" +
	"function mkw(n: i32): W { return W { d: [n], n: n }; }\n" +
	"function mkp(n: i32): Pair { return Pair { a: mkw(n), b: mkw(n) }; }\n" +
	"function (w: W) bump(k: i32): W { return W { d: w.d, n: w.n + k }; }\n" +
	"function main(): i32 { return 0; }\n"

func TestSelfHostCheckerDifferentialX86_64(t *testing.T) {
	checkerBin, runner, dir := buildCheckerCodesBin(t)

	progs := []struct{ name, src string }{
		{"result-conversion-ok-narrow", `function f(n: i64): Result[i32, string] { return Ok(n); }`},
		{"result-conversion-err-literal-overflow", `function f(): Result[string, u8] { return Err(300); }`},
		{"result-conversion-qualified-literal-overflow", `function f(): Result[u8, string] { return Result.Ok(300); }`},
		{"result-conversion-nested-literal-overflow", `function f(): Result[Result[u8, string], string] { return Ok(Ok(300)); }`},
		{"result-conversion-option-literal-overflow", `function f(): Option[u8] { return Some(300); }`},
		{"result-conversion-qualified-option-narrow", `function f(n: i64): Option[i32] { return Option.Some(n); }`},
		{"result-conversion-same-name-method", `struct Factory {} impl Factory { function Ok(self: Self, n: i32): Result[u8, string] { return Result.Ok(1u8); } } function f(): Result[u8, string] { let x = Factory {}; return x.Ok(300); }`},
		{"result-conversion-same-name-function", `function Ok(n: i32): Result[u8, string] { return Err("unused"); } function f(): Result[u8, string] { return Ok(300); }`},
		{"result-conversion-unsigned-small-widen", `function f(n: u8): Result[u32, string] { return Ok(n); }`},
		{"result-conversion-unsigned-small-narrow", `function f(n: u32): Result[string, u8] { return Err(n); }`},
		{"result-conversion-signedness-wide", `function f(n: u32): Result[i64, string] { return Ok(n); }`},
		{"result-conversion-pointer-width", `function f(n: usize): Result[u64, string] { return Ok(n); }`},
		{"result-conversion-float-narrow", `function f(n: f64): Result[f32, string] { return Ok(n); }`},
		{"result-conversion-option-widen", `function f(n: i32): Option[i64] { return Some(n); }`},
		{"result-conversion-binding-narrow", `function f(n: i64): void { let r: Result[i32, string] = Ok(n); }`},
		{"result-conversion-argument-narrow", `function take(r: Result[i32, string]): void {} function f(n: i64): void { take(Ok(n)); }`},
		{"result-conversion-err-narrow", `function f(n: i64): Result[string, i32] { return Err(n); }`},
		{"result-conversion-unsigned-narrow", `function f(n: u64): Result[u8, string] { return Result.Ok(n); }`},
		{"result-conversion-sign-change", `function f(n: i32): Result[u32, string] { return Ok(n); }`},
		{"result-conversion-ok-widen", `function f(n: i32): Result[i64, string] { return Ok(n); }`},
		{"result-conversion-err-widen", `function f(n: u8): Result[string, u64] { return Result.Err(n); }`},
		{"result-conversion-literal", `function f(): Result[u8, string] { return Ok(3); }`},
		{"result-conversion-literal-overflow", `function f(): Result[u8, string] { return Ok(300); }`},
		{"result-conversion-option-narrow", `function f(n: i64): Option[i32] { return Some(n); }`},
		{"result-conversion-existing-result", `function f(n: Result[i64, string]): Result[i32, string] { return n; }`},
		{"qualified-constructor-shadowing", qualifiedConstructorShadowingSource},
		{"qualified-constructor-function-value-mismatch", `enum A { Value(i32) } enum B { Value(string) } function Value(n: i32): B { return B.Value("value"); } function f(): B { let call = Value; return call("wrong"); }`},
		{"partial-result-foreign-constructor-widening", `enum Other[T] { Ok(T), No } function f(n: i32): Result[i64, string] { return Other.Ok(n); } function main(): i32 { return 0; }`},
		{"partial-result-foreign-constructor-literal", `enum Other[T] { Ok(T), No } function f(): Result[i64, string] { return Other.Ok(3); } function main(): i32 { return 0; }`},
		{"partial-result-method-widening", `struct Factory {} impl Factory { function Ok(self: Self, n: i32): Result[i32, string] { return Result.Ok(n); } } function f(n: i32): Result[i64, string] { let x = Factory {}; return x.Ok(n); } function main(): i32 { return 0; }`},
		{"partial-result-array-void-exit", `function f(c: boolean): i32 { let g = () => { if(c) { return [Ok(1)]; } return; }; return 0; }`},
		{"partial-result-tuple-void-exit", `function f(c: boolean): i32 { let g = () => { if(c) { return (Ok(1), 2); } return; }; return 0; }`},
		{"partial-result-nested-void-exit", `function f(c: boolean): i32 { let g = () => { if(c) { return [(Ok(1), 2)]; } return; }; return 0; }`},
		{"partial-result-struct-void-exit", `struct Box[T] { v: T } function f(c: boolean): i32 { let g = () => { if(c) { return Box { v: Ok(1) }; } return; }; return 0; }`},
		// A method's parameter or result spelling the receiver's type
		// parameter is bound by the receiver's instantiation, on an enum
		// receiver as on a struct one (#10014).
		{"method-enum-recv-bound-result-mismatch", "enum Box[T] { Full(T), Empty }\nfunction (b: Box[T]) get_or(d: T): T { match (b) { Full(x) => { return x; }, Empty => { return d; } } }\nfunction main(): i32 { return 0; }\nfunction f(): i32 { let o: Box[string] = Full(\"vw\"); let n: boolean = o.get_or(\"\"); return 0; }\n"},
		{"method-enum-recv-bound-result-len", "enum Box[T] { Full(T), Empty }\nfunction (b: Box[T]) get_or(d: T): T { match (b) { Full(x) => { return x; }, Empty => { return d; } } }\nfunction main(): i32 { return 0; }\nfunction f(): i32 { let o: Box[string] = Full(\"vw\"); return o.get_or(\"\").len(); }\n"},
		{"method-struct-recv-bound-arg-mismatch", "struct Hold[T] { v: T }\nfunction (h: Hold[T]) or_else(d: T): T { if (h.v == d) { return d; } return h.v; }\nfunction main(): i32 { return 0; }\nfunction f(): i32 { let h: Hold[i32] = Hold { v: 3 }; return h.or_else(\"x\"); }\n"},
		{"method-struct-recv-bound-result-len", "struct Hold[T] { v: T }\nfunction (h: Hold[T]) or_else(d: T): T { if (h.v == d) { return d; } return h.v; }\nfunction main(): i32 { return 0; }\nfunction f(): i32 { let h: Hold[string] = Hold { v: \"ab\" }; return h.or_else(\"x\").len(); }\n"},
		// A parameter typed by a trait is an anonymous generic; a trait
		// anywhere else is still no type.
		{"trait-param-generic", "trait Shape { function area(self: Self): i32; }\nstruct Sq { s: i32 }\nimpl Shape for Sq { function area(self: Sq): i32 { return self.s; } }\nfunction total(a: Shape, b: Shape): i32 { return a.area() + b.area(); }\nfunction main(): i32 { return total(Sq { s: 1 }, Sq { s: 2 }); }\n"},
		{"trait-param-missing-impl", "trait Shape { function area(self: Self): i32; }\nfunction total(a: Shape): i32 { return a.area(); }\nfunction main(): i32 { return total(5); }\n"},
		{"trait-param-with-type-arguments", "trait Sink[T] { function put(self: Self, v: T): i32; }\nstruct Acc { n: i32 }\nimpl Sink[i32] for Acc { function put(self: Acc, v: i32): i32 { return self.n + v; } }\nfunction feed(s: Sink[i32]): i32 { return s.put(4); }\nfunction main(): i32 { return feed(Acc { n: 3 }); }\n"},
		{"trait-param-array-is-not-the-trait", "trait Shape { function area(self: Self): i32; }\nstruct Sq { s: i32 }\nimpl Shape for Sq { function area(self: Sq): i32 { return self.s; } }\nfunction total(xs: Shape[]): i32 { return 7; }\nfunction main(): i32 { return 0; }\n"},
		{"trait-param-beside-undeclared-type", "trait Shape { function area(self: Self): i32; }\nstruct Sq { s: i32 }\nimpl Shape for Sq { function area(self: Sq): i32 { return self.s; } }\nfunction total(a: Shape, b: Wibble): i32 { return a.area(); }\nfunction main(): i32 { return 0; }\n"},
		{"generic-param-beside-undeclared-type", "function first[T](a: T, b: Wibble): T { return a; }\nfunction main(): i32 { return 0; }\n"},
		{"trait-as-return-type", "trait Shape { function area(self: Self): i32; }\nstruct Sq { s: i32 }\nimpl Shape for Sq { function area(self: Sq): i32 { return self.s; } }\nfunction make(): Shape { return Sq { s: 1 }; }\nfunction main(): i32 { return 0; }\n"},

		// A generic named as a value takes its type arguments from the
		// function type it is wanted at; a position with none is E040.
		{"fn-value-argument", "trait Shape { function area(self: Self): i32; }\nstruct Sq { s: i32 }\nimpl Shape for Sq { function area(self: Sq): i32 { return self.s; } }\nfunction measure[T: Shape](s: T): i32 { return s.area(); }\nfunction apply(f: (Sq) => i32, v: Sq): i32 { return f(v); }\nfunction main(): i32 { return apply(measure, Sq { s: 3 }); }\n"},
		{"fn-value-let-and-generic-callee", "trait Shape { function area(self: Self): i32; }\nstruct Sq { s: i32 }\nimpl Shape for Sq { function area(self: Sq): i32 { return self.s; } }\nfunction measure[T: Shape](s: T): i32 { return s.area(); }\nfunction twice[A](f: (A) => i32, v: A): i32 { return f(v) * 2; }\nfunction main(): i32 { let g: (Sq) => i32 = measure; return g(Sq { s: 1 }) + twice(measure, Sq { s: 1 }); }\n"},
		{"fn-value-bound-unmet", "trait Shape { function area(self: Self): i32; }\nfunction measure[T: Shape](s: T): i32 { return s.area(); }\nfunction apply_i(f: (i32) => i32, v: i32): i32 { return f(v); }\nfunction main(): i32 { return apply_i(measure, 3); }\n"},
		{"fn-value-unannotated-let", "function ident[T](x: T): T { return x; }\nfunction main(): i32 { let f = ident; return 0; }\n"},
		{"loop-string-byte-binding", `function f(text: string): i32 { let out: u8[] = []; for ch in text { out = out.append(ch); } return out.len(); }`},
		{"loop-string-byte-mismatch", `function f(text: string): i32 { for ch in text { let wrong: string = ch; } return 0; }`},
		{"loop-str-byte-binding", `function f(text: str): i32 { let out: u8[] = []; for ch in text { out = out.append(ch); } return out.len(); }`},
		{"arr-view-into-array-var", `function f(all: string[]): i32 { let mid: string[] = all[1:3]; return mid.len(); }`},
		{"arr-view-assign", `function f(x: i32[]): i32 { let y: i32[] = [3]; y = x[0:1]; return y.len(); }`},
		{"arr-view-return", `function f(a: i32[]): i32[] { return a[0:1]; }`},
		{"arr-view-field", "struct Q { xs: i32[] }\nfunction f(a: i32[]): Q { return Q { xs: a[0:1] }; }"},
		{"arr-ret-fn-into-array", "function first(a: [i32]): [i32] { return a[0:1]; }\nfunction f(x: i32[]): i32 { let o: i32[] = first(x); return o.len(); }"},
		{"arr-owned-into-view-var", `function f(a: i32[]): i32 { let w: [i32] = a; return w.len(); }`},
		{"arr-owned-return-from-view-fn", `function f(a: i32[]): [i32] { return a; }`},
		{"arr-view-param-into-array-param", "function h(x: i32[]): i32 { return x.len(); }\nfunction k(v: [i32]): i32 { return h(v); }"},
		{"as-bytes-into-array-var", `function f(s: string): i32 { let b: u8[] = s.as_bytes(); return b.len(); }`},
		{"arr-view-param-uses", "function n(v: [u8]): i32 { return v.len(); }\nfunction k(v: [u8]): i32 { let w: [u8] = v[0:1]; return n(v) + n(w); }\nfunction f(s: string): i32 { let b: [u8] = s.as_bytes(); let o: u8[] = [1, 2]; return k(b) + k(o); }"},
		{"arr-view-uses", "function sum(s: [i32]): i32 { let t: i32 = 0; for v in s { t = t + v; } return t; }\nfunction first(a: [i32]): [i32] { return a[0:1]; }\nfunction f(a: i32[]): i32 { let s: [i32] = a[1:4]; let s2: [i32] = s[0:2]; let u = a[0:2]; let g: [i32] = first(a); return sum(a[0:5]) + s.len() + s2[1] + u.len() + g[0] + sum(s); }"},
		{"loop-generic-callback-result", `function f[T, U](xs: T[], callback: (T) => U[]): U[] { let out: U[] = []; for x in xs { for y in callback(x) { out = out.append(y); } } return out; }`},
		{"loop-generic-callback-local", `function f[T, U](xs: T[], callback: (T) => U[]): U[] { let out: U[] = []; for x in xs { let ys = callback(x); for y in ys { out = out.append(y); } } return out; }`},
		{"loop-callback-argument-mismatch", `function f(callback: (string) => i32[]): i32 { for y in callback(1) { let n = y; } return 0; }`},
		{"loop-map-pair-types", `function f(m: Map[string, i64]): i64 { for (k, v) in m { return k.len() + v; } return 0; }`},
		// Map's helpers all come from core/map, so a program that never
		// imports it does not link — native reports E001 up front and the
		// self-host used to build it (#10094). The imported row is the
		// control: the rule must read the program's import closure, not
		// just flag every map_new it walks.
		{"map-without-core-map-import", "function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 2); return m.get_or(1, 0) - 2; }\n"},
		// A recursive local is a nested `function`; an arrow lambda bound by
		// `let` does not see its own name, so a self-call is E001 and a
		// same-named OUTER binding is what it reads (#10383).
		{"rec-local-fn-ok", "function demo(n: i32): i32 { function f(k: i32): i32 { if (k <= 0) { return n; } return f(k - 1); } return f(3); }\nfunction main(): i32 { return demo(2); }\n"},
		{"rec-local-arrow", "function demo(n: i32): i32 { let f = (): i32 => { if (n <= 0) { return 0; } return f(); }; return f(); }\nfunction main(): i32 { return demo(2); }\n"},
		{"rec-local-arrow-nested", "function demo(n: i32): i32 { let f = (): i32 => { let g2 = (): i32 => { return f(); }; return g2(); }; return f(); }\nfunction main(): i32 { return demo(2); }\n"},
		{"rec-local-arrow-reads-outer", "function main(): i32 { let f = (x: i32): i32 => { return x + 1; }; if (true) { let f = (x: i32): i32 => { return f(x) * 2; }; return f(3); } return 0; }\n"},
		{"map-with-core-map-import", "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 2); return m.get_or(1, 0) - 2; }\n"},
		// A generic method on a builtin union types its result from the
		// receiver's arguments, the method's own from its arguments (an `Ok`
		// payload binds `U`), and a nested receiver structurally (#10014).
		{"union-method-receiver-ret-mismatch", "function (r: Result[T, E]) unwrap_or(fallback: T): T { match (r) { Ok(x) => { return x; }, Err(e) => { return fallback; } } }\nfunction (r: Result[T, E]) and[U](other: Result[U, E]): Result[U, E] { match (r) { Ok(x) => { return other; }, Err(e) => { return Err(e); } } }\nfunction (r: Result[Result[T, E], E]) flatten(): Result[T, E] { match (r) { Ok(inner) => { return inner; }, Err(e) => { return Err(e); } } }\nfunction main(): i32 { let s: Result[string, string] = Ok(\"a\"); let z: boolean = s.unwrap_or(\"\"); return 0; }\n"},
		{"union-method-own-param-ret-mismatch", "function (r: Result[T, E]) unwrap_or(fallback: T): T { match (r) { Ok(x) => { return x; }, Err(e) => { return fallback; } } }\nfunction (r: Result[T, E]) and[U](other: Result[U, E]): Result[U, E] { match (r) { Ok(x) => { return other; }, Err(e) => { return Err(e); } } }\nfunction (r: Result[Result[T, E], E]) flatten(): Result[T, E] { match (r) { Ok(inner) => { return inner; }, Err(e) => { return Err(e); } } }\nfunction main(): i32 { let r: Result[i32, string] = Ok(1); let q: Result[i32, string] = r.and(Ok(\"vw\")); return 0; }\n"},
		{"union-method-own-param-ret-clean", "function (r: Result[T, E]) unwrap_or(fallback: T): T { match (r) { Ok(x) => { return x; }, Err(e) => { return fallback; } } }\nfunction (r: Result[T, E]) and[U](other: Result[U, E]): Result[U, E] { match (r) { Ok(x) => { return other; }, Err(e) => { return Err(e); } } }\nfunction (r: Result[Result[T, E], E]) flatten(): Result[T, E] { match (r) { Ok(inner) => { return inner; }, Err(e) => { return Err(e); } } }\nfunction main(): i32 { let r: Result[i32, string] = Ok(1); let s: Result[string, string] = r.and(Ok(\"vw\")); return s.unwrap_or(\"\").len(); }\n"},
		{"union-method-nested-receiver-ret-mismatch", "function (r: Result[T, E]) unwrap_or(fallback: T): T { match (r) { Ok(x) => { return x; }, Err(e) => { return fallback; } } }\nfunction (r: Result[T, E]) and[U](other: Result[U, E]): Result[U, E] { match (r) { Ok(x) => { return other; }, Err(e) => { return Err(e); } } }\nfunction (r: Result[Result[T, E], E]) flatten(): Result[T, E] { match (r) { Ok(inner) => { return inner; }, Err(e) => { return Err(e); } } }\nfunction main(): i32 { let i: Result[string, string] = Ok(\"p\"); let r: Result[Result[string, string], string] = Ok(i); let z: Result[i32, string] = r.flatten(); return 0; }\n"},
		{"option-method-own-param-ret-mismatch", "function (o: Option[T]) and[U](other: Option[U]): Option[U] { match (o) { Some(x) => { return other; }, None => { return None; } } }\nfunction main(): i32 { let o: Option[i32] = Some(1); let b: Option[i32] = o.and(Some(\"w\")); return 0; }\n"},
		// The literal spelling reaches the same rule by a different road: the
		// compile parse desugars `Map { … }` to a __map_new_i32 / map_new
		// chain, so the constructor the walk sees is not the one written.
		{"map-literal-without-core-map-import", "function main(): i32 { let m: Map[i32, i32] = Map { 1: 2 }; return m.get_or(1, 0) - 2; }\n"},
		{"map-literal-with-core-map-import", "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, i32] = Map { 1: 2 }; return m.get_or(1, 0) - 2; }\n"},
		// #10095: the pure-collection API removed the in-place Map spellings,
		// and native reports E043 naming the value-returning replacement. The
		// self-host had no Map arm at all, so ANY name on a Map receiver was
		// accepted — and the two lowerings then answered differently for it.
		{"map-set-retired", "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, i32] = map_new(8); m.set(1, 2); return m.get_or(1, 0); }\n"},
		{"map-delete-retired", "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, i32] = map_new(8); m.delete(1); return 0; }\n"},
		{"map-clear-retired", "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, i32] = map_new(8); m.clear(); return 0; }\n"},
		{"map-unknown-method", "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, i32] = map_new(8); return m.nope(); }\n"},
		// The controls: the value-returning spellings and the other builtins
		// stay clean, so the rule discriminates rather than refusing Maps.
		{"map-value-returning-ok", "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 2); m = m.without(1); m = m.cleared(); return m.len() + m.get_or(1, 0); }\n"},
		// A settled i32 beside a settled i64 widens to i64 in either operand
		// order (native's commonIntegerWidth), so the sum returns as i64 and
		// is refused as i32.
		// A builtin typed from builtin_sigs checks its arguments, not just
		// their count, for a free call and for a Reader method alike.
		{"builtin-arg-type-mismatch", `function main(): i32 { let r = chdir(42); return 0; }`},
		{"builtin-arg-literal-mismatch", `function main(): i32 { let b: usize = buf_new("x"); return 0; }`},
		{"builtin-method-arg-type-mismatch", `function main(): i32 { let r = stdin().read_chunk("x"); return 0; }`},
		{"builtin-args-ok", `function main(): i32 { let r = chdir("/"); let b: usize = buf_new(4); let c = cell_new(3); let rr = stdin().read_chunk(16); return 0; }`},
		{"mixed-width-narrow-left-ok", `function f(a: i32, v: i64): i64 { return a + v; }`},
		{"mixed-width-narrow-right-ok", `function f(a: i32, v: i64): i64 { return v + a; }`},
		{"mixed-width-narrow-left-mismatch", `function f(a: i32, v: i64): i32 { return a + v; }`},
		{"mixed-width-narrow-right-mismatch", `function f(a: i32, v: i64): i32 { return v + a; }`},
		{"mixed-width-call-left-ok", `function g(): i32 { return 1; } function f(v: i64): i64 { return g() + v; }`},
		{"mixed-width-call-right-ok", `function g(): i32 { return 1; } function f(v: i64): i64 { return v + g(); }`},
		{"loop-map-return-mismatch", `function f(m: Map[string, i64]): i64 { for (k, v) in m { return k; } return 0; }`},
		{"loop-map-argument-mismatch", `function take(s: string): i32 { return s.len(); } function f(m: Map[string, i64]): i32 { for (k, v) in m { return take(v); } return 0; }`},
		{"loop-map-assignment-mismatch", `function f(m: Map[string, i64]): i32 { for (k, v) in m { v = k; } return 0; }`},
		{"loop-tuple-element-types", `function f(xs: (i64, string)[]): i64 { for (n, text) in xs { return n + text.len(); } return 0; }`},
		{"loop-nested-tuple-types", `function f(xs: ((i64, string), boolean)[]): i64 { for ((n, text), flag) in xs { if (flag) { return n + text.len(); } } return 0; }`},
		{"loop-discard-is-not-binding", `function f(xs: (i32, i32)[]): i32 { for (_, n) in xs { return _; } return 0; }`},
		{"loop-range-shadow", `function f(i: string): i32 { for i in 0..4 { let n: i32 = i; } return i.len(); }`},
		{"loop-map-shadow", `function f(m: Map[string, i64], k: i32): i32 { for (k, v) in m { let text: string = k; } return k; }`},
		{"loop-array-shadow", `function f(xs: string[], x: i64): i64 { for x in xs { let text: string = x; } return x; }`},
		// An unsuffixed float literal takes the element type the other elements
		// settle on, whichever element comes first (#10122).
		{"array-float-literals-beside-f32", `function g(): f32 { return 1.0 as f32; } function f(): f32 { let a: f32[] = [2.0, g(), -1.5 * 2.0]; return a[0]; }`},
		{"array-float-literal-anchor-mismatch", `function g(): f32 { return 1.0 as f32; } function f(): i32 { let a = [2.0, g(), "x"]; return 0; }`},
		// A struct that shares a built-in variant's name is not that
		// variant: native has no struct-to-enum assignability.
		{"struct-named-like-a-builtin-variant", `struct Some { n: i32 } function f(): i32 { let o: Option[i32] = Some { n: 1 }; return 0; }`},
		// A pipe hole as a named argument's value (#10121).
		{"pipe-hole-named-arg", `function diff(a: i32 = 0, b: i32 = 0): i32 { return a - b; } function f(): i32 { return 9 |> diff(b = _); }`},
		// Annotated tuple shapes — var binding, parameter, return, nested, and
		// a tuple whose element is a (builtin) enum/union. All well-typed: the
		// self-host must not invent a diagnostic the Go checker doesn't report.
		{"tuple-return", "function pair(): (i32, string) { return (1, \"a\"); }\nfunction main(): i32 { return 0; }\n"},
		// Integer-width arithmetic the mixed-signedness rule must NOT flag.
		// The rule rejects operands that differ in SIGNEDNESS; everything
		// else about integer arithmetic has to keep working, and these are
		// the shapes the stdlib and the compiler's own sources lean on. The
		// Go checker is the oracle, so a self-host false positive on any of
		// them fails here rather than surfacing as a rejected stdlib build.
		{"int-widen-i64-i32", "function main(): i32 { let q: i64 = 5; let n: i32 = 1; return (q + n) as i32; }\n"},
		{"int-widen-u8-u32", "function main(): i32 { let s: string = \"ab\"; let b: u8 = s[0]; let u: u32 = 2; return (b + u) as i32; }\n"},
		{"int-unsigned-literal", "function main(): i32 { let s: string = \"ab\"; let b: u8 = s[0]; return (b * 2 + 1) as i32; }\n"},
		{"int-unsigned-shift-literal", "function main(): i32 { let u: u64 = 7; return ((u << 3) + 1) as i32; }\n"},
		{"int-unsigned-compare-literal", "function main(): i32 { let s: string = \"ab\"; let b: u8 = s[0]; if (b < 128) { return 1; } return 0; }\n"},
		{"int-byte-digit-arith", "function main(): i32 { let s: string = \"7\"; let d: u8 = s[0] - b'0'; return d as i32; }\n"},
		{"int-usize-mixed", "function main(): i32 { let p: usize = 16; let n: i32 = 4; return (p + n) as i32; }\n"},
		// The compiler-internal intrinsics core/map's bodies call by name. Each
		// was already LOWERED by the lowering — the comment at its lowering says the
		// point is that core/map compiles and links — but none was registered in
		// the self-host checker's intrinsic table, so every body calling one
		// answered the "could not infer an expression's type" bail while
		// the Go checker typed it fine. `map_new_impl` was the first casualty and
		// `__map_own_str_slot` the next, which is why core/map did not type at
		// all on the self-host and its Map stayed the linear-scan runtime.
		{"intrinsic-map-hash-seed", "function main(): i32 { return __map_hash_seed(); }\n"},
		{"intrinsic-rc-inc", "function main(): i32 { let p: usize = __alloc(16); let q: usize = __fern_rc_inc(p); return 0; }\n"},
		{"intrinsic-str-dec", "function main(): i32 { let s: string = \"ab\"; let q: usize = __fern_str_dec(s); return 0; }\n"},
		{"intrinsic-arr-dec", "function main(): i32 { let p: usize = __alloc(16); let q: usize = __fern_arr_dec(p, 8); return 0; }\n"},
		{"intrinsic-drop-arr-ptr", "function main(): i32 { let p: usize = __alloc(16); let q: usize = __fern_drop_arr_ptr(p, 8); return 0; }\n"},
		{"tuple-var-annot", "function main(): i32 { let t: (i32, string) = (1, \"a\"); return t.0; }\n"},
		{"tuple-array-annot", "function main(): i32 { let out: (i32, string)[] = []; return 0; }\n"},
		{"tuple-nested", "function main(): i32 { let t: (i32, (string, i32)) = (1, (\"a\", 2)); return t.0; }\n"},
		{"tuple-union-elem", "enum E { A, B }\nfunction f(): (E, i32) { return (A, 1); }\nfunction main(): i32 { return 0; }\n"},
		{"tuple-param", "function f(p: (i32, string)): i32 { return p.0; }\nfunction main(): i32 { return f((1, \"a\")); }\n"},
		{"tuple-struct-elem", "struct P { x: i32 }\nfunction f(): (P, i32) { return (P { x: 1 }, 2); }\nfunction main(): i32 { return 0; }\n"},
		// Composite MAP KEYS (#7001). A struct or enum key deriving Eq + Hash is
		// accepted by the Go checker; the self-host rejected every non-i32,
		// non-string key outright, so these rows were `[E045]` vs `[]`. The
		// underived pair is the other half of the rule — both must still be
		// rejected, so the relaxation cannot become a blanket accept.
		{"map-lit-derived-struct-key", "import \"core/cmp\";\nimport \"core/map\";\n@derive(cmp.Eq, cmp.Hash)\nstruct K { a: i32, b: string }\nfunction main(): i32 { let m: Map[K, i32] = Map { K { a: 1, b: \"x\" }: 10 }; return m.get_or(K { a: 1, b: \"x\" }, 0); }\n"},
		{"map-lit-derived-enum-key", "import \"core/cmp\";\nimport \"core/map\";\n@derive(cmp.Eq, cmp.Hash)\nenum T { Red, Green }\nfunction main(): i32 { let m: Map[T, i32] = Map { Red: 1, Green: 2 }; return m.get_or(Green, 0); }\n"},
		{"map-lit-underived-struct-key", "import \"core/map\";\nstruct B { a: i32 }\nfunction main(): i32 { let m: Map[B, i32] = Map { B { a: 1 }: 10 }; return m.len(); }\n"},
		{"map-lit-underived-enum-key", "import \"core/map\";\nenum U { X, Y }\nfunction main(): i32 { let m: Map[U, i32] = Map { X: 1 }; return m.len(); }\n"},
		{"tuple-reassign", "function main(): i32 { let t: (i32, string) = (1, \"a\"); t = (2, \"b\"); return t.0; }\n"},
		// Generic tuple container (the std/array `zip` shape).
		{"tuple-generic-array", "function zip(a: i32[], b: string[]): (i32, string)[] { let out: (i32, string)[] = []; return out; }\nfunction main(): i32 { return 0; }\n"},
		// Lambda bodies see the LAMBDA's declared return type (not the
		// enclosing function's) — pins the call_diags lambda-scope ret_type
		// threading (#4363 item 1) on valid array-returning shapes.
		{"lambda-arr-ret-ok", "function f(): i32[] {\n    let g = (): string[] => { return [\"a\", \"b\"]; };\n    return [1, 2];\n}\nfunction main(): i32 { return 0; }\n"},
		{"lambda-try-ret-option-ok", "function f(): i32 {\n    let g = (): Option[i32] => { let o: Option[i32] = Some(1); let v: i32 = o?; return Some(v); };\n    return 2;\n}\nfunction main(): i32 { return 0; }\n"},
		// Method chains on string / array builtins (valid).
		{"method-chain-len", "function main(): i32 { let s = \"abc\"; let n = s.len(); return n; }\n"},
		{"method-chain-array", "function main(): i32 { let a = [1, 2, 3]; return a.len(); }\n"},
		// if/match-expression values flowing into typed positions (post-#3137).
		{"if-expr-typed-ok", "function main(): i32 { let x: i32 = if (1 < 2) { 1 } else { 2 }; return x; }\n"},
		{"match-expr-typed-ok", "enum E { A, B }\nfunction main(): i32 { let e: E = A; let x: i32 = match (e) { A => 1, B => 2 }; return x; }\n"},
		// Builtin array-method calls with element-typed args (valid): the
		// self-host uses .append / .with pervasively, so an arg-type rule over
		// them must not false-positive on a correctly-typed call.
		{"array-append-prim-ok", "function main(): i32 { let a: i32[] = [1]; a = a.append(2); return a.len(); }\n"},
		{"array-append-struct-ok", "struct P { x: i32 }\nfunction main(): i32 { let a: P[] = [P { x: 1 }]; a = a.append(P { x: 2 }); return 0; }\n"},
		{"array-append-union-ok", "struct P { x: i32 }\nstruct Q { y: i32 }\ntype U = P | Q;\nfunction main(): i32 { let a: U[] = [P { x: 1 }]; a = a.append(Q { y: 2 }); return 0; }\n"},
		{"array-with-ok", "function main(): i32 { let a: i32[] = [1, 2]; a = a.with(0, 9); return a.len(); }\n"},
		{"array-append-tuple-ok", "function main(): i32 { let a: (i32, string)[] = []; a = a.append((1, \"x\")); return 0; }\n"},
		// Literal-match wildcard position (#3612), proven against the Go
		// oracle: a non-last `_` on an i32/string scrutinee desugars to an
		// if/else chain, so the self-host must still surface E026 (and only
		// E026) for any `_` position, while the wildcard-last form stays
		// clean. Differential — no hardcoded codes, native is the oracle.
		{"lit-match-wildcard-first", "function main(): i32 { let x = 1; match (x) { _ => { return 0; }, 1 => { return 1; } } }\n"},
		{"lit-match-wildcard-middle", "function main(): i32 { let x = 1; match (x) { 1 => { return 1; }, _ => { return 9; }, 2 => { return 2; } } }\n"},
		{"str-match-wildcard-middle", "function f(s: string): i32 { match (s) { \"a\" => { return 1; }, _ => { return 0; }, \"b\" => { return 2; } } }\nfunction main(): i32 { return f(\"a\"); }\n"},
		{"lit-match-wildcard-last-ok", "function classify(x: i32): i32 { match (x) { 1 => { return 10; }, 2 => { return 20; }, _ => { return 99; } } }\nfunction main(): i32 { return classify(2); }\n"},
		// Sub-word / pointer-width integer builtins (u8 / usize) — the only two
		// left after i8/u16/i16/isize were retired (#4408): the Go checker
		// accepts them as real types (stdlib byte code uses `let b: u8` and
		// core/int uses `let p: usize` pervasively), so the self-host E064
		// unknown-type rule must not flag them — on a param or a body `let`
		// annotation (the #3813 body-var walk that imported them via the
		// stdlib bundle). `byte` is the negative control: not a keyword, so
		// the Go checker rejects it and both must report E064, proving the
		// allowlist didn't over-broaden.
		{"subword-int-params", "function f(a: u8, b: usize): i32 { return 0; }\nfunction main(): i32 { return 0; }\n"},
		{"subword-u8-var", "function main(): i32 { let n: u8 = 0 as u8; return 0; }\n"},
		{"usize-var", "function main(): i32 { let p: usize = 0 as usize; return 0; }\n"},
		{"unknown-byte-param", "function f(x: byte): i32 { return 0; }\nfunction main(): i32 { return 0; }\n"},
		// Multi-binding variant payload patterns (#4345): the self-host checker
		// only bound the FIRST payload name (pv.binding) and rejected the rest as
		// undefined (a false E001), so `Rect(w, h)` reading `h` in an arm body /
		// guard / assignment tripped a diagnostic the Go checker never emits.
		// These exercise every arm-scope binding site: expr-arm body, when-guard
		// scope, assignment-in-arm, and a `_`-first position that binds only the
		// second name. All well-typed → the self-host must stay silent.
		{"match-multi-binding-body", "enum Shape { Circle(i32), Rect(i32, i32) }\nfunction area(s: Shape): i32 { return match (s) { Circle(r) => r, Rect(w, h) => w * h }; }\nfunction main(): i32 { return area(Rect(3, 4)); }\n"},
		{"match-multi-binding-guard", "enum Shape { Circle(i32), Rect(i32, i32) }\nfunction area(s: Shape): i32 { match (s) { Rect(w, h) when h > 0 => { return w * h; }, _ => { return 0; } } }\nfunction main(): i32 { return area(Rect(3, 4)); }\n"},
		{"match-multi-binding-assign", "enum Shape { Circle(i32), Rect(i32, i32) }\nfunction area(s: Shape): i32 { match (s) { Rect(w, h) => { h = h + 1; return w * h; }, Circle(r) => { return r; } } }\nfunction main(): i32 { return area(Rect(3, 4)); }\n"},
		{"match-multi-binding-wild-first", "enum Shape { Circle(i32), Rect(i32, i32) }\nfunction area(s: Shape): i32 { return match (s) { Circle(r) => r, Rect(_, h) => h }; }\nfunction main(): i32 { return area(Rect(3, 4)); }\n"},
		// Guarded match arms (#4344). A guarded arm doesn't fully cover its
		// variant, so:
		//   - guarded-then-unguarded is VALID (no false E028) — the self-host
		//     used to add every variant to `seen` regardless of the guard.
		//   - a guarded-ONLY variant is NON-exhaustive (E030) — the self-host
		//     used to count it as covered (accepts-invalid). Native is the
		//     oracle: the first is clean, the second draws E030.
		{"match-guarded-then-unguarded-ok", "enum Color { Red, Green, Blue }\nfunction f(c: Color): i32 { match (c) { Red when 1 == 1 => { return 1; }, Red => { return 2; }, Green => { return 3; }, Blue => { return 4; } } }\nfunction main(): i32 { return f(Green); }\n"},
		{"match-guarded-only-nonexhaustive", "enum Color { Red, Green, Blue }\nfunction f(c: Color): i32 { match (c) { Red when 1 == 2 => { return 1; }, Green => { return 3; }, Blue => { return 4; } } }\nfunction main(): i32 { return f(Green); }\n"},
		// Nested generic field spellings (#4346 piece 2): reading a field whose
		// declared type nests a type parameter — `items: T[]`, `kv: (K, V)` — off
		// a concrete instantiation substitutes the arg throughout, so
		// `Wrapper[i32].items` is i32[] (element i32) and `Pair[i32, string].kv`
		// is (i32, string). Both well-typed against the Go oracle — the self-host
		// must stay silent where it once typed the nested field unknown and
		// either over- or under-flagged.
		{"generic-nested-array-field-ok", "struct Wrapper[T] { items: T[] }\nfunction main(): i32 { let w: Wrapper[i32] = Wrapper { items: [1, 2, 3] }; return w.items[0]; }\n"},
		{"generic-nested-tuple-field-ok", "struct Pair[K, V] { kv: (K, V) }\nfunction main(): i32 { let p: Pair[i32, string] = Pair { kv: (7, \"hi\") }; return p.kv.0; }\n"},
		// dyn Trait representation (#4346 piece 2): a `dyn Trait` annotation now
		// resolves to TypeDyn, so binding a value into a dyn slot type-checks
		// (assignment into dyn is lenient). Both are well-typed against the Go
		// oracle — a var-binding and a `dyn T[]` array of dyn — where the
		// pre-slice self-host bound them to unknown and silently over-rejected.
		// The E021 object-safety pass keys off the annotation text (Greet is
		// object-safe), so no code fires; the self-host must stay silent.
		{"dyn-bind-ok", "trait Greet { function hi(self: Self): i32; }\nstruct Dog { }\nimpl Greet for Dog { function hi(self: Self): i32 { return 7; } }\nfunction main(): i32 { let d: dyn Greet = Dog { }; return 0; }\n"},
		{"dyn-array-ok", "trait Greet { function hi(self: Self): i32; }\nstruct Dog { }\nimpl Greet for Dog { function hi(self: Self): i32 { return 7; } }\nfunction main(): i32 { let ds: dyn Greet[] = [Dog { }]; return ds.len(); }\n"},
		// Generic-receiver method return substitution (#4346 piece 2): a method
		// returning a receiver type parameter — bare `T` or nested `T[]` —
		// resolves to the instantiation off a concrete receiver. Both well-typed
		// against the Go oracle (`Box[i32].get()` → i32 feeds an i32 return;
		// `Wrapper[i32].all()` → i32[] indexes to i32) where the pre-slice
		// self-host typed the call unknown and silently over-rejected.
		{"generic-method-ret-ok", "struct Box[T] { v: T }\nfunction (b: Box[T]) get(): T { return b.v; }\nfunction main(): i32 { let b: Box[i32] = Box { v: 5 }; return b.get(); }\n"},
		{"generic-method-nested-ret-ok", "struct Wrapper[T] { items: T[] }\nfunction (w: Wrapper[T]) all(): T[] { return w.items; }\nfunction main(): i32 { let w: Wrapper[i32] = Wrapper { items: [1, 2] }; return w.all()[0]; }\n"},

		// `void` shapes (#6649). Before TypeVoid existed, void resolved through
		// the same "unrecognised type name" fallthrough as a misspelling, so a
		// void call was an uncoded over-reject in statement position (invisible
		// to this gate, whose codeRE only matches E\d{3}) and an unknown-typed
		// wildcard in value position. The first three must stay CLEAN and the
		// rest must carry native's exact code.
		{"void-call-stmt", "function nothing(): void { }\nfunction main(): i32 { nothing(); return 0; }\n"},
		{"void-bare-return", "function nothing(): void { return; }\nfunction main(): i32 { return 0; }\n"},
		{"void-method-stmt", "struct S { a: i32 }\nfunction (s: S) touch(): void { }\nfunction main(): i32 { let s = S { a: 1 }; s.touch(); return 0; }\n"},
		{"void-assign-annotated", "function nothing(): void { }\nfunction main(): i32 { let x: i32 = nothing(); return x; }\n"},
		{"void-as-plain-arg", "function nothing(): void { }\nfunction g(x: i32): i32 { return x; }\nfunction main(): i32 { return g(nothing()); }\n"},
		{"void-return-value-from-void-fn", "function nothing(): void { return 1; }\nfunction main(): i32 { return 0; }\n"},
		// E072 itself: a void call in a variant-constructor payload. This is
		// native's ONLY requireValue site — the two rows above prove the same
		// argument draws E038 to a plain function, which is why the self-host
		// check is keyed on the callee being a variant constructor.
		{"e072-void-in-builtin-variant", "function nothing(): void { }\nfunction f(): Option[()] { return Some(nothing()); }\nfunction main(): i32 { return 0; }\n"},
		{"e072-unit-payload-ok", "function f(): Option[()] { return Some(()); }\nfunction main(): i32 { return 0; }\n"},
		{"e072-normal-payload-ok", "enum W { Wrap(i32), Empty }\nfunction f(): W { return Wrap(3); }\nfunction main(): i32 { return 0; }\n"},
		// `Wrap(nothing())` for a USER enum draws BOTH rules: E072 for the void
		// argument and E036 for the payload type it is not (#6650). The two are
		// independent — E072 fires on any variant, E036 only on a declared
		// enum's typed payload — so this row is what proves they compose.
		{"e072-and-e036-void-user-variant", "function nothing(): void { }\nenum W { Wrap(i32), Empty }\nfunction f(): W { return Wrap(nothing()); }\nfunction main(): i32 { return 0; }\n"},
		// Integer WIDTH in assignability (#7011). `type_eq` compared only
		// `is_char`, so every width was interchangeable and the permissive
		// direction went unreported — and no i64/u32/u64 row could be written
		// for anything else, because each one failed on this instead of on
		// what it meant to test. Both directions are here: native rejects
		// narrowing AND widening without a cast, so a rule that only caught
		// one would pass half of these.
		{"width-narrow-i64-to-i32", "function main(): i32 { let c: i64 = 5i64; let x: i32 = c; return 0; }\n"},
		{"width-widen-i32-to-i64", "function f(): i32 { return 1; }\nfunction main(): i32 { let x: i64 = f(); return 0; }\n"},
		{"width-unsigned-u32-to-i32", "function f(): u32 { return 1u32; }\nfunction main(): i32 { let x: i32 = f(); return 0; }\n"},
		{"width-u64-to-i64", "function f(): u64 { return 1u64; }\nfunction main(): i32 { let x: i64 = f(); return 0; }\n"},
		{"width-arg-i32-to-i64-param", "function g(v: i64): i32 { return 0; }\nfunction f(): i32 { return 1; }\nfunction main(): i32 { return g(f()); }\n"},
		{"width-return-i32-from-i64-fn", "function f(): i32 { return 1; }\nfunction g(): i64 { return f(); }\nfunction main(): i32 { return 0; }\n"},
		{"width-equality-i64-vs-i32", "function f(): i32 { return 1; }\nfunction g(): i64 { return 1i64; }\nfunction main(): i32 { if (f() == g()) { return 1; } return 0; }\n"},
		// The other half of the same rule: an UNSUFFIXED literal is read at the
		// destination's width, so tightening assignability must not start
		// rejecting these. Each one is accepted by native.
		{"width-literal-into-i64", "function main(): i32 { let x: i64 = 5; return 0; }\n"},
		{"width-literal-into-u64", "function main(): i32 { let x: u64 = 5; return 0; }\n"},
		{"width-literal-arg-to-i64-param", "function g(v: i64): i32 { return 0; }\nfunction main(): i32 { return g(5); }\n"},
		{"width-literal-return-from-i64-fn", "function g(): i64 { return 5; }\nfunction main(): i32 { return 0; }\n"},
		{"width-literal-equality-with-i64", "function main(): i32 { let c: i64 = 5i64; if (c == 3) { return 1; } return 0; }\n"},
		{"width-i64-arithmetic-stays-i64", "function main(): i32 { let a: i64 = 1i64; let c: i64 = a + a; return 0; }\n"},
		{"width-i64-mixed-arithmetic", "function main(): i32 { let a: i64 = 1i64; let n: i32 = 3; let c: i64 = a - (n as i64); return 0; }\n"},
		{"width-i64-unary-minus", "function main(): i32 { let a: i64 = 1i64; let c: i64 = 0i64 - a; return 0; }\n"},
		// A magnitude past i32-max names i64 with no suffix to say so, and
		// `-2147483648` is still i32 — the boundary the magnitude rule reads
		// on the operand's POSITIVE text.
		{"width-wide-literal-into-i64", "function main(): i32 { let x: i64 = 5000000000; return 0; }\n"},
		{"width-wide-literal-into-u64", "function main(): i32 { let x: u64 = 5000000000; return 0; }\n"},
		{"width-i32-min-literal", "function main(): i32 { let x: i32 = -2147483648; return x; }\n"},
		{"width-i32-min-return", "function smallest(): i32 { return -2147483648; }\nfunction main(): i32 { return 0; }\n"},
		{"width-mixed-literal-array", "function main(): i32 { let a: i64[] = [5000000000, 1]; return a.len(); }\n"},
		// A wide literal beside an operand already COMMITTED to a width is read
		// at that width, and E047 is what reports one it cannot hold (#8722).
		// The self-host typed every wide literal i64 by magnitude and had no
		// rule for this, so it either widened the binary silently (rows 1, 4)
		// or reported the mismatch downstream as E003 (row 2). Native is the
		// oracle for all of them; each destination the rule reaches gets a row,
		// with the accepting neighbour beside it so the rule cannot become a
		// blanket refusal of every wide literal.
		{"wide-lit-beside-i32-operand", "function main(): i32 { let a: i32 = 3; let t = a - 4611686018427387904; return 0; }\n"},
		{"wide-lit-i32-annotated-arith", "function main(): i32 { let t: i32 = 3 - 4611686018427387904; return 0; }\n"},
		{"wide-lit-past-i64-unannotated", "function main(): i32 { let t = 3 - 18446744073709551616; return 0; }\n"},
		{"wide-lit-compare-i32-operand", "function main(): i32 { let a: i32 = 3; if (a != 4611686018427387904) { return 1; } return 0; }\n"},
		{"wide-lit-shift-i32-operand", "function main(): i32 { let a: i32 = 1; let t = a << 4611686018427387904; return 0; }\n"},
		{"wide-lit-beside-u32-operand", "function main(): i32 { let a: u32 = 3; let t = a + 4611686018427387904; return 0; }\n"},
		{"over-lit-beside-u8-operand", "function main(): i32 { let a: u8 = 3; let t = a + 300; return 0; }\n"},
		{"wide-lit-i32-call-arg", "function f(n: i32): i32 { return n; }\nfunction main(): i32 { return f(4611686018427387904); }\n"},
		{"wide-lit-i32-return", "function main(): i32 { return 4611686018427387904; }\n"},
		{"wide-lit-i32-struct-field", "struct P { x: i32 }\nfunction main(): i32 { let p = P { x: 4611686018427387904 }; return p.x; }\n"},
		{"wide-lit-i32-array-append", "function main(): i32 { let xs: i32[] = []; xs = xs.append(4611686018427387904); return 0; }\n"},
		{"wide-lit-i32-array-annot", "function main(): i32 { let xs: i32[] = [4611686018427387904]; return 0; }\n"},
		{"wide-lit-i32-tuple-annot", "function main(): i32 { let t: (i32, i32) = (1, 4611686018427387904); return 0; }\n"},
		{"wide-lit-i32-assignment", "function main(): i32 { let a: i32 = 3; a = a + 4611686018427387904; return a; }\n"},
		{"double-negated-i32-min-lit", "function main(): i32 { let x: i32 = - -2147483648; return x; }\n"},
		// The accepting side of the same rule: a wide literal beside an operand
		// committed to a width that HOLDS it settles there and stays clean —
		// including at u64, where reading the literal as the i64 its magnitude
		// names made it a signedness clash (E009) instead.
		{"wide-lit-beside-i64-operand-ok", "function main(): i32 { let a: i64 = 3; let t = a - 4611686018427387904; return 0; }\n"},
		{"wide-lit-beside-u64-operand-ok", "function main(): i32 { let a: u64 = 1; let t = a - 4611686018427387904; return 0; }\n"},
		{"u64-max-lit-beside-u64-operand-ok", "function main(): i32 { let x: u64 = 3; let t = x - 18446744073709551615; return 0; }\n"},
		{"wide-lit-i64-tuple-annot-ok", "function main(): i32 { let t: (i32, i64) = (1, 4611686018427387904); let u: i64 = t.1; return 0; }\n"},
		{"wide-lit-unannotated-tuple-ok", "function main(): i32 { let t = (1, 4611686018427387904); let u: i64 = t.1; return 0; }\n"},
		{"wide-lit-unannotated-array-ok", "function main(): i32 { let xs = [4611686018427387904]; let u: i64 = xs[0]; return 0; }\n"},
		{"wrapping-i32-sum-of-fitting-lits-ok", "function main(): i32 { let a: i32 = 1; let t = a - (2147483647 + 1); return 0; }\n"},
		// A method on a type-parameter receiver (#7187): refused when no bound
		// provides it, in every receiver spelling native types as the
		// parameter; accepted through a supertrait, a default, a second bound
		// or a fn-typed parameter's result.
		{"tp-method-unbound", "trait Key { function k_id(self: Self): i32; }\nimpl Key for i32 { function k_id(self: Self): i32 { return self; } }\nfunction direct[K: Key](k: K): i32 { return k.no_such_method(); }\nfunction main(): i32 { return direct(3); }\n"},
		{"tp-method-unbound-fn-param-result", "trait Key { function k_id(self: Self): i32; }\nimpl Key for i32 { function k_id(self: Self): i32 { return self; } }\nimpl Key for string { function k_id(self: Self): i32 { return self.len(); } }\nfunction keyed_sum[T, K: Key](xs: T[], key: (T) => K): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < xs.len()) { acc = acc + key(xs[i]).no_such_method(); i = i + 1; } return acc; }\nstruct Row { n: i32, name: string }\nfunction main(): i32 { let rows: Row[] = [Row { n: 7, name: \"abcd\" }]; return keyed_sum(rows, (r: Row): string => r.name); }\n"},
		{"tp-method-unbound-annotated-local", "trait Key { function k_id(self: Self): i32; }\nimpl Key for i32 { function k_id(self: Self): i32 { return self; } }\nfunction direct[K: Key](k: K): i32 { let kv: K = k; return kv.no_such_method(); }\nfunction main(): i32 { return direct(3); }\n"},
		{"tp-method-unbound-inferred-local", "trait Key { function k_id(self: Self): i32; }\nimpl Key for i32 { function k_id(self: Self): i32 { return self; } }\nfunction direct[K: Key](k: K): i32 { let kv = k; return kv.no_such_method(); }\nfunction main(): i32 { return direct(3); }\n"},
		{"tp-method-unbounded-param", "function unbounded[K](k: K): i32 { return k.no_such_method(); }\nfunction main(): i32 { return unbounded(3); }\n"},
		{"tp-method-via-supertrait-and-default-ok", "trait A { function fa(self: Self): i32; }\ntrait B: A { function fb(self: Self): i32; function fd(self: Self): i32 { return 4; } }\nstruct S { v: i32 }\nimpl A for S { function fa(self: Self): i32 { return self.v; } }\nimpl B for S { function fb(self: Self): i32 { return self.v + 1; } }\nfunction viasuper[T: B](x: T): i32 { return x.fa() + x.fb() + x.fd(); }\nfunction main(): i32 { return viasuper(S { v: 1 }); }\n"},
		{"tp-method-via-second-bound-ok", "trait A { function fa(self: Self): i32; }\ntrait C { function fc(self: Self): i32; }\nstruct S { v: i32 }\nimpl A for S { function fa(self: Self): i32 { return self.v; } }\nimpl C for S { function fc(self: Self): i32 { return self.v + 2; } }\nfunction viaplus[T: A + C](x: T): i32 { let y: T = x; let z = x; return y.fc() + z.fa(); }\nfunction main(): i32 { return viaplus(S { v: 1 }); }\n"},
		{"tp-method-on-fn-param-result-ok", "trait C { function fc(self: Self): i32; }\nstruct S { v: i32 }\nimpl C for S { function fc(self: Self): i32 { return self.v + 2; } }\nfunction viafn[T, K: C](xs: T[], key: (T) => K): i32 { return key(xs[0]).fc(); }\nfunction main(): i32 { let ss: S[] = [S { v: 1 }]; return viafn(ss, (q: S): S => q); }\n"},
		{"value-local-struct-ok", "struct P { x: i32 }\nfunction main(): i32 { let t: (i32, i32) = (7, 2); let p: P = match (t) { (a, b) => P { x: a } }; return p.x - 7; }\n"},
		{"value-local-enum-dest-ok", "function main(): i32 { let t: (i32, i32) = (7, 2); let o: Option[i32] = match (t) { (a, b) => Some(a + b) }; match (o) { Some(v) => { return v - 9; }, None => { return 5; } } }\n"},
		{"value-local-array-ok", "function main(): i32 { let t: (i32, i32) = (7, 2); let xs: i32[] = match (t) { (a, b) => [a, b, 1] }; return xs.len() - 3; }\n"},
		{"value-local-tuple-ok", "function main(): i32 { let t: (i32, i32) = (7, 2); let u: (i32, i32) = match (t) { (a, b) => (b, a) }; return u.0 - 2; }\n"},
		{"value-local-in-lambda-ok", "struct P { x: i32 }\nfunction main(): i32 { let f = (k: i32): i32 => { let t: (i32, i32) = (k, 2); let p: P = match (t) { (a, b) => P { x: a } }; let o: Option[i32] = match (t) { (a, b) => Some(a + b) }; match (o) { Some(v) => { return p.x + v - k - 9; }, None => { return 5; } } }; return f(7); }\n"},
		{"tp-method-shadowed-by-local-ok", "trait Key { function k_id(self: Self): i32; }\nimpl Key for i32 { function k_id(self: Self): i32 { return self; } }\nfunction direct[K: Key](k: K): i32 { let k: string = \"ab\"; return k.len(); }\nfunction main(): i32 { return direct(3); }\n"},

		// A value coerced into a dyn slot must implement every trait in the
		// set, at each direct coercion site, and a container element is not
		// one (#10055).
		{"dyn-coerce-nonconforming-var", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction describe(s: dyn Shape): i32 { return s.area(); }\nfunction main(): i32 { let o: dyn Shape = Other { x: 1 }; return 0; }\n"},
		{"dyn-coerce-nonconforming-assign", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction describe(s: dyn Shape): i32 { return s.area(); }\nfunction main(): i32 { let o: dyn Shape = Square { side: 1 }; o = Other { x: 2 }; return 0; }\n"},
		{"dyn-coerce-nonconforming-arg", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction describe(s: dyn Shape): i32 { return s.area(); }\nfunction main(): i32 { return describe(Other { x: 4 }); }\n"},
		{"dyn-coerce-nonconforming-primitive-arg", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction describe(s: dyn Shape): i32 { return s.area(); }\nfunction main(): i32 { return describe(5); }\n"},
		{"dyn-coerce-nonconforming-return", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction describe(s: dyn Shape): i32 { return s.area(); }\nfunction mk(): dyn Shape { return Other { x: 1 }; }\nfunction main(): i32 { return 0; }\n"},
		{"dyn-coerce-nonconforming-field", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction describe(s: dyn Shape): i32 { return s.area(); }\nstruct Holder { s: dyn Shape }\nfunction main(): i32 { let h: Holder = Holder { s: Other { x: 3 } }; return 0; }\n"},
		{"dyn-coerce-inside-a-container", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction describe(s: dyn Shape): i32 { return s.area(); }\nfunction main(): i32 { let arr: Square[] = [Square { side: 1 }]; let ds: dyn Shape[] = arr; return 0; }\n"},
		{"dyn-coerce-conforming-ok", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction describe(s: dyn Shape): i32 { return s.area(); }\nfunction main(): i32 { let d: dyn Shape = Square { side: 2 }; let e: dyn Shape = d; return describe(d) + describe(e) + describe(Square { side: 1 }); }\n"},
		{"dyn-coerce-array-literal-ok", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction describe(s: dyn Shape): i32 { return s.area(); }\nfunction main(): i32 { let ds: dyn Shape[] = [Square { side: 1 }, Square { side: 2 }]; return describe(ds[0]); }\n"},
		// #10085: a TUPLE element is not a coercion site. Native refuses a
		// concrete in a `dyn` tuple slot even when it conforms — boxing is a
		// representation change and a tuple is not where one happens — where
		// the array literal above is a coercion site and stays clean. The
		// self-host accepted both, because `dyn Shape` mashed to `dynShape` in
		// a parenthesised type list and the element resolved to no type at all.
		{"tuple-dyn-element-nonconforming", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction main(): i32 { let t: (dyn Shape, i32) = (Other { x: 1 }, 2); return 0; }\n"},
		{"tuple-dyn-element-conforming", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nfunction main(): i32 { let t: (dyn Shape, i32) = (Square { side: 1 }, 2); return 0; }\n"},
		// The control for the parser half on its own: a dyn tuple element that
		// IS a dyn value stays clean, so keeping the space does not start
		// refusing the shape the tuple genuinely admits.
		{"tuple-dyn-element-from-dyn-ok", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nfunction main(): i32 { let d: dyn Shape = Square { side: 3 }; let t: (dyn Shape, i32) = (d, 2); return t.1; }\n"},
		// The same control for a MULTI-trait element. The checker compares dyn
		// types by exact traits string, so the parenthesised type list has to
		// spell the separator ` + ` the way parse_type_dyn does: collecting
		// `dyn Shape+Named` refuses a value whose type is `dyn Shape + Named`.
		{"tuple-dyn-element-from-dyn-multi-trait-ok", "trait Shape { function area(self: Self): i32; }\ntrait Named { function name(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nimpl Named for Square { function name(self: Self): i32 { return 2; } }\nfunction main(): i32 { let d: dyn Shape + Named = Square { side: 3 }; let t: (dyn Shape + Named, i32) = (d, 2); return t.1; }\n"},
		// #10147: a dyn trait set is a SET. Native sorts and dedupes it at
		// construction (ast.NewDynTraitType), so an order-swapped or repeated
		// spelling is the same type; the self-host compared the traits string
		// exactly, making the spelling the identity and refusing both.
		{"dyn-set-order-swapped-ok", "trait Shape { function area(self: Self): i32; }\ntrait Named { function name(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nimpl Named for Square { function name(self: Self): i32 { return 2; } }\nfunction main(): i32 { let d: dyn Named + Shape = Square { side: 3 }; let e: dyn Shape + Named = d; return e.area() - 3; }\n"},
		{"tuple-dyn-element-order-swapped-ok", "trait Shape { function area(self: Self): i32; }\ntrait Named { function name(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nimpl Named for Square { function name(self: Self): i32 { return 2; } }\nfunction main(): i32 { let d: dyn Named + Shape = Square { side: 3 }; let t: (dyn Shape + Named, i32) = (d, 2); return t.1; }\n"},
		{"dyn-set-duplicate-trait-ok", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nfunction main(): i32 { let d: dyn Shape + Shape = Square { side: 3 }; let e: dyn Shape = d; return e.area() - 3; }\n"},
		{"tuple-dyn-element-duplicate-trait-ok", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nfunction main(): i32 { let d: dyn Shape + Shape = Square { side: 3 }; let t: (dyn Shape, i32) = (d, 2); return t.1; }\n"},
		// The `+` here is INSIDE the associated-type pin, so it joins the pinned
		// type, not the trait set. Splitting on it leaves the set as the two
		// fragments `B]` and `Holder[Item = dyn A`, and canonicalising sorts
		// those into `dyn B] + Holder[Item = dyn A` — neither fragment names a
		// trait, so conformance fails and the self-host refuses what native
		// accepts. This is what pins dyn_trait_set's bracket depth.
		{"dyn-assoc-pin-holds-a-multi-trait-set", "trait A { function f(self: Self): i32; }\ntrait B { function g(self: Self): i32; }\ntrait Holder { type Item; function get(self: Self): i32; }\nstruct S { n: i32 }\nimpl A for S { function f(self: Self): i32 { return 1; } }\nimpl B for S { function g(self: Self): i32 { return 2; } }\nstruct H { n: i32 }\nimpl Holder for H { type Item = dyn A + B; function get(self: Self): i32 { return 7; } }\nfunction main(): i32 { let h: dyn Holder[Item = dyn A + B] = H { n: 0 }; return h.get(); }\n"},
		{"dyn-coerce-enum-and-primitive-ok", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction describe(s: dyn Shape): i32 { return s.area(); }\nenum E { A, B(i32) }\nimpl Shape for E { function area(self: Self): i32 { return 1; } }\nimpl Shape for i32 { function area(self: Self): i32 { return self; } }\nimpl Shape for string { function area(self: Self): i32 { return self.len(); } }\nfunction main(): i32 { let s: string = \"ab\"; return describe(E.A) + describe(E.B(2)) + describe(7) + describe(s); }\n"},
		{"dyn-coerce-two-traits", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction describe(s: dyn Shape): i32 { return s.area(); }\ntrait Named { function name(self: Self): string; }\nimpl Named for Square { function name(self: Self): string { return \"sq\"; } }\nimpl Named for Other { function name(self: Self): string { return \"o\"; } }\nfunction both(x: dyn Shape + Named): i32 { return x.area(); }\nfunction main(): i32 { return both(Square { side: 1 }) + both(Other { x: 1 }); }\n"},
		// A `dyn` argument of an enum takes a concrete payload only where the
		// value is constructed, never from a finished Option[Square].
		{"dyn-slot-option-var-e003", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction main(): i32 { let c: Option[Square] = Some(Square { side: 1 }); let o: Option[dyn Shape] = c; return 0; }\n"},
		{"dyn-slot-option-arg-e038", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction take(o: Option[dyn Shape]): i32 { return 0; }\nfunction main(): i32 { let c: Option[Square] = Some(Square { side: 1 }); return take(c); }\n"},
		{"dyn-slot-option-ctor-arg-ok", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction take(o: Option[dyn Shape]): i32 { return 0; }\nfunction main(): i32 { return take(Some(Square { side: 1 })); }\n"},
		{"dyn-slot-option-ctor-return-ok", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction mk(): Option[dyn Shape] { return Some(Square { side: 1 }); }\nfunction main(): i32 { return 0; }\n"},
		{"dyn-slot-option-ctor-nonconforming", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction main(): i32 { let o: Option[dyn Shape] = Some(Other { x: 1 }); return 0; }\n"},
		{"dyn-slot-result-ctor-ok", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction main(): i32 { let r: Result[i32, dyn Shape] = Err(Square { side: 1 }); return 0; }\n"},
		{"dyn-slot-user-enum-ctor-nonconforming", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nenum MyOpt[T] { Wrap(T), Empty }\nfunction main(): i32 { let o: MyOpt[dyn Shape] = Wrap(Other { x: 1 }); return 0; }\n"},
		{"dyn-slot-qualified-user-ctor-nonconforming", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nenum MyOpt[T] { Wrap(T), Empty }\nfunction main(): i32 { let o: MyOpt[dyn Shape] = MyOpt.Wrap(Other { x: 1 }); return 0; }\n"},
		{"dyn-slot-qualified-some-nonconforming", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nenum MyOpt[T] { Wrap(T), Empty }\nfunction main(): i32 { let o: Option[dyn Shape] = Option.Some(Other { x: 1 }); return 0; }\n"},
		{"dyn-slot-user-and-qualified-ctors-ok", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nenum MyOpt[T] { Wrap(T), Empty }\nfunction main(): i32 { let o: MyOpt[dyn Shape] = Wrap(Square { side: 1 }); let p: Option[dyn Shape] = Option.Some(Square { side: 1 }); let q: MyOpt[dyn Shape] = MyOpt.Wrap(Square { side: 1 }); return 0; }\n"},
		{"dyn-slot-unused-by-the-variant-ok", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nenum MyOpt[T] { Wrap(T), Empty }\nfunction main(): i32 { let o: MyOpt[dyn Shape] = Empty; let r: Result[i32, dyn Shape] = Ok(1); return 0; }\n"},
		// A generic struct literal carries the arguments its fields fix, and
		// is read at its destination's where one names them (#10259): each
		// field is then judged there, as E043.
		{"struct-lit-infers-arguments", "struct Box[T] { v: T }\nfunction main(): i32 { let b = Box { v: 1 }; let z: string = b; return 0; }\n"},
		{"struct-lit-settles-at-destination-ok", "struct Box[T] { v: T }\nstruct Pair[A, B] { a: A, b: B }\nstruct W[T] { items: T[] }\nfunction take(b: Box[i64]): i64 { return b.v; }\nfunction mk(): Box[f64] { return Box { v: 1 }; }\nfunction main(): i32 { let b: Box[i64] = Box { v: 1 }; let p: Pair[i64, string] = Pair { a: 3, b: \"x\" }; let w: W[string] = W { items: [] }; let n: i64 = take(Box { v: 5 }); let bb: Box[Box[i64]] = Box { v: Box { v: 1 } }; return 0; }\n"},
		// The first written field that fixes a type argument binds it, and a
		// later field that contradicts it is E043, as native binds.
		{"struct-lit-fields-disagree-e043", "struct Same[T] { a: T, b: T }\nfunction main(): i32 { let p = Same { a: 1, b: \"x\" }; return 0; }\n"},
		{"struct-lit-literal-binds-first-e043", "struct Same[T] { a: T, b: T }\nfunction main(): i32 { let y: i64 = 5; let q = Same { a: 1, b: y }; let r: i64 = q.a; return 0; }\n"},
		// A destination reaches a literal nested in an array element, a
		// field's array, a return, and a map or array method's stored value,
		// so the literal field settles there as native settles it.
		{"struct-lit-nested-destinations-ok", "import \"core/map\";\nstruct Same[T] { a: T, b: T }\nstruct Holder { xs: Same[i64][] }\nfunction mk(y: i64): Same[i64][] { return [Same { a: 1, b: y }]; }\nfunction main(): i32 { let y: i64 = 5; let xs: Same[i64][] = [Same { a: 1, b: y }]; let h: Holder = Holder { xs: [Same { a: 2, b: y }] }; let m: Map[i32, Same[i64]] = Map {}; m = m.insert(1, Same { a: 3, b: y }); let ys: Same[i64][] = []; ys = ys.append(Same { a: 4, b: y }); let t: i64 = xs[0].a + h.xs[0].a + ys[0].a + mk(y)[0].a; return t as i32; }\n"},
		// An unresolved callee is its own error: native checks none of its
		// arguments, so a literal passed to one draws nothing more.
		{"unresolved-method-args-unchecked", "struct Same[T] { a: T, b: T }\nfunction main(): i32 { let y: i64 = 5; let xs: Same[i64][] = []; xs = xs.frob(Same { a: 1, b: y }); return 0; }\n"},
		{"retired-method-args-unchecked", "struct Same[T] { a: T, b: T }\nfunction main(): i32 { let y: i64 = 5; let xs: Same[i64][] = []; xs = xs.set(Same { a: 1, b: y }); return 0; }\n"},
		{"undefined-function-args-unchecked", "struct Same[T] { a: T, b: T }\nfunction main(): i32 { let y: i64 = 5; return nope(Same { a: 1, b: y }); }\n"},
		{"struct-receiver-unresolved-args-unchecked", "struct Same[T] { a: T, b: T }\nstruct P { x: i32, f: (Same[i64]) => i32 }\nfunction main(): i32 { let y: i64 = 5; let p: P = P { x: 1, f: (s: Same[i64]): i32 => 0 }; let a: i32 = p.nope(Same { a: 1, b: y }); let b: i32 = p.x(Same { a: 1, b: y }); let c: i32 = p.f(Same { a: 1, b: y }); return 0; }\n"},
		// An array-literal field binds through its element type; an empty one
		// binds nothing and settles at what the others fix (#10269).
		{"struct-lit-array-field-infers", "struct W[T] { items: T[] }\nstruct P[T] { a: T[], b: T }\nfunction main(): i32 { let y: i64 = 3; let w = W { items: [y, 1] }; let v = W { items: [1, 2] }; let f = W { items: [1.5] }; let z: string = w; let z2: string = v; let z3: string = f; return 0; }\n"},
		{"struct-lit-empty-array-field-e040", "struct W[T] { items: T[] }\nstruct P[T] { a: T[], b: T }\nfunction main(): i32 { let w = W { items: [] }; return 0; }\n"},
		{"struct-lit-empty-array-field-settles-ok", "struct W[T] { items: T[] }\nstruct P[T] { a: T[], b: T }\nfunction main(): i32 { let p = P { a: [], b: 5 }; let q = P { a: [], b: \"x\" }; let w: W[i64] = W { items: [] }; return p.b + q.a.len(); }\n"},
		{"struct-lit-update-empty-array-field-ok", "struct W[T] { items: T[] }\nfunction main(): i32 { let w: W[i32] = W { items: [1] }; let v = W { ...w, items: [] }; return v.items.len(); }\n"},
		{"struct-lit-array-field-binds-first-e043", "struct W[T] { items: T[] }\nstruct P[T] { a: T[], b: T }\nfunction main(): i32 { let w = P { a: [1], b: 5 as i64 }; return 0; }\n"},
		{"struct-lit-later-literal-settles-ok", "struct Same[T] { a: T, b: T }\nfunction main(): i32 { let y: i64 = 5; let u = Same { a: y, b: 1 }; let r: i64 = u.b; return 0; }\n"},
		{"struct-lit-field-e043-var", "struct Box[T] { v: T }\nfunction main(): i32 { let b: Box[string] = Box { v: 1 }; return 0; }\n"},
		{"struct-lit-field-e043-return", "struct Box[T] { v: T }\nfunction mk(): Box[string] { return Box { v: 1 }; }\nfunction main(): i32 { return 0; }\n"},
		{"struct-lit-field-e043-argument", "struct Box[T] { v: T }\nfunction take(b: Box[string]): i32 { return 0; }\nfunction main(): i32 { return take(Box { v: 3 }); }\n"},
		{"struct-lit-field-e043-nested", "struct Box[T] { v: T }\nfunction main(): i32 { let bb: Box[Box[string]] = Box { v: Box { v: 1 } }; return 0; }\n"},
		{"struct-lit-finished-value-e003", "struct Box[T] { v: T }\nfunction main(): i32 { let q: Box[i32] = Box { v: 2 }; let t: Box[i64] = q; return 0; }\n"},
		{"struct-lit-through-generic-impl-e021", "struct Box[T] { v: T }\ntrait Conv[T] { function conv(self: Self): T; }\nimpl[T] Conv[T] for Box[T] { function conv(self: Self): T { return self.v; } }\nfunction go[T, C: Conv[T]](c: C): T { return c.conv(); }\nfunction main(): i32 { let s: string = go(Box { v: 1 }); let m = go(Box { v: \"x\" }); let q: i32 = m; return 0; }\n"},
		{"dyn-coerce-option-payload-ok", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction describe(s: dyn Shape): i32 { return s.area(); }\nfunction main(): i32 { let o: Option[dyn Shape] = Some(Square { side: 3 }); match (o) { Some(s) => { return s.area() - 3; }, None => { return 1; } } }\n"},
		{"dyn-coerce-dyn-to-wider-set", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\ntrait Named { function name(self: Self): i32; }\nimpl Named for Square { function name(self: Self): i32 { return 2; } }\nfunction main(): i32 { let a: dyn Shape = Square { side: 1 }; let b: dyn Shape + Named = a; return b.area() - 1; }\n"},
		{"dyn-coerce-dyn-to-narrower-set", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\ntrait Named { function name(self: Self): i32; }\nimpl Named for Square { function name(self: Self): i32 { return 2; } }\nfunction main(): i32 { let a: dyn Shape + Named = Square { side: 1 }; let b: dyn Shape = a; return b.area() - 1; }\n"},
		{"dyn-coerce-nonconforming-some", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction main(): i32 { let o: Option[dyn Shape] = Some(Other { x: 1 }); match (o) { Some(s) => { return s.area(); }, None => { return 1; } } }\n"},
		{"dyn-coerce-nonconforming-ok-payload", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction main(): i32 { let r: Result[dyn Shape, string] = Ok(Other { x: 1 }); match (r) { Ok(s) => { return s.area(); }, Err(e) => { return 1; } } }\n"},
		{"dyn-coerce-parametric-impl-ok", "trait Boxed { function b(self: Self): i32; }\nstruct W[T] { v: T }\nimpl[T] Boxed for W[T] { function b(self: Self): i32 { return 1; } }\nfunction main(): i32 { let w: W[i32] = W { v: 1 }; let b: dyn Boxed = w; return b.b() - 1; }\n"},
		{"dyn-coerce-nonconforming-some-return", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction mk(): Option[dyn Shape] { return Some(Other { x: 1 }); }\nfunction main(): i32 { match (mk()) { Some(s) => { return s.area(); }, None => { return 1; } } }\n"},
		{"dyn-coerce-nonconforming-some-field", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nstruct H { o: Option[dyn Shape] }\nfunction main(): i32 { let h: H = H { o: Some(Other { x: 1 }) }; return 0; }\n"},
		{"dyn-coerce-nonconforming-some-assign", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction main(): i32 { let o: Option[dyn Shape] = None; o = Some(Other { x: 1 }); return 0; }\n"},
		{"dyn-coerce-conforming-some-return-ok", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction mk(): Option[dyn Shape] { return Some(Square { side: 1 }); }\nfunction main(): i32 { match (mk()) { Some(s) => { return s.area() - 1; }, None => { return 1; } } }\n"},
		{"dyn-coerce-conforming-some-field-ok", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nstruct H { o: Option[dyn Shape] }\nfunction main(): i32 { let h: H = H { o: Some(Square { side: 1 }) }; return 0; }\n"},
		{"dyn-coerce-conforming-some-assign-ok", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction main(): i32 { let o: Option[dyn Shape] = None; o = Some(Square { side: 1 }); return 0; }\n"},
		{"dyn-coerce-nonconforming-err-arg", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction take(r: Result[i32, dyn Shape]): i32 { return 0; }\nfunction main(): i32 { return take(Err(Other { x: 1 })); }\n"},
		{"dyn-coerce-conforming-err-arg-ok", "trait Shape { function area(self: Self): i32; }\nstruct Square { side: i32 }\nimpl Shape for Square { function area(self: Self): i32 { return self.side; } }\nstruct Other { x: i32 }\nfunction take(r: Result[i32, dyn Shape]): i32 { return 0; }\nfunction main(): i32 { return take(Err(Square { side: 1 })); }\n"},
		{"dyn-array-literal-mixed-var-ok", "trait Show { function show(self: Self): i32; }\nimpl Show for i32 { function show(self: Self): i32 { return self; } }\nimpl Show for string { function show(self: Self): i32 { return self.len(); } }\nfunction main(): i32 { let xs: dyn Show[] = [1, \"ab\"]; return xs.len(); }\n"},
		{"dyn-array-literal-mixed-field-ok", "trait Show { function show(self: Self): i32; }\nimpl Show for i32 { function show(self: Self): i32 { return self; } }\nimpl Show for string { function show(self: Self): i32 { return self.len(); } }\nstruct H { xs: dyn Show[] }\nfunction main(): i32 { let h: H = H { xs: [1, \"ab\"] }; return h.xs.len(); }\n"},
		{"dyn-array-literal-mixed-return-ok", "trait Show { function show(self: Self): i32; }\nimpl Show for i32 { function show(self: Self): i32 { return self; } }\nimpl Show for string { function show(self: Self): i32 { return self.len(); } }\nfunction mk(): dyn Show[] { return [1, \"ab\"]; }\nfunction main(): i32 { return mk().len(); }\n"},
		{"dyn-array-literal-mixed-arg-ok", "trait Show { function show(self: Self): i32; }\nimpl Show for i32 { function show(self: Self): i32 { return self; } }\nimpl Show for string { function show(self: Self): i32 { return self.len(); } }\nfunction n(xs: dyn Show[]): i32 { return xs.len(); }\nfunction main(): i32 { return n([1, \"ab\"]); }\n"},
		{"dyn-array-literal-mixed-assign", "trait Show { function show(self: Self): i32; }\nimpl Show for i32 { function show(self: Self): i32 { return self; } }\nimpl Show for string { function show(self: Self): i32 { return self.len(); } }\nfunction main(): i32 { let xs: dyn Show[] = [1]; xs = [2, \"ab\"]; return xs.len(); }\n"},
		{"array-literal-mixed-undirected", "function main(): i32 { let xs = [1, \"ab\"]; return xs.len(); }\n"},
		// A local at its last use in an `own` position (#9541): native admits by
		// CallArgDeaths and the self-host by its lu_* port, so each shape that
		// analysis reasons about has a row here.
		{"e051-last-use-ret-lit", lastUsePrelude + "function f(): i32 { let a: i32[] = [1]; return keep(a, 1); }\n"},
		{"e051-last-use-ret-binary-lit", lastUsePrelude + "function f(): i32 { let a: i32[] = [1]; return keep(a, 1) + 1; }\n"},
		{"e051-last-use-mid-call", lastUsePrelude + "function f(): i32 { let a: i32[] = mk(1); let r: i32 = keep(a, 1); return r; }\n"},
		{"e051-last-use-mid-lit", lastUsePrelude + "function f(): i32 { let a: i32[] = [1]; let r: i32 = keep(a, 1); return r; }\n"},
		// A literal-built local is as fresh as a call-bound one (#10864).
		{"e051-last-use-mid-lit-grown", lastUsePrelude + "function f(): i32 { let a: i32[] = []; a = a.append(1); let r: i32 = keep(a, 1); return r; }\n"},
		{"e051-last-use-ret-binary-lit-grown", lastUsePrelude + "function f(): i32 { let a: i32[] = []; a = a.append(1); return keep(a, 1) + 0; }\n"},
		{"e051-last-use-mid-struct-lit", lastUsePrelude + "function f(): i32 { let w: W = W { d: [1], n: 2 }; let r: i32 = eat(w); return r; }\n"},
		{"e051-last-use-mid-string-lit", lastUsePrelude + "function takes(own s: string): i32 { return s.len(); }\nfunction f(): i32 { let s: string = \"ab\"; s = s + \"c\"; let r: i32 = takes(s); return r; }\n"},
		{"e051-last-use-spread-lit", lastUsePrelude + "function f(): i32 { let v: W = mkw(1); let w: W = W { ...v, n: 2 }; let r: i32 = eat(w); return r + v.n; }\n"},
		{"e051-last-use-read-again", lastUsePrelude + "function f(): i32 { let a: i32[] = mk(1); let r: i32 = keep(a, 1); return r + a[0]; }\n"},
		{"e051-last-use-loop", lastUsePrelude + "function f(): i32 { let a: i32[] = mk(1); let t: i32 = 0; while (t < 3) { t = t + keep(a, 1); } return t; }\n"},
		{"e051-last-use-loop-nested-reassign", lastUsePrelude + "function f(): i32 { let a: i32[] = mk(1); let t: i32 = 0; while (t < 3) { a = grow(grow(a, t), 1); t = t + 1; } return a.len(); }\n"},
		{"e051-last-use-two-stmt-loop", lastUsePrelude + "function f(): i32 { let a: i32[] = mk(1); let t: i32 = 0; while (t < 2) { let b: i32[] = grow(a, 2); a = b; t = t + 1; } return a.len(); }\n"},
		{"e051-last-use-defer", lastUsePrelude + "function f(): i32 { let a: i32[] = mk(1); defer { let z: i32 = a.len(); } return keep(a, 1); }\n"},
		{"e051-last-use-lambda", lastUsePrelude + "function f(): i32 { let a: i32[] = mk(1); let g = (): i32 => a.len(); return keep(a, 1) + g(); }\n"},
		{"e051-last-use-in-lambda", lastUsePrelude + "function f(): i32 { let g = (): i32 => { let a: i32[] = mk(1); return keep(a, 1); }; return g(); }\n"},
		{"e051-last-use-held", lastUsePrelude + "function two(x: i32, k: i32): i32 { return k; }\nfunction f(): i32 { let a: i32[] = mk(1); let r: i32 = two(a.len(), keep(a, 1)); return r; }\n"},
		{"e051-last-use-struct-path-last", lastUsePrelude + "function f(c: boolean): i32 { let w: W = mkw(1); if (c) { let r: i32 = eat(w); return r; } return w.n; }\n"},
		{"e051-last-use-array-path-last", lastUsePrelude + "function f(c: boolean): i32 { let a: i32[] = mk(1); if (c) { let r: i32 = keep(a, 1); return r; } return a.len(); }\n"},
		{"e051-last-use-path-last-break", lastUsePrelude + "function f(c: boolean): i32 { let w: W = mkw(1); let r: i32 = 0; while (c) { if (c) { r = eat(w); break; } } return r + w.n; }\n"},
		{"e051-last-use-unpack", lastUsePrelude + "function f(): i32 { let p: Pair = mkp(1); let x: W = p.a; let r: i32 = eat(x); return r; }\n"},
		{"e051-last-use-unpack-twice", lastUsePrelude + "function f(): i32 { let p: Pair = mkp(1); let x: W = p.a; let r: i32 = eat(x); return r + p.a.n; }\n"},
		{"e051-last-use-rename-param", lastUsePrelude + "function f(w0: W): i32 { let w: W = w0; let r: i32 = eat(w); return r; }\n"},
		// A destructured binding is fresh when the value it unpacks is (#11120).
		{"e051-last-use-destructure", lastUsePrelude + "function pw(n: i32): (W, i32) { return (mkw(n), n); }\nfunction f(): i32 { let (x, k) = pw(1); let r: i32 = eat(x); return r + k; }\n"},
		{"e051-last-use-destructure-ret", lastUsePrelude + "function pw(n: i32): (W, i32) { return (mkw(n), n); }\nfunction f(): i32 { let (x, k) = pw(1); return eat(x) + k; }\n"},
		{"e051-last-use-destructure-read-again", lastUsePrelude + "function pw(n: i32): (W, i32) { return (mkw(n), n); }\nfunction f(): i32 { let (x, k) = pw(1); let r: i32 = eat(x); return r + k + x.n; }\n"},
		{"e051-last-use-destructure-at", lastUsePrelude + "function pw(n: i32): (W, i32) { return (mkw(n), n); }\nfunction f(): i32 { let t @ (x, k) = pw(1); let r: i32 = eat(x); return r + t.1; }\n"},
		{"e051-last-use-destructure-nested", lastUsePrelude + "function pw(n: i32): (W, i32) { return (mkw(n), n); }\nfunction pw2(n: i32): (i32, (W, i32)) { return (n, pw(n)); }\nfunction f(): i32 { let (k, (x, j)) = pw2(1); let r: i32 = eat(x); return r + k + j; }\n"},
		{"e051-last-use-destructure-redeclared", lastUsePrelude + "function pw(n: i32): (W, i32) { return (mkw(n), n); }\nfunction f(c: boolean): i32 { let (x, k) = pw(1); if (c) { let x: W = mkw(2); return x.n; } let r: i32 = eat(x); return r + k; }\n"},
		{"e051-last-use-struct-destructure", lastUsePrelude + "function pw(n: i32): (W, i32) { return (mkw(n), n); }\nfunction f(): i32 { let Pair { a, b } = mkp(1); let r: i32 = eat(a); return r + b.n; }\n"},
		{"e051-last-use-struct-destructure-param", lastUsePrelude + "function pw(n: i32): (W, i32) { return (mkw(n), n); }\nfunction f(p: Pair): i32 { let Pair { a, b } = p; let r: i32 = eat(a); return r + b.n; }\n"},
		{"e051-last-use-method-chain", lastUsePrelude + "function f(): i32 { let w: W = mkw(1); let v: W = w.bump(1); let u: W = v.bump(2); return eat(u); }\n"},
		{"e051-last-use-field-arg-local", lastUsePrelude + "function f(): i32 { let w: W = mkw(1); return keep(w.d, 1); }\n"},
		{"e051-last-use-if-expr", lastUsePrelude + "function f(c: boolean): i32 { let a: i32[] = mk(1); let r: i32 = if (c) { keep(a, 1) } else { 0 }; return r; }\n"},
		{"e051-last-use-if-expr-read-after", lastUsePrelude + "function f(c: boolean): i32 { let a: i32[] = mk(1); let r: i32 = if (c) { keep(a, 1) } else { 0 }; return r + a.len(); }\n"},
		{"e051-last-use-match-expr-read-after", lastUsePrelude + "enum E { A, B }\nfunction f(e: E): i32 { let w: W = mkw(1); let r: i32 = match (e) { A => eat(w), B => 0 }; return r + w.n; }\n"},
		{"e051-last-use-same-twice", lastUsePrelude + "function both(own a: W, own b: W): i32 { return a.n + b.n; }\nfunction f(): i32 { let x: W = mkw(1); return both(x, x); }\n"},
		{"e051-last-use-for-in", lastUsePrelude + "function f(): i32 { let t: i32 = 0; for i in [1, 2] { let w: W = mkw(i); t = t + eat(w); } return t; }\n"},
	}

	for _, tc := range progs {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(checkerBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], checkerBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			out := runCheckerDriver(t, cmd, tc.name)
			got := driverCodes(out)
			want := goCheckerCodes(t, dir, tc.src)
			if !equalStrings(got, want) {
				t.Errorf("%s: self-host codes %v disagree with Go checker %v (unfiltered)\nsrc: %s", tc.name, got, want, tc.src)
			}
		})
	}

	// Map's helpers all come from the one module, so one report covers every
	// use: native stops after the first with a checker-level flag
	// (mapErrReported), and thin_map_import_diags is this checker's pure
	// equivalent. The rows above cannot see it — every differential here
	// compares a de-duplicated code SET, so a program reported twice and a
	// program reported once are the same answer — and no row has two map
	// sites. This counts the diagnostics instead.
	t.Run("map-import-reported-once-per-program", func(t *testing.T) {
		const src = "function a(): i32 { let m: Map[i32, i32] = map_new(8); return m.len(); }\n" +
			"function b(): i32 { let m: Map[i32, i32] = map_new(4); return m.len(); }\n" +
			"function main(): i32 { return a() + b(); }\n"
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(checkerBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], checkerBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		out := runCheckerDriver(t, cmd, "map-import-reported-once-per-program")
		var mapDiags []driverDiag
		for _, d := range driverDiags(out) {
			if d.code == "E001" && strings.Contains(d.msg, "core/map") {
				mapDiags = append(mapDiags, d)
			}
		}
		// Two map_new sites, one diagnostic. Native answers the same program
		// with one, so anything else is a parity break rather than a style
		// choice; zero would mean the rule stopped firing at all.
		if len(mapDiags) != 1 {
			t.Errorf("two map_new sites drew %d missing-core/map diagnostics, want 1: %v\nsrc: %s", len(mapDiags), mapDiags, src)
		}
	})
}

// TestSelfHostCheckerBundleDifferentialX86_64 is the MULTI-MODULE differential
// verification harness. Unlike TestSelfHostCheckerDifferentialX86_64 (which
// checks a single self-contained module), it resolves each program's stdlib
// imports off disk and runs it through the file-based checker driver
// (checker_modload_run.fern → ./modloader → flatten.bundle → check_module),
// then asserts the self-host code set matches the Go checker's (modload +
// check) — unfiltered, with the Go checker as the sole oracle (no
// hardcoded expectations).
//
// This is the harness the method-table-dependent diagnostics need: it lets a
// rule keyed off an IMPORTED module's methods/types (string methods from
// std/string, etc.) be verified false-positive-free against real stdlib code
// before it ships. Seed it with valid stdlib-method programs; extend it when
// teaching the checker a rule that consults the imported table.
func TestSelfHostCheckerBundleDifferentialX86_64(t *testing.T) {
	_, runner, driverBin := buildCheckerModloadDriverX86(t)

	progs := []struct{ name, src string }{
		// `char` is NOT an integer. Both directions must be rejected without an
		// explicit cast — that distinctness is the type's whole point (#5629),
		// since a byte and a code point sharing i32 is what made
		// `s[i].to_upper()` and `to_upper_char(cp)` indistinguishable. The cast
		// itself stays legal, and the last case pins that: making char distinct
		// must not turn `n as char` / `c as i32` into E033.
		// `.map` on an `own` array is admitted by E053 like `.with` (#9733):
		// whether it is written through the donor is E068's question, on
		// both compilers. A borrowed receiver has no donor and stays E053.
		// Here rather than in the codes table because std/array has to be
		// in scope for the call to be a method call at all.
		{"e053-map-on-an-own-array", "import \"std/array\";\nfip function f(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => x); }\nfunction main(): i32 { return f([1 as i64]).len(); }\n"},
		// A derive resolves against the traits of the module its qualifier
		// names (#9322): the import makes `cmp.Eq` resolve and a bare `Eq`
		// still unknown, and a qualified derive's field conformance is
		// checked against the imported trait.
		{"derive-bare-with-cmp-imported", "import \"core/cmp\";\n@derive(Eq)\nstruct P { n: i32 }\nfunction main(): i32 { return 0; }\n"},
		{"derive-qualified-ok", "import \"core/cmp\";\n@derive(cmp.Eq, cmp.Hash)\nstruct P { n: i32, s: string }\nfunction main(): i32 { let p: P = P { n: 1, s: \"x\" }; if (p.eq(p)) { return 3; } return 0; }\n"},
		{"derive-aliased-import-ok", "import \"core/cmp\" as c;\n@derive(c.Eq)\nstruct P { n: i32 }\nfunction main(): i32 { return 0; }\n"},
		// An impl names its trait by the module that declares it (#10816): a
		// program's own `Ord` is not core/cmp's, whose impls for i32 do not
		// answer its requirement; `dyn cmp.Display`, qualified directly or
		// through an alias, takes what implements core/cmp's; and a program's
		// own `Display` takes nothing core/cmp's impls cover. A bare bound names
		// the trait of the module that wrote it, so `rank[T: Ord]` here reaches
		// the program's `Ord` and core/cmp's generics still reach their own.
		{"trait-same-name-as-imported-trait", "import \"std/i32\";\ntrait Eq { function eq(self: Self, other: Self): boolean; }\ntrait Ord: Eq { function lt(self: Self, other: Self): boolean; }\nstruct P { x: i32 }\nimpl Eq for P { function eq(self: Self, other: Self): boolean { return self.x == other.x; } }\nimpl Ord for P { function lt(self: Self, other: Self): boolean { return self.x < other.x; } }\nfunction main(): i32 { let p: P = P { x: 3 }; if (p.lt(P { x: 5 }) && !p.eq(P { x: 5 })) { return 0; } return 1; }\n"},
		{"trait-bound-same-name-as-imported-trait", "import \"std/i32\";\ntrait Eq { function eq(self: Self, other: Self): boolean; }\ntrait Ord: Eq { function lt(self: Self, other: Self): boolean; }\nstruct P { x: i32 }\nimpl Eq for P { function eq(self: Self, other: Self): boolean { return self.x == other.x; } }\nimpl Ord for P { function lt(self: Self, other: Self): boolean { return self.x < other.x; } }\nfunction rank[T: Ord](a: T, b: T): string { if (a.eq(b)) { return \"eq\"; } if (a.lt(b)) { return \"lt\"; } return \"gt\"; }\nfunction main(): i32 { print(rank(P { x: 3 }, P { x: 5 })); return 0; }\n"},
		{"dyn-qualified-imported-trait", "import \"core/cmp\";\nimport \"std/i32\";\nstruct Q { x: i32 }\nimpl cmp.Display for Q { function to_string(self: Self): string { return \"Q\"; } }\nfunction show(d: dyn cmp.Display): i32 { return d.to_string().len(); }\nfunction main(): i32 { let xs: dyn cmp.Display[] = [42, \"hi\", true]; let d: dyn cmp.Display = 42; let e: dyn cmp.Display = Q { x: 1 }; return show(42) + show(e) + xs.len() + d.to_string().len(); }\n"},
		{"dyn-aliased-imported-trait", "import \"core/cmp\" as c;\nimport \"std/i32\";\nstruct Q { x: i32 }\nimpl c.Display for Q { function to_string(self: Self): string { return \"Q\"; } }\nfunction main(): i32 { let xs: dyn c.Display[] = [Q { x: 7 }, 42]; return xs.len(); }\n"},
		{"dyn-own-trait-named-like-imported", "import \"core/cmp\";\nimport \"std/i32\";\ntrait Display { function show(self: Self): string; }\nfunction main(): i32 { let d: dyn Display = 42; return 0; }\n"},
		{"derive-qualified-field-no-impl", "import \"core/cmp\";\nstruct Q { n: i32 }\n@derive(cmp.Eq)\nstruct P { q: Q }\nfunction main(): i32 { return 0; }\n"},
		{"derive-aliased-bound-ok", "import \"core/cmp\" as c;\n@derive(c.Eq)\nstruct P { n: i32 }\nfunction same[T: c.Eq](a: T, b: T): boolean { return a.eq(b); }\nfunction main(): i32 { let p: P = P { n: 1 }; if (same(p, p)) { return 3; } return 0; }\n"},
		{"derive-unknown-qualifier", "@derive(cmp.Eq)\nstruct P { n: i32 }\nfunction main(): i32 { return 0; }\n"},
		// An entry const named like an imported variant shadows it in the
		// entry and never reaches the importing module's bodies (#11143).
		{"entry-const-named-like-imported-variant", "import \"std/dns\";\nconst A: i32 = 1;\nfunction main(): i32 { print(A.to_string()); return 0; }\n"},
		{"entry-const-named-like-imported-variant-e003", "import \"std/dns\";\nconst A: i32 = 1;\nfunction main(): i32 { let s: string = A; return 0; }\n"},
		// An imported module's struct named without its qualifier (#10965):
		// E064 on the annotation with E003 beside it, and E043 on the literal,
		// never a clean pass that the lowering then refuses.
		{"imported-struct-unqualified", "import \"std/http\";\nfunction main(): i32 {\n    let small: HttpLimits = http.http_limits();\n    let l: http.HttpLimits = HttpLimits { ...small, body: 1024 };\n    return l.body;\n}\n"},
		{"imported-struct-unqualified-literal", "import \"std/http\";\nfunction main(): i32 {\n    let l: http.HttpLimits = HttpLimits { request_line: 1, header_bytes: 2, header_fields: 3, body: 1024 };\n    return l.body;\n}\n"},
		// Seven programs native accepts that the self-host checker refused (#10767),
		// and the typed result of an inherent associated call that one of them needed.
		{"bitwise-and-shift-overloads", "struct F { b: i32 }\nfunction (self: F) bitand(o: F): F { return F { b: self.b & o.b }; }\nfunction (self: F) bitor(o: F): F { return F { b: self.b | o.b }; }\nfunction (self: F) bitxor(o: F): F { return F { b: self.b ^ o.b }; }\nfunction (self: F) shl(o: F): F { return F { b: self.b << o.b }; }\nfunction (self: F) shr(o: F): F { return F { b: self.b >> o.b }; }\nfunction main(): i32 { let a: F = F { b: 12 }; let c: F = a & a; c = a | c; c = a ^ c; c = a << F { b: 1 }; c = c >> F { b: 1 }; return c.b; }\n"},
		{"bitwise-on-a-struct-without-the-method", "struct F { b: i32 }\nfunction main(): i32 { let a: F = F { b: 12 }; let c: F = a & a; return c.b; }\n"},
		{"derived-display-record-variant", "import \"core/cmp\";\n@derive(cmp.Display)\nenum Shape { Circle { r: i32 }, Rect { w: i32, h: i32 }, Unit }\nfunction area(s: Shape): i32 { match (s) { Circle { r } => { return r; }, Rect { h, w } => { return w * h; }, Unit => { return 0; } } return 0; }\nfunction main(): i32 { return area(Rect(3, 4)) + Rect(3, 4).to_string().len(); }\n"},
		{"derived-display-multi-payload", "import \"core/cmp\";\n@derive(cmp.Display)\nenum Shape { Rect(i32, i32), Unit }\nfunction main(): i32 { return Rect(2, 3).to_string().len(); }\n"},
		{"owned-array-lent-to-a-generic-view", "function count[T](xs: [T]): i32 { return xs.len(); }\nfunction main(): i32 { let bytes: u8[] = [1, 2, 3]; let ints: i32[] = [10, 20]; return count(bytes) + count(ints); }\n"},
		{"function-shadows-builtin-none", "function None(v: i32): Option[i32] { return Some(v); }\nfunction mk(v: i32): Option[i32] { return None(v); }\nfunction main(): i32 { match (mk(5)) { Some(v) => { return v; }, None => { return 99; } } }\n"},
		{"generic-inherent-impl-assoc", "struct Box[T] { v: T }\nimpl[T] Box[T] {\n    function of(v: T): Box[T] { return Box { v: v }; }\n    function get(self: Self): T { return self.v; }\n}\nfunction main(): i32 { let b: Box[i32] = Box.of(42); return b.get(); }\n"},
		{"generic-inherent-impl-assoc-result-mismatch", "struct Box[T] { v: T }\nimpl[T] Box[T] {\n    function of(v: T): Box[T] { return Box { v: v }; }\n}\nfunction main(): i32 { let b = Box.of(42); let s: string = b; return 0; }\n"},
		{"try-inside-a-match-value", "function pick(p: Result[i32, i32]): Option[i32] {\n    return (match (p) { Ok(v) => Some(v + 1), Err(e) => Some(((Some(e))?) + 1) });\n}\nfunction main(): i32 { match (pick(Err(2))) { Some(v) => { return v; }, None => { return 1; } } }\n"},
		{"try-inside-a-match-value-in-an-i32-function", "function pick(p: Result[i32, i32]): i32 {\n    return (match (p) { Ok(v) => v, Err(e) => ((Some(e))?) });\n}\nfunction main(): i32 { return pick(Ok(3)); }\n"},
		{"e053-map-on-a-borrowed-array", "import \"std/array\";\nfip function f(xs: i64[]): i64[] { return xs.map((x: i64): i64 => x); }\nfunction main(): i32 { return f([1 as i64]).len(); }\n"},
		{"char-not-from-int-literal", "function main(): i32 { let c: char = 65; return 0; }\n"},
		{"char-not-to-i32-return", "function f(c: char): i32 { return c; }\nfunction main(): i32 { return 0; }\n"},
		{"char-not-from-i32-return", "function f(n: i32): char { return n; }\nfunction main(): i32 { return 0; }\n"},
		{"char-not-to-u8", "function main(): i32 { let cs: char[] = []; let b: u8 = cs[0]; return 0; }\n"},
		{"char-casts-both-ways-ok", "function main(): i32 { let c: char = 65 as char; let n: i32 = c as i32; return n; }\n"},
		// The NEGATIVE cases are the ones that discriminate. A bogus method on a
		// char must be E043 in both checkers; before #5922 the self-host reported
		// nothing at all, because `char` resolved in only one of five type-name
		// resolvers and an unknown receiver type skips every check — which made
		// the two positive cases below pass vacuously.
		{"char-method-bogus", "import \"std/utf8\";\nimport \"std/unicode\";\nfunction main(): i32 { let cs: char[] = utf8.codepoints(\"a\"); return cs[0].definitely_not_a_method(); }\n"},
		{"char-param-method-bogus", "function f(c: char): i32 { return c.definitely_not_a_method(); }\nfunction main(): i32 { return 0; }\n"},
		// A `char`-RECEIVER method call. std/unicode declares seven of them
		// ((c: char) to_upper / is_letter / ...), so if the self-host resolves a
		// char receiver to the i32 label while the declaration registered under
		// "char", every one of these is a spurious E043.
		{"char-method-to-upper", "import \"std/utf8\";\nimport \"std/unicode\";\nfunction main(): i32 { let cs: char[] = utf8.codepoints(\"a\"); return cs[0].to_upper() as i32; }\n"},
		{"char-method-is-letter", "import \"std/utf8\";\nimport \"std/unicode\";\nfunction main(): i32 { let cs: char[] = utf8.codepoints(\"a\"); if (cs[0].is_letter()) { return 1; } return 0; }\n"},
		// `<lit> as char` range/surrogate validation (E071, #5629 slice 5).
		// Both checkers must agree on which literals name a Unicode scalar
		// value. The self-host resolves `char` to i32, so it keys the rule on
		// the op text rather than the target type — these hold the two
		// implementations to the same answer, including the hex spelling and
		// both boundary values.
		{"char-lit-ok-ascii", "function main(): i32 { let c: char = 65 as char; return 0; }\n"},
		{"char-lit-ok-max", "function main(): i32 { let c: char = 1114111 as char; return 0; }\n"},
		{"char-lit-ok-below-surrogates", "function main(): i32 { let c: char = 55295 as char; return 0; }\n"},
		{"char-lit-ok-above-surrogates", "function main(): i32 { let c: char = 57344 as char; return 0; }\n"},
		{"char-lit-ok-hex", "function main(): i32 { let c: char = 0x10FFFF as char; return 0; }\n"},
		{"char-lit-e071-above-max", "function main(): i32 { let c: char = 1114112 as char; return 0; }\n"},
		{"char-lit-e071-huge", "function main(): i32 { let c: char = 2147483647 as char; return 0; }\n"},
		{"char-lit-e071-surrogate-lo", "function main(): i32 { let c: char = 55296 as char; return 0; }\n"},
		{"char-lit-e071-surrogate-hi", "function main(): i32 { let c: char = 57343 as char; return 0; }\n"},
		{"char-lit-e071-hex", "function main(): i32 { let c: char = 0x110000 as char; return 0; }\n"},
		// A runtime operand stays unchecked in BOTH — `as char` is the
		// reinterpret hatch, and a checker that flagged this would break
		// std/unicode's table lookups.
		{"char-runtime-cast-unchecked", "function main(): i32 { let n: i32 = 1114112; let c: char = n as char; return 0; }\n"},
		// Valid string-method calls (user-defined methods in std/string, not
		// builtins): both checkers must agree they are well-typed.
		{"string-contains-ok", "import \"std/string\";\nfunction main(): i32 { let s = \"abc\"; if (s.contains(\"b\")) { return 1; } return 0; }\n"},
		{"string-starts-with-ok", "import \"std/string\";\nfunction main(): i32 { let s = \"abc\"; if (s.starts_with(\"a\")) { return 1; } return 0; }\n"},
		{"string-is-empty-ok", "import \"std/string\";\nfunction main(): i32 { let s = \"\"; if (s.is_empty()) { return 1; } return 0; }\n"},
		{"string-trim-ok", "import \"std/string\";\nfunction main(): i32 { let s = \"  a \"; let t = s.trim(); return 0; }\n"},
		// `str` through an IMPORTED method's declared return (#7293): std/string's
		// trim family returns the borrowed view, declared `str` and
		// carried through flatten into the method-sig table, so an owning sink
		// refuses the view (E003 / E002) while a `str` binding, `.to_owned()`,
		// and an argument position (params are borrowed) stay clean. The
		// mismatch rows are the issue's repro: before the fix the self-host
		// compiled them to running binaries.
		{"str-trim-into-string-e003", "import \"std/string\";\nfunction main(): i32 { let raw: string = \"  ab  \"; let s: string = raw.trim(); return s.len(); }\n"},
		{"str-trim-assign-e003", "import \"std/string\";\nfunction main(): i32 { let raw: string = \"  ab  \"; let s: string = \"x\"; s = raw.trim(); return s.len(); }\n"},
		{"str-trim-return-e002", "import \"std/string\";\nfunction f(s: string): string { return s.trim(); }\nfunction main(): i32 { return 0; }\n"},
		{"str-trim-annotated-clean", "import \"std/string\";\nfunction main(): i32 { let raw: string = \"  ab  \"; let t: str = raw.trim(); return t.len(); }\n"},
		{"str-trim-to-owned-clean", "import \"std/string\";\nfunction main(): i32 { let raw: string = \"  ab  \"; let s: string = raw.trim().to_owned(); return s.len(); }\n"},
		{"str-trim-arg-borrow-clean", "import \"std/string\";\nfunction g(x: string): i32 { return x.len(); }\nfunction main(): i32 { let raw: string = \"  ab  \"; return g(raw.trim()); }\n"},
		{"string-to-lower-ok", "import \"std/string\";\nfunction main(): i32 { let s = \"AB\"; let t = s.to_lower(); return 0; }\n"},
		// Auto-discovered std/array methods (__method_Array_*) must resolve once
		// std/array is imported — the import-side companion to the single-module
		// E043 corpus (a.sum() without import → E043). A codegen-intercepted
		// method (sum) and a non-intercepted one (sum_squared) both stay clean;
		// an unknown method (bogus) is still E043 even with std/array in scope.
		{"array-sum-import-ok", "import \"std/array\";\nfunction main(): i32 { let a: i32[] = [1, 2, 3]; return a.sum(); }\n"},
		{"array-sum-squared-import-ok", "import \"std/array\";\nfunction main(): i32 { let a: i32[] = [1, 2, 3]; return a.sum_squared(); }\n"},
		{"array-bogus-import-e043", "import \"std/array\";\nfunction main(): i32 { let a: i32[] = [1, 2, 3]; return a.bogus(); }\n"},
		// std/array's ARRAY-RECEIVER methods — `function (xs: T[]) <name>[T: cmp.Eq](…)`,
		// which is how the whole Eq/Ord method surface is written. They are
		// MethodSigs with a `T[]` receiver, not `__method_Array_*` free
		// functions, so the E043 array-method-existence rule scanned the wrong
		// table and reported "no method" for every one of them while the Go
		// checker was clean. `index_of` / `contains` reach this shape as of
		// #6801; `index_of_last` always had it and was silently diverging.
		{"array-index-of-import-ok", "import \"std/array\";\nfunction main(): i32 { let a: i32[] = [1, 2, 3]; return a.index_of(2); }\n"},
		{"array-contains-import-ok", "import \"std/array\";\nfunction main(): i32 { let a: i32[] = [1, 2, 3]; if (a.contains(2)) { return 1; } return 0; }\n"},
		{"array-index-of-string-import-ok", "import \"std/array\";\nfunction main(): i32 { let a: string[] = [\"x\", \"y\"]; return a.index_of(\"y\"); }\n"},
		{"array-index-of-last-import-ok", "import \"std/array\";\nfunction main(): i32 { let a: i32[] = [1, 2, 1]; match (a.index_of_last(1)) { Some(i) => { return i; }, None => { return 0; } } }\n"},
		{"array-all-equal-import-ok", "import \"std/array\";\nfunction main(): i32 { let a: i32[] = [1, 1]; if (a.all_equal()) { return 1; } return 0; }\n"},
		// The receiver-method table must not become a blanket "any method
		// exists on an array" escape hatch. An unknown name is still E043 with
		// std/array in scope (`array-bogus-import-e043` above), and the same
		// receiver methods are still E043 with NOTHING imported — nothing
		// declares them then.
		{"array-index-of-no-import-e043", "function main(): i32 { let a: i32[] = [1, 2, 3]; return a.index_of(2); }\n"},
		{"array-contains-no-import-e043", "function main(): i32 { let a: i32[] = [1, 2, 3]; if (a.contains(2)) { return 1; } return 0; }\n"},
		// Array method RETURN-TYPING: `a.<m>()` resolves to the std/array
		// helper's declared return type, not `unknown`. So a result used in a
		// type-incompatible context surfaces the same E002/E003 the Go checker
		// reports (before, the unknown result was conservatively accepted and
		// the self-host stayed silent — a divergence). Scalar (sum→i32),
		// array (reversed→i32[]), bool (every_positive→boolean) and string
		// (join→string) returns are each covered on a clean and a mismatch path.
		{"array-sum-ret-i32-ok", "import \"std/array\";\nfunction main(): i32 { let a: i32[] = [1, 2, 3]; let x: i32 = a.sum(); return x; }\n"},
		{"array-sum-ret-string-mismatch", "import \"std/array\";\nfunction main(): i32 { let a: i32[] = [1, 2, 3]; let x: string = a.sum(); return 0; }\n"},
		// `sum` / `product` are the only array methods returning the ELEMENT
		// itself, so they are the only ones whose return has to be recovered
		// from the receiver rather than read off the declaration. The f64 rows
		// are the ones that catch it: a substitution that resolved the element to
		// i32 rather than to the receiver's own would pass every i32 row here
		// and report nothing for `let x: i32 = f.sum()`.
		{"array-product-ret-i32-ok", "import \"std/array\";\nfunction main(): i32 { let a: i32[] = [1, 2, 3]; let x: i32 = a.product(); return x; }\n"},
		{"array-product-ret-string-mismatch", "import \"std/array\";\nfunction main(): i32 { let a: i32[] = [1, 2, 3]; let x: string = a.product(); return 0; }\n"},
		{"array-sum-f64-elem-ok", "import \"std/array\";\nfunction main(): i32 { let f: f64[] = [1.5, 2.5]; let x: f64 = f.sum(); return 0; }\n"},
		{"array-sum-f64-elem-mismatch", "import \"std/array\";\nfunction main(): i32 { let f: f64[] = [1.5, 2.5]; let x: i32 = f.sum(); return 0; }\n"},
		{"array-reversed-ret-array-ok", "import \"std/array\";\nfunction main(): i32 { let a: i32[] = [1, 2, 3]; let b: i32[] = a.reverse(); return b[0]; }\n"},
		{"array-reversed-ret-string-mismatch", "import \"std/array\";\nfunction main(): i32 { let a: i32[] = [1, 2, 3]; let c: string = a.reverse(); return 0; }\n"},
		{"array-every-positive-ret-bool-ok", "import \"std/array\";\nfunction main(): i32 { let a: i32[] = [1, 2, 3]; if (a.every_positive()) { return 1; } return 0; }\n"},
		{"array-join-ret-string-ok", "import \"std/array\";\nfunction main(): i32 { let a: string[] = [\"x\", \"y\"]; let s: string = a.join(\",\"); return 0; }\n"},
		{"array-join-ret-i32-mismatch", "import \"std/array\";\nfunction main(): i32 { let a: string[] = [\"x\", \"y\"]; let n: i32 = a.join(\",\"); return 0; }\n"},
		// The string-method-existence E043 rule must still fire for a method
		// that no imported module defines, even WITH std/string in scope —
		// the bundle harness's reason for existing. `substr` isn't a std/string
		// method, so both checkers report E043.
		{"string-unknown-method-e043", "import \"std/string\";\nfunction main(): i32 { let s = \"abc\"; let t = s.substr(0, 1); return 0; }\n"},
		// #5205: a bundled helper that `match`es on an Option returned by a
		// method call (`result.checked_mul(base)` → Option[i32]) binding
		// `Some(v)` and assigning the payload to a concrete-typed local
		// (`result = v`). A built-in payload variant has no struct sig, so the
		// self-host used to bind `v` as the wrapper struct `Some` and spuriously
		// draw E003 on `result = v` — poisoning the whole bundle's code set for
		// every program that transitively imports the helper's module. The
		// helper's payload type is now bound unknown (unrecoverable in the
		// name-only type system), so both checkers agree the bundle is clean.
		{"match-method-option-payload-assign-ok", "import \"std/string\";\nfunction trig(base: i32): i32 { let result: i32 = 1; match (result.checked_mul(base)) { Some(v) => { result = v; }, None => { return 0; } } return result; }\nfunction main(): i32 { let s = \"abc\"; if (s.contains(\"b\")) { return 1; } return 0; }\n"},
		{"match-method-option-payload-assign-array-ok", "import \"std/array\";\nfunction trig(base: i32): i32 { let result: i32 = 1; match (result.checked_mul(base)) { Some(v) => { result = v; }, None => { return 0; } } return result; }\nfunction main(): i32 { let a: i32[] = [1, 2, 3]; return a.sum(); }\n"},
		// #6933: an ASSOCIATED (receiver-less) trait function reached through a
		// bound type parameter — `T.zero()` in std/num's `sum[T: Add + Zero]`.
		// The object is a TYPE, not a value, so walking it reported E001
		// "undefined name T" at every such site in std/num while the Go checker
		// was clean — importing std/num at all poisoned the code set. Merely
		// importing std/num also pulled `impl Iterator[i32] for Range` in via
		// core/iter, whose generic-trait requirement signature the E021
		// conformance check compared unsubstituted and always called a
		// mismatch, so this one case covers both diagnostics.
		// A parametrised-trait bound types the call (#9925): `I: Iterator[T]`
		// at `I = Range` reads T off `impl Iterator[i32] for Range`, so the
		// result is i32[] / Option[i32]. A destination the arguments leave T
		// open to binds it instead, and the impl then disagrees: E021, not the
		// E003 the inferred type would give.
		{"bound-infers-array-result", "import \"core/iter\";\nfunction main(): i32 { let xs = iter.to_array(iter.range(0, 5)); let s: string = xs[0]; return 0; }\n"},
		{"bound-infers-option-result", "import \"core/iter\";\nfunction main(): i32 { let r = iter.nth(iter.range(0, 5), 2); let x: string = r; return 0; }\n"},
		{"bound-infers-generic-impl-ok", "import \"core/iter\";\nfunction main(): i32 { let xs: f64[] = [1.5]; let ys = iter.to_array(iter.of(xs)); let z: f64 = ys[0]; let n: i32 = iter.to_array(iter.range(0, 3))[0]; return n; }\n"},
		{"bound-infers-argument-e038", "import \"core/iter\";\nfunction take(xs: string[]): i32 { return 0; }\nfunction main(): i32 { return take(iter.to_array(iter.range(0, 5))); }\n"},
		{"bound-infers-assignment-e003", "import \"core/iter\";\nfunction main(): i32 { let xs: string[] = []; xs = iter.to_array(iter.range(0, 5)); return 0; }\n"},
		{"bound-dest-var-e021", "import \"core/iter\";\nfunction main(): i32 { let xs: string[] = iter.to_array(iter.range(0, 5)); return 0; }\n"},
		{"bound-dest-return-e021", "import \"core/iter\";\nfunction f(): string[] { return iter.to_array(iter.range(0, 5)); }\nfunction main(): i32 { return 0; }\n"},
		{"bound-dest-option-e021", "import \"core/iter\";\nfunction main(): i32 { let m: Option[string] = iter.nth(iter.range(0, 5), 1); return 0; }\n"},
		{"bound-dest-generic-impl-e021", "import \"core/iter\";\nfunction main(): i32 { let xs: f64[] = [1.5]; let w: string[] = iter.to_array(iter.of(xs)); return 0; }\n"},
		{"bound-lambda-binds-e021", "import \"core/iter\";\nfunction main(): i32 { return iter.count_by(iter.range(0, 5), (x: string): boolean => true); }\n"},
		{"bound-qualified-infers-e003", "import \"core/iter\";\nfunction grab[T, I: iter.Iterator[T]](it: I): Option[T] { return iter.nth(it, 0); }\nfunction main(): i32 { let r = grab(iter.range(0, 3)); let x: string = r; return 0; }\n"},
		{"bound-qualified-dest-e021", "import \"core/iter\";\nfunction grab[T, I: iter.Iterator[T]](it: I): Option[T] { return iter.nth(it, 0); }\nfunction main(): i32 { let y: Option[string] = grab(iter.range(0, 3)); return 0; }\n"},
		// A value-`if` or value-`match` arm is read at the destination too
		// (#10264): the desugared IIFE's results each take it.
		{"bound-dest-value-if-var-e021", "import \"core/iter\";\nfunction main(): i32 { let xs: string[] = if (true) { iter.to_array(iter.range(0, 5)) } else { [] }; return 0; }\n"},
		{"bound-dest-value-if-return-e021", "import \"core/iter\";\nfunction f(c: boolean): string[] { return if (c) { iter.to_array(iter.range(0, 5)) } else { [] }; }\nfunction main(): i32 { return 0; }\n"},
		{"bound-dest-value-match-e021", "import \"core/iter\";\nfunction g(k: i32): string[] { let xs: string[] = match (k) { 0 => iter.to_array(iter.range(0, 5)), _ => [] }; return xs; }\nfunction main(): i32 { return 0; }\n"},
		{"bound-dest-value-if-agrees-ok", "import \"core/iter\";\nfunction ok(c: boolean): i32[] { let xs: i32[] = if (c) { iter.to_array(iter.range(0, 5)) } else { [] }; return xs; }\nfunction main(): i32 { return ok(true).len(); }\n"},
		// A bound is judged on each type parameter, under what the arguments
		// and the other bounds bind: T learned through I's impl is checked
		// against T's own bound, and against a later bound that names it.
		{"bound-on-a-type-parameter-e021", "trait C[U] { function c(self: Self): U; }\ntrait D[T] { function d(self: Self): T; }\nstruct X { n: i32 }\nstruct Y { n: i32 }\nimpl C[string] for X { function c(self: Self): string { return \"x\"; } }\nimpl D[X] for Y { function d(self: Self): X { return X { n: 1 }; } }\nfunction f[U, T: C[U], I: D[T]](it: I, u: U): i32 { return 0; }\nfunction main(): i32 { return f(Y { n: 1 }, 5); }\n"},
		{"bound-learned-through-another-bound-e021", "trait C[T] { function c(self: Self): T; }\ntrait A[T] { function a(self: Self): T; }\nstruct SC { n: i32 }\nstruct P { n: i32 }\nstruct V1 { n: i32 }\nimpl C[SC] for P { function c(self: Self): SC { return SC { n: 1 }; } }\nimpl A[string] for V1 { function a(self: Self): string { return \"v\"; } }\nfunction f[I: C[T], T, V: A[T]](i: I, v: V): i32 { return 0; }\nfunction main(): i32 { return f(P { n: 1 }, V1 { n: 2 }); }\n"},
		// An array literal's element reads the element type (#10266).
		{"bound-dest-array-element-e021", "import \"core/iter\";\nfunction main(): i32 { let xs: string[][] = [iter.to_array(iter.range(0, 5)), iter.to_array(iter.range(0, 2))]; return 0; }\n"},
		{"bound-dest-array-element-ok", "trait C[T] { function c(self: Self): T; }\nstruct A { n: i32 }\nimpl C[i32] for A { function c(self: Self): i32 { return self.n; } }\nfunction f[T, I: C[T]](i: I): T { return i.c(); }\nfunction main(): i32 { let ys: i32[] = [f(A { n: 2 }), 3]; let zs: i32[][] = [iter_free()]; return ys[0]; }\nfunction iter_free(): i32[] { return [1]; }\n"},
		{"bound-dest-array-element-scalar-e021", "trait C[T] { function c(self: Self): T; }\nstruct A { n: i32 }\nimpl C[i32] for A { function c(self: Self): i32 { return self.n; } }\nfunction f[T, I: C[T]](i: I): T { return i.c(); }\nfunction main(): i32 { let xs: string[] = [f(A { n: 1 })]; return 0; }\n"},
		{"bound-dest-agrees-ok", "import \"core/iter\";\nfunction main(): i32 { let xs: i32[] = iter.to_array(iter.range(0, 5)); let m: Option[i32] = iter.nth(iter.range(0, 5), 1); return xs[0]; }\n"},
		{"num-sum-assoc-tp-ok", "import \"std/num\";\nfunction main(): i32 { let xs: i32[] = [1, 2, 3]; return num.sum(xs); }\n"},
		{"num-product-assoc-tp-ok", "import \"std/num\";\nfunction main(): i32 { let xs: i32[] = [2, 3]; return num.product(xs); }\n"},
		// The same shape written by the USER, so the bound comes from the
		// program rather than from the imported module.
		{"assoc-tp-user-generic-ok", "import \"std/num\";\npub function total[T: num.Add + num.Zero](xs: T[]): T { let acc: T = T.zero(); for x in xs { acc = acc.add(x); } return acc; }\nfunction main(): i32 { let xs: i32[] = [1, 2, 3]; return total(xs); }\n"},
		// The gate is the BOUND, not "any name that looks like a type": a
		// method no bound provides is still the E001 the Go checker reports.
		{"assoc-tp-unbound-e001", "import \"std/num\";\npub function total[T: num.Add + num.Zero](xs: T[]): T { let acc: T = T.nope(); for x in xs { acc = acc.add(x); } return acc; }\nfunction main(): i32 { let xs: i32[] = [1, 2, 3]; return total(xs); }\n"},
		{"assoc-tp-single-bound-missing", "trait Zero { function zero(): Self; }\nfunction f[T: Zero](x: T): i32 { T.nope(); return 0; }\nfunction main(): i32 { return 0; }\n"},
		{"assoc-tp-other-parameter-bound", "trait Zero { function zero(): Self; }\ntrait One { function one(): Self; }\nfunction f[T: Zero, U: One](x: T, y: U): i32 { T.one(); return 0; }\nfunction main(): i32 { return 0; }\n"},
		{"assoc-tp-receiver-is-not-associated", "trait Value { function value(self: Self): i32; }\nfunction f[T: Value](x: T): i32 { return T.value(); }\nfunction main(): i32 { return 0; }\n"},
		{"assoc-tp-supertrait-ok", "trait Zero { function zero(): Self; }\ntrait Num: Zero { function one(): Self; }\nfunction f[T: Num](x: T): i32 { T.zero(); return 0; }\nfunction main(): i32 { return 0; }\n"},
		{"assoc-tp-deep-supertrait-ok", "trait A { function value(): i32; }\ntrait B: A {}\ntrait C: B {}\ntrait D: C {}\ntrait E: D {}\ntrait F: E {}\ntrait G: F {}\ntrait H: G {}\ntrait I: H {}\ntrait J: I {}\ntrait K: J {}\nfunction f[T: K](x: T): i32 { return T.value(); }\nfunction main(): i32 { return 0; }\n"},
		{"assoc-tp-default-ok", "trait Answer { function answer(): i32 { return 42; } }\nfunction f[T: Answer](x: T): i32 { return T.answer(); }\nfunction main(): i32 { return 0; }\n"},
		{"assoc-tp-supertrait-default-ok", "trait Answer { function answer(): i32 { return 42; } }\ntrait More: Answer { function extra(): i32; }\nfunction f[T: More](x: T): i32 { return T.answer(); }\nfunction main(): i32 { return 0; }\n"},
		{"assoc-tp-generated-generic-default-ok", "trait Default { function default(): Self; }\n@derive(Default) struct Inner { n: i32 }\n@derive(Default) struct Pair[T] { a: T, b: i32 }\nfunction main(): i32 { let p: Pair[Inner] = Pair.default(); return p.a.n + p.b; }\n"},
		// The CONCRETE spelling of the same call — what a monomorphised
		// `T.zero()` becomes — on a primitive and on a user type. Both must
		// resolve AND type: an unknown result would trip the self-host's
		// uncoded over-reject in a non-generic body.
		{"assoc-concrete-primitive-ok", "import \"std/num\";\nfunction main(): i32 { let z: i32 = i32.zero(); return z; }\n"},
		// #10120: the entry's unit variant `Ok` is invisible inside std/json,
		// whose `return Ok(v)` is Result's; in the entry both are visible, so
		// a bare `Ok(1)` there is only the E036 ambiguity.
		{"entry-variant-invisible-in-import-ok", "import \"std/json\";\nenum Reply { Ok, Full }\nfunction main(): i32 {\n    let r: Reply = Reply.Ok;\n    match (json.json_parse_result(\"1\")) { Ok(_) => { }, Err(_) => { return 1; } }\n    return match (r) { Ok => 0, Full => 2 };\n}\n"},
		// The same leak through the entry's union ALIAS: its members are
		// variants to this checker's union table, and an import cannot name
		// them any more than the entry's enums.
		{"entry-alias-invisible-in-import-ok", "import \"std/json\";\nstruct Ok { n: i32 }\nstruct Full { n: i32 }\ntype Reply = Ok | Full;\nfunction main(): i32 {\n    let r: Reply = Full { n: 2 };\n    match (json.json_parse_result(\"1\")) { Ok(_) => { }, Err(_) => { return 1; } }\n    return 0;\n}\n"},
		{"entry-variant-shadows-result-e036", "enum Reply { Ok, Full }\nfunction f(): Result[i32, string] { return Ok(1); }\nfunction main(): i32 { let r: Reply = Reply.Full; return match (r) { Reply.Ok => 0, Reply.Full => 2 }; }\n"},
		{"assoc-concrete-user-impl-ok", "import \"std/num\";\nstruct P { x: i32 }\nimpl num.Zero for P { function zero(): Self { return P { x: 0 }; } }\nfunction main(): i32 { let p: P = P.zero(); return p.x; }\n"},
		// #10094, the BUNDLED half. E001's missing-`core/map` rule reads the
		// program's import closure, which only exists here because
		// flatten.bundle keeps it on the merged module — it used to hand the
		// checker an empty list, which cannot tell these three apart. The
		// direct-import row is what a dropped list breaks (it would draw the
		// diagnostic the no-import row wants); the transitive row is what a
		// list narrowed to the ENTRY's own imports breaks, since std/json is
		// where core/map is imported.
		{"map-bundled-no-import", "function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 2); return m.get_or(1, 0) - 2; }\n"},
		{"map-bundled-direct-import", "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 2); return m.get_or(1, 0) - 2; }\n"},
		{"map-bundled-transitive-import", "import \"std/json\";\nfunction main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 2); return m.get_or(1, 0) - 2; }\n"},
		// An imported trait as a parameter type is desugared against the
		// modules this loader resolved, which record no `resolved` identity.
		{"trait-param-imported", "import \"core/cmp\";\nfunction describe(s: cmp.Display): string { return s.to_string(); }\nfunction main(): i32 { return describe(1).len(); }\n"},
		// #10095's escape, which only the bundled table can see: core/map's own
		// receiver methods are not runtime builtins, they reach the Map
		// namespace through the method table once the module is loaded. The
		// single-module driver records the import without loading it, so this
		// row would read as an unknown method there and prove the opposite.
		{"map-core-receiver-method-ok", "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, i32] = map_new(4); m = m.insert(1, 2); return m.entries().len(); }\n"},
	}

	for _, tc := range progs {
		t.Run(tc.name, func(t *testing.T) {
			out := checkSourceModload(t, runner, driverBin, tc.src)
			got := driverCodes(out)
			want := goCheckerCodes(t, t.TempDir(), tc.src)
			if !equalStrings(got, want) {
				t.Errorf("%s: self-host file-loader codes %v disagree with Go checker %v (unfiltered)\nsrc: %s", tc.name, got, want, tc.src)
			}
		})
	}
}
