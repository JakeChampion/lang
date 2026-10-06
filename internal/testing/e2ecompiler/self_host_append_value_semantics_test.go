package e2ecompiler

import "testing"

// #6891: an expression-position `.append` whose receiver is READ AGAIN must
// have value semantics. op_arr_push consumes its receiver — at rc==1 with
// spare capacity the grow helper appends into the receiver's own buffer and
// returns that pointer — so `sink(roomy.append(20))` grew `roomy` itself and
// the `sink(roomy)` under it read four elements where the interpreter and
// native read three. rc is the wrong test here: `roomy` is uniquely
// referenced and still read twice, and rc counts references, not uses.
//
// The fix brackets the receiver with a retain/release around the push, so the
// helper's uniqueness gate takes the copy path (native's emitArrayPush does
// the same, #4827/#4838). It is the ANSWER that diverged, so these cases are
// differential — every expectation comes from `fern -interp` rather than a
// written-down number, and the allocation-shape conformance case that carries
// the same shape (alloc_flat_consumed_append) cannot see this at all.
//
// SPARE CAPACITY is what makes it visible: with none the grow allocates a
// fresh buffer anyway and the receiver is untouched, which is why the ordinary
// corpus missed it. Each case therefore appends three times from empty first.
//
// The last two are controls on the exemption (append_inplace_names_of): the
// self-reassign `a = a.append(v)` and the accumulator tail
// `return acc.append(v)` may still mutate in place, and forcing a copy there
// would be O(n²).
var selfHostAppendValueCases = []struct {
	name string
	src  string
}{
	// The reported shape: an argument-position append on a reused i32[].
	{"arg-position-i32", "function sink(xs: i32[]): i32 {\n    let s: i32 = 0;\n    for i in 0..xs.len() { s = s + xs[i]; }\n    return s;\n}\nfunction main(): i32 {\n    let roomy: i32[] = [];\n    roomy = roomy.append(1);\n    roomy = roomy.append(2);\n    roomy = roomy.append(3);\n    let a: i32 = sink(roomy.append(20));\n    return a + sink(roomy) * 2 + roomy.len();\n}"},
	// A pointer-element receiver: the copy shares element pointers, so this
	// also pins that the un-shared copy still reads live strings.
	{"arg-position-string", "function chars(xs: string[]): i32 {\n    let t: i32 = 0;\n    for i in 0..xs.len() { t = t + xs[i].len(); }\n    return t;\n}\nfunction main(): i32 {\n    let xs: string[] = [];\n    xs = xs.append(\"aa\");\n    xs = xs.append(\"bb\");\n    xs = xs.append(\"cc\");\n    let a: i32 = chars(xs.append(\"dddd\"));\n    return a + chars(xs) + xs.len();\n}"},
	// The 8-byte-slot widths take their own push helper on wasm
	// ($__fern_arr_push_i64 / _f64), which had no rc gate at all.
	{"arg-position-i64", "function s8(xs: i64[]): i64 {\n    let t: i64 = 0i64;\n    for i in 0..xs.len() { t = t + xs[i]; }\n    return t;\n}\nfunction main(): i32 {\n    let xs: i64[] = [];\n    xs = xs.append(1i64);\n    xs = xs.append(2i64);\n    xs = xs.append(3i64);\n    let a: i64 = s8(xs.append(20i64));\n    let b: i64 = s8(xs);\n    return (a as i32) + (b as i32) * 2 + xs.len();\n}"},
	{"arg-position-f64", "function sf(xs: f64[]): f64 {\n    let t: f64 = 0.0;\n    for i in 0..xs.len() { t = t + xs[i]; }\n    return t;\n}\nfunction main(): i32 {\n    let xs: f64[] = [];\n    xs = xs.append(1.0);\n    xs = xs.append(2.0);\n    xs = xs.append(3.0);\n    let a: f64 = sf(xs.append(20.0));\n    let b: f64 = sf(xs);\n    return (a as i32) + (b as i32) * 2 + xs.len();\n}"},
	// A map literal is the position the receiver census could not see:
	// collect_append_recvs_expr had no ExprMapLit / ExprFString arm, so every
	// other occurrence of `roomy` here is a self-reassign and the name would
	// have kept the in-place exemption it does not deserve. Same hole in the
	// #4873 may-grow param census, which shares the collector.
	{"map-literal-position", "import \"core/map\";\nfunction sink(xs: i32[]): i32 {\n    let s: i32 = 0;\n    for i in 0..xs.len() { s = s + xs[i]; }\n    return s;\n}\nfunction main(): i32 {\n    let roomy: i32[] = [];\n    roomy = roomy.append(1);\n    roomy = roomy.append(2);\n    roomy = roomy.append(3);\n    let m: Map[string, i32] = Map { \"k\": sink(roomy.append(20)) };\n    return m.get_or(\"k\", 0) + sink(roomy) * 2 + roomy.len();\n}"},
	// Not an argument: a receiver read again from the same expression.
	{"operand-position", "function main(): i32 {\n    let roomy: i32[] = [];\n    roomy = roomy.append(1);\n    roomy = roomy.append(2);\n    roomy = roomy.append(3);\n    return roomy.append(9).len() * 10 + roomy.len();\n}"},
	// A borrowed param appended once per loop iteration — the caller-side
	// #4873 bracket already contained this one; it is here so the new
	// receiver-side bracket cannot double-count or drop it.
	{"loop-body-param", "function sink(xs: i32[]): i32 {\n    let s: i32 = 0;\n    for i in 0..xs.len() { s = s + xs[i]; }\n    return s;\n}\nfunction walk(path: i32[]): i32 {\n    let t: i32 = 0;\n    for i in 0..4 { t = t + sink(path.append(i)); }\n    return t;\n}\nfunction main(): i32 {\n    let p: i32[] = [];\n    p = p.append(1);\n    p = p.append(2);\n    return walk(p) + p.len();\n}"},
	// Control: both in-place shapes still produce the right answer.
	{"inplace-shapes-control", "function tail(acc: i32[], x: i32): i32[] {\n    return acc.append(x);\n}\nfunction main(): i32 {\n    let a: i32[] = [];\n    let i: i32 = 0;\n    while (i < 20) { a = tail(a, i); i = i + 1; }\n    let b: i32[] = [];\n    let j: i32 = 0;\n    while (j < 20) { b = b.append(j); j = j + 1; }\n    return a.len() + b.len();\n}"},
}

// TestSelfHostAppendValueSemanticsX86_64 — the x86-64 leg against the
// interpreter oracle.
func TestSelfHostAppendValueSemanticsX86_64(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	cli := newStrictCLI(t)
	for _, tc := range selfHostAppendValueCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostAppendValueSemanticsArm64 — the same cases through the arm64
// emit. The receiver bracket is shared lowering analysis, so this leg guards
// the register backends agreeing on the grow helper's uniqueness gate.
func TestSelfHostAppendValueSemanticsArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	cli := newStrictCLI(t)
	for _, tc := range selfHostAppendValueCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostAppendValueSemanticsWasmIR — the wasm leg, and the only one
// that reaches $__fern_arr_push_i64 / $__fern_arr_push_f64: the register
// backends route every element width through the rc-gated __fern_arr_push,
// while wasm has separate 8-byte-slot helpers that had no rc gate at all, so
// the i64[] / f64[] cases stayed wrong there after the lowering was fixed.
func TestSelfHostAppendValueSemanticsWasmIR(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	cli := newStrictCLI(t)
	for _, tc := range selfHostAppendValueCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
