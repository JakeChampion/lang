package checker

import (
	"github.com/jakechampion/lang/internal/syntax/diag"
	"testing"
)

func TestImmutableCallResultsCanBeCopied(t *testing.T) {
	for name, src := range map[string]string{

		"generic data":          `struct Data[T] { value: T } function loan[T](x: Data[T]): Data[T] { return x; } function take(own x: Data[i32]): void {} function run(x: Data[i32]): void { let y = loan(x); take(y); print(x.value); }`,
		"borrowed field result": `struct Wv { v: i32[] } function inner(w: Wv): i32[] { return w.v; } function consume(own xs: i32[]): i32 { return xs.len(); } function f(w: Wv): i32 { return consume(inner(w)); }`,
		"direct array":          `function loan(x: i32[]): i32[] { return x; } function take(own x: i32[]): void {} function run(x: i32[]): void { take(loan(x)); print(x.len()); }`,
		"local array":           `function loan(x: i32[]): i32[] { return x; } function take(own x: i32[]): void {} function run(x: i32[]): void { let y = loan(x); take(y); print(x.len()); }`,
		"callback result":       `function run(x: i32[], f: (i32[]) => i32[]): void { take(f(x)); print(x.len()); } function take(own x: i32[]): void {}`,
		"nested data":           `struct Data { bytes: u8[] } function loan(x: Data): Data { return x; } function take(own x: Data): void {} function run(x: Data): void { let y = loan(x); take(y); print(x.bytes.len()); }`,
		"checked value":         `@value struct Key { bytes: u8[] } function (own x: Key) close(): void {} function loan(x: Key): Key { return x; } function take(own x: Key): void {} function run(x: Key): void { take(loan(x)); print(x.bytes.len()); }`,
	} {
		wantOK(t, name, src+` function main(): i32 { return 0; }`)
	}
}

func TestCallResultCopiesExcludeLoans(t *testing.T) {
	for name, src := range map[string]string{
		"resource":              `struct H { fd: i32 } function (own h: H) close(): void {} function loan(x: H): H { return x; } function take(own x: H): void {} function run(x: H): void { take(loan(x)); }`,
		"resource array":        `struct H { fd: i32 } function (own h: H) close(): void {} function loan(x: H[]): H[] { return x; } function take(own x: H[]): void {} function run(x: H[]): void { let y = loan(x); take(y); }`,
		"cell":                  `function loan(x: Cell[i32]): Cell[i32] { return x; } function take(own x: Cell[i32]): void {} function run(x: Cell[i32]): void { take(loan(x)); }`,
		"callback":              `function loan(x: () => i32): () => i32 { return x; } function take(own x: () => i32): void {} function run(x: () => i32): void { take(loan(x)); }`,
		"reused generic cell":   `struct Data[T] { value: T } struct Mixed { first: Data[i32], second: Data[Cell[i32]] } function loan(x: Mixed): Mixed { return x; } function consume(own x: Mixed): void {} function run(x: Mixed): void { consume(loan(x)); }`,
		"shadowed data name":    `struct Data { bytes: u8[] } function loan[Data](x: Data): Data { return x; } function consume[Data](own x: Data): void {} function run[Data](x: Data): void { consume(loan(x)); }`,
		"unconstrained generic": `function loan[T](x: T): T { return x; } function take[T](own x: T): void {} function run[T](x: T): void { take(loan(x)); }`,
	} {
		wantE051(t, name, src+` function main(): i32 { return 0; }`)
	}
}

func TestCallCopiesDoNotStrengthenFunctionTypes(t *testing.T) {
	err := checkSource(t, `function loan(x: i32[]): i32[] { return x; } function main(): i32 { let f: (i32[]) => own i32[] = loan; return 0; }`)
	if err == nil || !hasCode(err, "E003") {
		t.Fatalf("expected E003, got %v", err)
	}
}

func TestLegacyResourceCallResultsRetainLoans(t *testing.T) {
	for name, src := range map[string]string{
		"e051-result-is-the-borrowed-parameter": `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys[0].fd; }
function passthru(xs: H[]): H[] { return xs; }
function borrowed_source(source: H[]): i32 { return keep(passthru(source)); }
`,
		"e051-result-borrowed-through-a-local": `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys[0].fd; }
function hop(xs: H[]): H[] {
    let y: H[] = xs;
    return y;
}
function borrowed_source(source: H[]): i32 { return keep(hop(source)); }
`,
		"e051-result-borrowed-through-a-chain": `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys[0].fd; }
function passthru(xs: H[]): H[] { return xs; }
function relay(ys: H[]): H[] { return passthru(ys); }
function borrowed_source(source: H[]): i32 { return keep(relay(source)); }
`,
		"e051-result-borrowed-through-a-generic-callback": `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys[0].fd; }
function fold[A](xs: H[], acc: A, f: (H[], A) => A): A { return f(xs, acc); }
function pick(xs: H[], acc: H[]): H[] { return xs; }
function collect(xs: H[]): H[] { let none: H[] = []; return fold(xs, none, pick); }
function borrowed_source(source: H[]): i32 { let xs: H[] = source; return keep(collect(xs)); }
`,
		"e051-result-borrowed-through-a-function-parameter": `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys[0].fd; }
function via(xs: H[], f: (H[]) => H[]): H[] { return f(xs); }
function same(xs: H[]): H[] { return xs; }
function borrowed_source(source: H[]): i32 { let xs: H[] = source; return keep(via(xs, same)); }
`,
		"e051-result-borrowed-through-a-closure-local": `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys[0].fd; }
function via(xs: H[]): H[] {
    let f: (H[]) => H[] = (a: H[]): H[] => a;
    return f(xs);
}
function borrowed_source(source: H[]): i32 { let xs: H[] = source; return keep(via(xs)); }
`,
		"e051-result-borrowed-through-a-bounded-generic": `struct H { fd: i32 } function (own h: H) close(): void {}
trait Named {
  function name(self: Self): string;
}
struct N { s: string }
impl Named for N {
  function name(self: N): string { return self.s; }
}
function keep(own ys: H[]): i32 { return ys[0].fd; }
function first[T: Named](t: T, xs: H[]): H[] { return xs; }
function relay(xs: H[]): H[] { return first(N { s: "a" }, xs); }
function borrowed_source(source: H[]): i32 { let xs: H[] = source; return keep(relay(xs)); }
`,
		"e051-result-borrowed-through-a-value-if": `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys[0].fd; }
function pick(c: boolean, xs: H[], ys: H[]): H[] { return if (c) { xs } else { ys }; }
function borrowed_source(source: H[]): i32 { let xs: H[] = source; let ys: H[] = []; return keep(pick(true, xs, ys)); }
`,
		"e051-result-borrowed-through-an-else-if-arm": `struct H { fd: i32 } function (own h: H) close(): void {}
function mk(): H[] { return [H { fd: 2 }]; }
function keep(own ys: H[]): i32 { return ys[0].fd; }
function pick(a: i32, xs: H[]): H[] { return if (a == 1) { mk() } else if (a == 2) { xs } else { mk() }; }
function borrowed_source(source: H[]): i32 { let xs: H[] = source; return keep(pick(1, xs)); }
`,
		"e051-result-borrowed-through-a-value-if-local": `struct H { fd: i32 } function (own h: H) close(): void {}
function mk(): H[] { return [H { fd: 2 }]; }
function keep(own ys: H[]): i32 { return ys[0].fd; }
function pick(c: boolean, xs: H[]): H[] {
    let r: H[] = if (c) { xs } else { mk() };
    return r;
}
function borrowed_source(source: H[]): i32 { let xs: H[] = source; return keep(pick(true, xs)); }
`,
		"e051-result-borrowed-through-a-value-match": `struct H { fd: i32 } function (own h: H) close(): void {}
enum E { A, B }
function keep(own ys: H[]): i32 { return ys[0].fd; }
function pick(e: E, xs: H[], ys: H[]): H[] { return match (e) { E.A => xs, E.B => ys }; }
function borrowed_source(source: H[]): i32 { let xs: H[] = source; let ys: H[] = []; return keep(pick(E.A, xs, ys)); }
`,
		"e051-generic-returning-its-type-parameter-is-not-fresh": `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys.len(); }
function mk(): H[] { return [H { fd: 1 }]; }
function call[T](f: () => T): T { return f(); }
function borrowed_source(source: H[]): i32 { let factory = (): H[] => source; return keep(call(factory)); }
`,
		"e051-written-iife-retains-borrowed-array": `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys[0].fd; }
function id(xs: H[]): H[] { return ((): H[] => xs)(); }
function borrowed_source(source: H[]): i32 { let xs: H[] = source; return keep(id(xs)); }
`,
		"e051-an-element-that-is-a-pointer-still-borrows": `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys.len(); }
function firsts(xs: H[]): H[] {
    let out: H[] = [];
    out = out.append(xs[0]);
    return out;
}
function borrowed_source(source: H[]): i32 { return keep(firsts(source)); }
`,
	} {
		t.Run(name, func(t *testing.T) { wantE051(t, name, src+` function main(): i32 { return 0; }`) })
	}
}

func TestCallCopyCacheSeparatesGenericAndNominalArguments(t *testing.T) {
	const declarations = `struct Data { bytes: u8[] } struct Box[T] { value: T } `
	const concrete = `function loan_concrete(x: Box[Data]): Box[Data] { return x; } function consume_concrete(own x: Box[Data]): void {} function concrete(x: Box[Data]): void { consume_concrete(loan_concrete(x)); } `
	const generic = `function loan_generic[Data](x: Box[Data]): Box[Data] { return x; } function consume_generic[Data](own x: Box[Data]): void {} function generic[Data](x: Box[Data]): void { consume_generic(loan_generic(x)); } `
	for name, body := range map[string]string{"concrete first": concrete + generic, "generic first": generic + concrete} {
		t.Run(name, func(t *testing.T) {
			err := checkSource(t, declarations+body+` function main(): i32 { return 0; }`)
			errors, ok := err.(diag.Errors)
			if !ok || len(errors) != 1 || !hasCode(err, "E051") {
				t.Fatalf("want only the generic loan refusal, got %v", err)
			}
		})
	}
}
