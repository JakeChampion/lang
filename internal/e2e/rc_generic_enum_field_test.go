package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #8829 — a generic enum INSTANTIATION is deep-drop-wired, so a struct
// holding one stays in the owned model.
//
// Generic enums are not monomorphised: `Option[i32]` and `Option[string]`
// share one EnumDecl whose payloads are the type parameter, and the concrete
// types live in EnumType.Args. rc_caps.go's capability walks read that decl
// without substituting, met a ParamType, and answered "deep drop not wired"
// for every Option and Result in the language — which took every struct
// holding one out of owned-by-default and cost it every reuse path. The
// classification is pinned in internal/ir/rc_caps_test.go; these pin what
// the emitted runtime does.
//
// std/io_buffered's BufWriter is the shape that surfaced it: its `err:
// Option[IoError]` field alone put the writer's `buf: b.buf + s` back on a
// fresh box and a whole-buffer copy per append. 3M 17-byte writes took 3.9 s
// and now take 43 ms.

// genericEnumFieldGrowSrc accumulates through a struct field on a struct that
// also carries generic-enum fields at each payload shape the walk classifies
// differently: a heap-payload one, a scalar-payload one, an array payload, a
// nested instantiation, and a two-parameter enum.
const genericEnumFieldGrowSrc = `struct Inner { s: string }
struct Acc {
    buf: string,
    o_str: Option[string],
    o_i32: Option[i32],
    o_arr: Option[i32[]],
    o_struct: Option[Inner],
    o_nest: Option[Option[string]],
    r: Result[string, i32],
    n: i32,
}

function fresh(): Acc {
    return Acc {
        buf: "",
        o_str: Some("payload-string-long-enough-to-heap"),
        o_i32: Some(7),
        o_arr: Some([1, 2, 3]),
        o_struct: Some(Inner { s: "inner-string-long-enough-to-heap" }),
        o_nest: Some(Some("nested-string-long-enough-to-heap")),
        r: Ok("ok-string-long-enough-to-heap-allocate"),
        n: 0,
    };
}

function (a: Acc) grow(piece: string): Acc {
    return Acc { ...a, buf: a.buf + piece, n: a.n + 1 };
}

function main(): i32 {
    var a: Acc = fresh();
    var i: i32 = 0;
    while (i < 2000) {
        a = a.grow("ab");
        i = i + 1;
    }
    if (a.buf.len() != 4000) { return 1; }
    if (a.n != 2000) { return 2; }
    match (a.o_str) { Some(s) => { if (s.len() != 34) { return 3; } }, None => { return 4; } }
    match (a.o_i32) { Some(v) => { if (v != 7) { return 5; } }, None => { return 6; } }
    match (a.o_arr) { Some(xs) => { if (xs.len() != 3) { return 7; } }, None => { return 8; } }
    match (a.o_struct) { Some(v) => { if (v.s.len() != 32) { return 9; } }, None => { return 10; } }
    match (a.o_nest) {
        Some(o) => { match (o) { Some(s) => { if (s.len() != 33) { return 11; } }, None => { return 12; } } },
        None => { return 13; }
    }
    match (a.r) { Ok(s) => { if (s.len() != 38) { return 14; } }, Err(_) => { return 15; } }
    return 0;
}`

// genericEnumFieldAliasSrc is the discriminating one: the struct's box is
// aliased while its buffer is not, so a gate on anything but the BOX's own
// runtime uniqueness grows a buffer the second alias still reads through the
// old box. Admitting the type into the owned model must not weaken that gate
// — the alias is what proves the reuse still declines.
//
// The accumulator starts as a heap string (__fern_str_append refuses to grow
// a .rodata literal whatever the rc says) and twenty bytes plus one stays
// inside one allocator class, so a decline here is the gate and not a class
// boundary.
const genericEnumFieldAliasSrc = `struct B { buf: string, tag: Option[string], n: i32 }

function heap(s: string): string { return s + ""; }

function main(): i32 {
    var b: B = B { buf: heap("0123456789abcdefghij"), tag: Some("t"), n: 1 };
    var al: B = b;
    b = B { ...b, buf: b.buf + "X", n: b.n + 1 };
    print(al.buf);
    print(b.buf);
    al = B { ...al, buf: al.buf + "Y", n: al.n + 7 };
    print(al.buf);
    print(b.buf);
    if (al.n != 8) { return 1; }
    if (b.n != 2) { return 2; }
    return 0;
}`

const genericEnumFieldAliasWant = `0123456789abcdefghij
0123456789abcdefghijX
0123456789abcdefghijY
0123456789abcdefghijX`

// TestX86_64GenericEnumFieldAppendAllocsBounded pins the collapse. Measured
// on this program: 4007 allocations before the fix, 141 after — the before
// figure is one box and one whole-buffer copy per append, which is the
// quadratic. The assertion is the invariant rather than the number.
func TestX86_64GenericEnumFieldAppendAllocsBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckX86_64(t, genericEnumFieldGrowSrc)
	if code != 0 {
		t.Fatalf("accumulator exited %d (a check inside it failed); stdout=%q stderr=%q", code, stdout, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs > 3000 {
		t.Errorf("allocs = %d for 2000 field appends on a struct with generic-enum fields, "+
			"want well under 3000; the type is not reaching the owned model", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	}
}

// The aliased-box leg: the answers, and a balanced heap, which catches an
// over-release from the newly-admitted deep drop as firmly as a leak.
func TestX86_64GenericEnumFieldAliasedBoxIsNotMutated(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckX86_64(t, genericEnumFieldAliasSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != genericEnumFieldAliasWant+"\n" {
		t.Errorf("x86-64 aliased-box append =\n%q\nwant\n%q", stdout, genericEnumFieldAliasWant+"\n")
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	}
}

// arm64 keeps the plain concat (no __fern_str_append helper), so its win is
// the box reuse alone — but it takes the same ownership decision, and its
// exit sweep runs the deep drop this change newly admits. Balanced counts
// here are the arm64 half of that.
func TestArm64GenericEnumFieldDropsAreBalanced(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckArm64(t, genericEnumFieldGrowSrc)
	if code != 0 {
		t.Fatalf("accumulator exited %d; stdout=%q stderr=%q", code, stdout, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	}
}

func TestArm64GenericEnumFieldAliasedBoxIsNotMutated(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckArm64(t, genericEnumFieldAliasSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != genericEnumFieldAliasWant+"\n" {
		t.Errorf("arm64 aliased-box append =\n%q\nwant\n%q", stdout, genericEnumFieldAliasWant+"\n")
	}
}

// The two-word (wasm) legs. A string field fans out to (data, len) there, so
// the deep drop this change admits releases two words per field rather than
// one — the ABI half of the same question.
func TestWASMGenericEnumFieldAppendCorrect(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	if code := runWasm(t, genericEnumFieldGrowSrc); code != 0 {
		t.Errorf("wasm accumulator exited %d, want 0", code)
	}
}

func TestWASMGenericEnumFieldAliasedBoxIsNotMutated(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	if got := runWasmCapturingStdout(t, genericEnumFieldAliasSrc); got != genericEnumFieldAliasWant {
		t.Errorf("wasm aliased-box append =\n%q\nwant\n%q", got, genericEnumFieldAliasWant)
	}
}

// A generic enum whose ARGUMENT carries a Map stays out: only the substituted
// walk can see it, since the Map is in EnumType.Args and never in the shared
// decl. Map-in-enum reclamation is an open gap (__map_drop_values is not
// pulled into a generated __drop_enum_ body on wasm), so admitting this shape
// would hand the owned model a drop it cannot run.
func TestOptionOfMapStaysOutOfTheOwnedModel(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	const src = `import "core/map";

struct H { buf: string, m: Option[Map[string, i32]], n: i32 }

function (h: H) grow(s: string): H {
    return H { ...h, buf: h.buf + s, n: h.n + 1 };
}

function main(): i32 {
    var mm: Map[string, i32] = map_new(8);
    mm = mm.insert("k", 3);
    var h: H = H { buf: "", m: Some(mm), n: 0 };
    var i: i32 = 0;
    while (i < 50) { h = h.grow("cccc"); i = i + 1; }
    if (h.buf.len() != 200) { return 1; }
    if (h.n != 50) { return 2; }
    match (h.m) { Some(m) => { if (m.len() != 1) { return 3; } }, None => { return 4; } }
    return 0;
}`
	if _, stderr, code := runLeakCheckX86_64(t, src); code != 0 {
		t.Fatalf("Option[Map] program exited %d, want 0; stderr=%q", code, stderr)
	}
}
