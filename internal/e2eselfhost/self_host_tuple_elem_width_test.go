package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A tuple element's WIDTH is decided from its type tag, and every lowering that
// could not resolve one used to fall through to the untyped 4-byte
// `op_tuple_get`. That guess is invisible on the register backends (the value
// lands in a 64-bit register either way) and fatal on wasm, whose validator is
// the only thing in the project that reports the class at all — so these rows
// run on BOTH, and assert printed VALUES rather than that the module loads.
//
// Covered:
//   - #8458 `var (a, b, c) = <source>` over an (i64, f64, string) tuple, from
//     every source shape. The field / nested-field / receiver-field rows read
//     the f64 element as its low 32 bits and truncated the i64 before the fix.
//   - #8459 `p.ts[i].N` over a `(i32, f64)[]`, from every array shape.
//   - #8460 unary minus on a 64-bit operand, which lowered as a 32-bit
//     `0 - x` and made the self-host wasm emitter produce an invalid module.
//
// Nothing here imports the stdlib: the IR drivers read one module from stdin
// and resolve no imports, so each program carries its own decimal formatter.
// `df` renders an f64 in hundredths, which is exact for these values and
// nothing like the integer a mis-read bit pattern produces.
const tupleElemWidthPrelude = `function dig(n: i64): string {
	if (n == 0) { return "0"; }
	if (n == 1) { return "1"; }
	if (n == 2) { return "2"; }
	if (n == 3) { return "3"; }
	if (n == 4) { return "4"; }
	if (n == 5) { return "5"; }
	if (n == 6) { return "6"; }
	if (n == 7) { return "7"; }
	if (n == 8) { return "8"; }
	return "9";
}
function d64(v: i64): string {
	if (v == 0) { return "0"; }
	var neg: boolean = v < 0;
	var m: i64 = v;
	if (neg) { m = 0 - v; }
	var s: string = "";
	while (m > 0) {
		s = dig(m % 10) + s;
		m = m / 10;
	}
	if (neg) { return "-" + s; }
	return s;
}
function d32(v: i32): string { return d64(v as i64); }
function df(v: f64): string { return d64((v * 100.0) as i64); }
function show(a: i64, b: f64, c: string): string { return d64(a) + "|" + df(b) + "|" + c; }
`

var tupleElemWidthCases = []struct {
	name string
	src  string
	want string
}{
	// --- #8458: destructure source shapes, elements i64 / f64 / string. ---
	// The literal's i64 element needs the suffix: an unsuffixed literal in an
	// unannotated tuple literal is an i32 on both compilers.
	{"destructure-literal", `function main(): i32 { var (a, b, c) = (5000000000i64, 1.5, "lit"); print("v: " + show(a, b, c)); return 0; }`,
		"v: 5000000000|150|lit\n"},
	{"destructure-local", `function main(): i32 { var t: (i64, f64, string) = (5000000001, 2.5, "loc"); var (a, b, c) = t; print("v: " + show(a, b, c)); return 0; }`,
		"v: 5000000001|250|loc\n"},
	{"destructure-param", `function via(t: (i64, f64, string)): string { var (a, b, c) = t; return show(a, b, c); }
function main(): i32 { print("v: " + via((5000000002, 3.5, "par"))); return 0; }`,
		"v: 5000000002|350|par\n"},
	{"destructure-call", `function mkt(): (i64, f64, string) { return (5000000003, 4.5, "cal"); }
function main(): i32 { var (a, b, c) = mkt(); print("v: " + show(a, b, c)); return 0; }`,
		"v: 5000000003|450|cal\n"},
	{"destructure-field", `struct P { t: (i64, f64, string) }
function main(): i32 { var p: P = P { t: (5000000004, 5.5, "fld") }; var (a, b, c) = p.t; print("v: " + show(a, b, c)); return 0; }`,
		"v: 5000000004|550|fld\n"},
	{"destructure-nested-field", `struct Q { t: (i64, f64, string) }
struct P { q: Q }
function main(): i32 { var p: P = P { q: Q { t: (5000000005, 6.5, "nst") } }; var (a, b, c) = p.q.t; print("v: " + show(a, b, c)); return 0; }`,
		"v: 5000000005|650|nst\n"},
	{"destructure-receiver-field", `struct P { t: (i64, f64, string) }
function (p: P) via(): string { var (a, b, c) = p.t; return show(a, b, c); }
function main(): i32 { var p: P = P { t: (5000000006, 7.5, "rcv") }; print("v: " + p.via()); return 0; }`,
		"v: 5000000006|750|rcv\n"},

	// --- #8656: the payload of a user enum's variant. A tuple sub-pattern in
	// the payload slot (`Pr((a, b, c))`) desugars to a temp bound to the payload
	// and a destructure of that temp, so the binding must carry the element
	// tags the enum declares — without them the destructure has no width to
	// read at, which is the #8458 bail. The flat sibling (`Pr(p)` then `p.N`)
	// reads through the same tags, and the nested tuple destructures twice. ---
	{"destructure-variant-payload", `enum T { Pr((i64, f64, string)), Non }
function pick(t: T): string {
	match (t) {
		Pr((a, b, c)) => { return show(a, b, c); },
		Non => { return "non"; }
	}
}
function whole(t: T): string {
	match (t) {
		Pr(p) => { return show(p.0, p.1, p.2); },
		Non => { return "non"; }
	}
}
function main(): i32 {
	print("v: " + pick(T.Pr((5000000007, 8.5, "pay"))));
	print("w: " + whole(T.Pr((5000000008, 9.5, "flat"))));
	print("n: " + pick(T.Non));
	return 0;
}`, "v: 5000000007|850|pay\nw: 5000000008|950|flat\nn: non\n"},
	{"destructure-variant-payload-nested", `enum U { Q((i32, (i64, f64))), R }
function deep(u: U): string {
	match (u) {
		Q((a, (b, c))) => { return d32(a) + "|" + d64(b) + "|" + df(c); },
		R => { return "r"; }
	}
}
function main(): i32 {
	print("d: " + deep(U.Q((3, (5000000009, 1.5)))));
	print("r: " + deep(U.R));
	return 0;
}`, "d: 3|5000000009|150\nr: r\n"},

	// --- #8459: `.N` on an element of a `(i32, f64)[]`, every array shape. ---
	{"tuple-array-elem", `struct P { ts: (i32, f64)[] }
function mk(): (i32, f64)[] { return [(1, 3.5)]; }
function getp(): P { return P { ts: [(2, 4.5)] }; }
function main(): i32 {
	var p: P = P { ts: [(2, 4.5)] };
	var ps: (i32, f64)[] = mk();
	print("local: " + df(ps[0].1) + " " + d32(ps[0].0));
	print("call: " + df(mk()[0].1) + " " + d32(mk()[0].0));
	print("field: " + df(p.ts[0].1) + " " + d32(p.ts[0].0));
	print("callfield: " + df(getp().ts[0].1) + " " + d32(getp().ts[0].0));
	var f: f64 = p.ts[0].1;
	print("bind: " + df(f));
	return 0;
}`, "local: 350 1\ncall: 350 1\nfield: 450 2\ncallfield: 450 2\nbind: 450\n"},

	// --- #8745: the same array-of-tuples read whose base is a PARAMETER. The
	// four rows above cover a local, a call, a struct field and a call
	// result's field; a parameter had no route at all, because the param-slot
	// seeding recorded an element spelling only for a `T[][]` depth-2 array.
	// Every `(tuple)[]` parameter therefore answered "unknown", which #8458's
	// bail turned from a silent 4-byte read into a refusal of the whole
	// module — `std/url`'s `query_encode` is the shape that surfaced it, and
	// the destructure row is written the way that function reads its pairs. ---
	{"tuple-array-param", `function elems(ps: (i32, f64)[]): string { return df(ps[0].1) + " " + d32(ps[0].0); }
function destr(ps: (i32, f64)[]): string {
	var (k, v) = ps[0];
	return df(v) + " " + d32(k);
}
function loop_destr(ps: (i32, f64)[]): string {
	var out: string = "";
	var i: i32 = 0;
	while (i < ps.len()) {
		var (k, v) = ps[i];
		out = out + df(v) + " " + d32(k) + ";";
		i = i + 1;
	}
	return out;
}
function main(): i32 {
	var ps: (i32, f64)[] = [(2, 4.5), (3, 5.5)];
	print("read: " + elems(ps));
	print("destr: " + destr(ps));
	print("loop: " + loop_destr(ps));
	return 0;
}`, "read: 450 2\ndestr: 450 2\nloop: 450 2;550 3;\n"},

	// --- Same class, found while closing it: three shapes whose element
	// TYPES the lowering dropped, each of which read an f64 element as four
	// bytes of its bit pattern. A non-move tuple alias inherited the release
	// role but not the element tags; an array literal recorded an element tuple
	// tag only for an inline tuple LITERAL, not a tuple local; and a tuple
	// local used as an element of another tuple fell through to the "i32"
	// default. ---
	{"tuple-alias-and-containers", `function main(): i32 {
	var t = (3, 4.5);
	var u = t;
	print("alias: " + df(u.1) + " " + df(t.1));
	var t2 = (5, 6.5);
	var arr = [t2];
	print("arrlit: " + df(arr[0].1) + " " + d32(arr[0].0));
	var o = (t2, 99);
	print("intuple: " + df(o.0.1) + " " + d32(o.1));
	return 0;
}`, "alias: 450 450\narrlit: 650 5\nintuple: 650 99\n"},

	// --- #8460: unary minus at each width. ---
	// `i64-as-f64` and `i64-div` are the two shapes that actually reach the
	// 32-bit unary lowering: a plain `-b` in an i64-typed context is lowered by
	// lower_i64, which was already width-correct.
	{"unary-neg", `function main(): i32 {
	var b: i64 = 5000000000;
	print("i64: " + d64(-b));
	var u: u64 = 5000000000;
	print("u64: " + d64((0 - u) as i64));
	var i: i32 = 12345;
	print("i32: " + d32(-i));
	var f: f64 = 2.5;
	print("f64: " + df(-f));
	var g: f64 = (-b) as f64;
	print("i64-as-f64: " + df(g / 1000000.0));
	var h: i64 = 3;
	print("i64-div: " + d64((-b) / h));
	var nu: u64 = (0 - u) / 4;
	print("u64-div: " + d64(nu as i64));
	return 0;
}`, "i64: -5000000000\nu64: -5000000000\ni32: -12345\nf64: -250\ni64-as-f64: -500000\ni64-div: -1666666666\nu64-div: 4611686017177387904\n"},
}

// TestSelfHostTupleElemWidthX86_64 runs each program through the self-hosted
// x86-64 IR driver under FERN_STRICT_IR=1 — the answer alone would not say the
// shape stayed on the IR path, and a bail can reach the same answer by another
// route.
func TestSelfHostTupleElemWidthX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleElemWidthCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tupleElemWidthPrelude + tc.src + "\n")
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, src, "-ir")
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			out, _ := runX86_64Bin(runner, progBin).Output()
			if string(out) != tc.want {
				t.Errorf("%s stdout = %q, want %q", tc.name, string(out), tc.want)
			}
		})
	}
}

// TestSelfHostTupleElemWidthWasmIR is the same corpus on the wasm IR backend —
// the only backend in the project whose validator reports a wrong element
// width, since the register backends compute the right answer from a 64-bit
// register regardless of the load they emitted.
func TestSelfHostTupleElemWidthWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host tuple-element-width wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range tupleElemWidthCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tupleElemWidthPrelude + tc.src + "\n")
			wat := runCaptureStrictIR(t, gcc, runner, driverBin, src, "-ir")
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			out, err := exec.Command("wasmtime", "run", watFile).CombinedOutput()
			if err != nil {
				t.Fatalf("wasmtime run %s: %v\n%s", tc.name, err, out)
			}
			if string(out) != tc.want {
				t.Errorf("%s stdout = %q, want %q", tc.name, string(out), tc.want)
			}
		})
	}
}

// untypedTupleElemBailCases are destructures whose element types NOTHING in
// scope records. Binding them anyway is the miscompile the bail exists to
// prevent: the untyped element read is 4 bytes, so an f64 element came back as
// the low half of its bit pattern and an i64 element truncated, on every backend
// and with no diagnostic (#8458). The refusal names the element and the binding.
//
// Both shapes are typed correctly by the ANNOTATING drivers (asm_load_run, and
// the self-host CLI) from ExprIndex.ty, and compile there — these rows say what
// happens where the checker's stamp is absent, which is every IR driver that
// reads one module from stdin.
var untypedTupleElemBailCases = []struct {
	name string
	src  string
	want string
}{
	// A two-deep tuple array: no slot records the element spelling of a
	// `(…)[][]`, and the array-tag walk peels to the inner array, not the tuple.
	{"nested-tuple-array", `function main(): i32 { var ts: (i32, f64)[][] = [[(1, 2.5)]]; var (a, b) = ts[0][0]; return a; }`,
		"destructured tuple element 0 (`a`) has no type"},
	// An ERASED generic's tuple array. The self-host does not monomorphise
	// `mk[T]`, and T is not pinned by the registry (arrtup_ret_fns_of declines a
	// return spelling holding a type variable), so the element has no type here
	// at all.
	{"erased-generic-tuple-array", `function mk[T](x: T): (i32, T)[] { return [(1, x)]; }
function main(): i32 { var e = mk(5); var (a, b) = e[0]; return a + b; }`,
		"destructured tuple element 0 (`a`) has no type"},
}

func TestSelfHostUntypedTupleElemBailsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range untypedTupleElemBailCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := runX86_64Bin(runner, driverBin, "-ir")
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			cmd.Env = append(childEnv(), "FERN_STRICT_IR=1")
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 3 {
				t.Fatalf("%s: driver exited %d under FERN_STRICT_IR=1, want 3 (a named bail)\nstderr: %s", tc.name, code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("%s: bail diagnostic %q does not name the site (%q)", tc.name, stderr.String(), tc.want)
			}
			if stdout.Len() != 0 {
				t.Errorf("%s: driver emitted %d bytes of asm for a bailed module", tc.name, stdout.Len())
			}
		})
	}
}

// tupleFieldTypeLabelCases pin the type STRING the self-host checker renders
// for a struct field whose declared type is a tuple, a generic instantiation or
// a Map — against native, word for word. The self-host resolvers used to model
// only scalars, `Elem[]` and bare names in a field position, so `(i32, f64)[]`
// resolved to `unknown[]` and the unknown widened into every consumer of the
// field's type; the divergence is visible in an E003 message, which pins it
// cheaply (#8459).
var tupleFieldTypeLabelCases = []struct {
	name  string
	field string
	want  string
}{
	{"tuple-array", "(i32, f64)[]", "cannot assign (i32, f64)[] to variable of type i32"},
	{"generic-array", "Option[i32][]", "cannot assign Option[i32][] to variable of type i32"},
	{"nested-tuple-array", "(i32, (f64, i32))[]", "cannot assign (i32, (f64, i32))[] to variable of type i32"},
	{"map", "Map[string, i32]", "cannot assign Map[string, i32] to variable of type i32"},
	{"tuple", "(i32, f64)", "cannot assign (i32, f64) to variable of type i32"},
}

func TestSelfHostTupleFieldTypeLabelParity(t *testing.T) {
	checkerBin, runner, dir := buildCheckerCodesBin(t)

	for _, tc := range tupleFieldTypeLabelCases {
		t.Run(tc.name, func(t *testing.T) {
			src := "struct P { f: " + tc.field + " }\n" +
				"function mkp(): P { return P { f: mkf() }; }\n" +
				"function mkf(): " + tc.field + " { return mkf(); }\n" +
				"function main(): i32 { var bad: i32 = mkp().f; return bad; }\n"

			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(checkerBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], checkerBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(src))
			got := driverDiags(runCheckerDriver(t, cmd, tc.name))

			if len(got) != 1 || got[0].code != "E003" || got[0].msg != tc.want {
				t.Errorf("%s: self-host diagnostics = %v, want one E003 %q", tc.name, got, tc.want)
			}
			// Differential: native must say the same thing, so the row cannot
			// decay into pinning a spelling only the self-host uses.
			goDiags := goCheckerDiags(t, dir, src)
			if len(goDiags) != 1 || goDiags[0].code != "E003" || goDiags[0].msg != tc.want {
				t.Errorf("%s: native diagnostics = %v, want one E003 %q", tc.name, goDiags, tc.want)
			}
		})
	}
}
