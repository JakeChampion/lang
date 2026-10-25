package e2ecompiler

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestSelfHostCheckerCodeSequenceX86_64(t *testing.T) {
	checkerBin, runner, _ := buildCheckerCodesBin(t)

	cases := []struct {
		name    string
		src     string
		want    string
		withMsg bool
	}{
		{"for-over-struct", "struct Box { k: i32 }\nfunction main(): i32 { let b: Box = Box { k: 1 }; for it in b { let k: i32 = it.k; } return 0; }\n", "E083(cannot iterate over a value of type Box: `for` walks an array, a string, a Map, a range, an iter.Iterator, or a value whose iter() returns one)", true},
		{"two-leaks", "@must_consume\nstruct Ticket { id: i32 }\nfunction sink(t: Ticket): Ticket { return t; }\nfunction f(): void { let a: Ticket = Ticket { id: 1 }; let b: Ticket = Ticket { id: 2 }; }\nfunction main(): i32 { return 0; }\n", "E067,E067", false},
		{"leak-in-each-of-two-fns", "@must_consume\nstruct Ticket { id: i32 }\nfunction sink(t: Ticket): Ticket { return t; }\nfunction f(): void { let a: Ticket = Ticket { id: 1 }; }\nfunction g(): void { let b: Ticket = Ticket { id: 2 }; }\nfunction main(): i32 { return 0; }\n", "E067,E067", false},
		{"nested-lambda-capture-assign", "function main(): i32 { let s: string = \"x\"; let f = (): i32 => { let g = (): i32 => { s = \"y\"; return 0; }; return g(); }; return f(); }\n", "E049", false},
		{"nested-lambda-both-assign", "function main(): i32 { let s: string = \"x\"; let f = (): i32 => { s = \"a\"; let g = (): i32 => { s = \"y\"; return 0; }; return g(); }; return f(); }\n", "E049,E049", false},
		{"sibling-lambdas-capture-assign", "function main(): i32 { let s: string = \"x\"; let f = (): i32 => { s = \"a\"; return 0; }; let g = (): i32 => { s = \"b\"; return 0; }; return f() + g(); }\n", "E049,E049", false},
		{"two-ambiguous-variant-refs", "enum A { Red, Blue }\nenum B { Red, Green }\nfunction main(): i32 { let x = Red; let y = Red; return 0; }\n", "E036,E036", false},
		{"lambda-shadows-variant-name", "enum A { Red, Blue }\nenum B { Red, Green }\nfunction main(): i32 { let f = (): i32 => { let Red: i32 = 1; return Red; }; return 0; }\n", "", false},
		{"lambda-does-not-shadow", "enum A { Red, Blue }\nenum B { Red, Green }\nfunction main(): i32 { let f = (): i32 => { let z = Red; return 0; }; return 0; }\n", "E036", false},
		{"struct-lit-unknown-and-missing", "struct P { x: i32, y: i32 }\nfunction main(): i32 { let p: P = P { x: 1, zz: 2 }; return 0; }\n", "E043,E005", false},
		{"struct-lit-nested-bad-fields", "struct In { a: i32 }\nstruct Out { x: In }\nfunction main(): i32 { let o: Out = Out { bad2: In { bad1: 1 } }; return 0; }\n", "E043,E005,E043,E005", false},
		{"struct-lit-two-siblings-missing", "struct P { x: i32, y: i32 }\nfunction main(): i32 { let a: P = P { x: 1 }; let b: P = P { x: 2 }; return 0; }\n", "E005,E005", false},
		{"two-sibling-lambdas-bad-capture", "function v(): void { return; }\nfunction main(): i32 {\n  let x = v();\n  let g = () => x;\n  let h = () => x;\n  return 0;\n}\n", "E044,E044", false},
		{"nested-lambda-bad-capture", "function v(): void { return; }\nfunction main(): i32 {\n  let x = v();\n  let g = (): i32 => { let h = () => x; return 0; };\n  return 0;\n}\n", "E044", false},
		{"two-bad-use-bindings", "function add(x: i32, y: i32): i32 { return x + y; }\nfunction main(): i32 {\n    use n <- add(1);\n    use m <- add(2);\n    return n + m;\n}\n", "E032,E038,E032,E038", false},
		{"use-inside-lambda", "function add(x: i32, y: i32): i32 { return x + y; }\nfunction main(): i32 {\n    let f = (): i32 => { use n <- add(1); return n; };\n    return f();\n}\n", "E032,E038", false},
		{"two-bad-downcasts", "trait Shape { function area(self: Self): i32; }\nstruct Circle { r: i32 }\nstruct Square { s: i32 }\nstruct Tri { t: i32 }\nimpl Shape for Circle { function area(self: Self): i32 { return self.r; } }\nfunction main(): i32 {\n    let d: dyn Shape = Circle { r: 3 };\n    match (d as? Square) { Some(sq) => { return sq.s; }, None => {} }\n    match (d as? Tri) { Some(tr) => { return tr.t; }, None => {} }\n    return 0;\n}\n", "E060,E060", false},
		{"downcast-inside-lambda", "trait Shape { function area(self: Self): i32; }\nstruct Circle { r: i32 }\nstruct Square { s: i32 }\nimpl Shape for Circle { function area(self: Self): i32 { return self.r; } }\nfunction main(): i32 {\n    let d: dyn Shape = Circle { r: 3 };\n    let f = (): i32 => {\n        match (d as? Square) { Some(sq) => { return sq.s; }, None => { return 0; } }\n    };\n    return f();\n}\n", "E060", false},
		{"dyn-local-inside-lambda", "trait Shape { function area(self: Self): i32; }\nstruct Circle { r: i32 }\nstruct Square { s: i32 }\nimpl Shape for Circle { function area(self: Self): i32 { return self.r; } }\nfunction main(): i32 {\n    let f = (): i32 => {\n        let d: dyn Shape = Circle { r: 3 };\n        match (d as? Square) { Some(sq) => { return sq.s; }, None => { return 0; } }\n    };\n    return f();\n}\n", "E060", false},
		{"structlit-base-before-fields", "trait Shape { function area(self: Self): i32; }\ntrait A { function m(self: Self): i32; }\ntrait B { function m(self: Self): i32; }\nstruct Circle { r: i32 }\nstruct Square { s: i32 }\nstruct S { v: i32 }\nimpl Shape for Circle { function area(self: Self): i32 { return self.r; } }\nimpl A for S { function m(self: Self): i32 { return self.v; } }\nimpl B for S { function m(self: Self): i32 { return self.v; } }\nstruct Box { a: i32, b: i32 }\nfunction cs(o: Option[Square]): i32 { return 0; }\nfunction boxify(n: i32): Box { return Box { a: n, b: n }; }\nfunction main(): i32 {\n    let d: dyn Shape = Circle { r: 3 };\n    let e: dyn A + B = S { v: 1 };\n    let q: Box = Box { ...boxify(e.m()), a: cs(d as? Square) };\n    return q.a;\n}\n", "E062,E060", false},
		{"ambiguous-dyn-method", "trait A { function m(self: Self): i32; }\ntrait B { function m(self: Self): i32; }\nstruct S { v: i32 }\nimpl A for S { function m(self: Self): i32 { return self.v; } }\nimpl B for S { function m(self: Self): i32 { return self.v; } }\nfunction main(): i32 {\n    let d: dyn A + B = S { v: 1 };\n    return d.m();\n}\n", "E062", false},
		{"e053-two-arrays", "fip function f(n: i32): i32 {\n    let a: i32[] = [1];\n    let b: i32[] = [2];\n    return n;\n}\nfunction main(): i32 { return f(1); }\n", "E053(`fip` function \"f\" may not allocate (array literal)),E053(`fip` function \"f\" may not allocate (array literal))", true},
		{"e053-lambda-body-array", "fip function f(n: i32): i32 {\n    let g = (): i32 => { let a: i32[] = [1]; return a.len(); };\n    return n;\n}\nfunction main(): i32 { return f(1); }\n", "E053(`fip` function \"f\" may not allocate (array literal))", true},
		{"e053-nested-named-fn-array", "fip function f(n: i32): i32 {\n    function inner(): i32 { let a: i32[] = [1]; return a.len(); }\n    return n + inner();\n}\nfunction main(): i32 { return f(1); }\n", "E053(`fip` function \"f\" may not allocate (array literal)),E053(`fip` function \"f\" may only call other `fip` functions, not \"inner\")", true},
		{"match-wildcard-not-last", "enum Opt { A, B }\nfunction main(): i32 {\n    let x = A;\n    match (x) {\n        _ => { return 1; },\n        Opt.B => { return 2; }\n    }\n}\n", "E026", false},
		{"match-wildcard-not-last-in-lambda", "enum Opt { A, B }\nfunction main(): i32 {\n    let f = (): i32 => {\n        let x = A;\n        match (x) {\n            _ => { return 1; },\n            Opt.B => { return 2; }\n        }\n    };\n    return f();\n}\n", "E026", false},
		{"match-duplicate-variant", "enum Opt { A, B }\nfunction main(): i32 {\n    let x = A;\n    match (x) {\n        Opt.A => { return 1; },\n        Opt.A => { return 2; },\n        _ => { return 3; }\n    }\n}\n", "E028", false},
		{"match-duplicate-variant-in-lambda", "enum Opt { A, B }\nfunction main(): i32 {\n    let f = (): i32 => {\n        let x = A;\n        match (x) {\n            Opt.A => { return 1; },\n            Opt.A => { return 2; },\n            _ => { return 3; }\n        }\n    };\n    return f();\n}\n", "E028", false},
		{"nested-match-arm-order", "enum Outer { P, Q }\nenum Inner { X, Y }\nfunction main(): i32 {\n    let o = P;\n    let i = X;\n    match (o) {\n        _ => {\n            match (i) {\n                Inner.X => { return 1; },\n                Inner.X => { return 2; },\n                _ => { return 3; }\n            }\n        },\n        Outer.Q => { return 4; },\n        Outer.Q => { return 5; }\n    }\n}\n", "E026(wildcard `_` arm must be last in the match),E028(variant \"X\" already covered earlier in this match),E028(variant \"Q\" already covered earlier in this match)", true},
		{"string-minus-string", "function main(): i32 {\n    let s: string = \"a\" - \"b\";\n    return 0;\n}\n", "E009(operator \"-\" requires an integer type, got string)", true},
		{"const-string-minus-string", "const S: string = \"a\" - \"b\";\nfunction main(): i32 { return 0; }\n", "E009(operator \"-\" requires an integer type, got string)", true},
		{"string-minus-bool", "function main(): i32 {\n    let s: string = \"a\" - true;\n    return 0;\n}\n", "E009(operator \"-\" requires an integer type, got string),E009(operator \"-\" requires an integer type, got boolean)", true},
		{"dup-var-in-block", "function main(): i32 {\n    let x: i32 = 1;\n    let x: i32 = 2;\n    return x;\n}\n", "E013", false},
		{"dup-var-in-lambda", "function main(): i32 {\n    let f = (): i32 => {\n        let x: i32 = 1;\n        let x: i32 = 2;\n        return x;\n    };\n    return f();\n}\n", "E013", false},
		{"i32-literal-overflow", "function main(): i32 {\n    let big: i32 = 99999999999;\n    return big;\n}\n", "E047", false},
		{"i32-literal-overflow-in-lambda", "function main(): i32 {\n    let f = (): i32 => {\n        let big: i32 = 99999999999;\n        return big;\n    };\n    return f();\n}\n", "E047", false},
		{"mixed-capture-and-leak", "@must_consume\nstruct Ticket { id: i32 }\nfunction sink(t: Ticket): Ticket { return t; }\nfunction main(): i32 { let s: string = \"x\"; let f = (): i32 => { s = \"y\"; return 0; }; let tk: Ticket = Ticket { id: 1 }; return f(); }\n", "E067,E049", false},
		{"bind-annotated-var-then-return", "function f(): string { let x: string = 5; return x; }\nfunction main(): i32 { return 0; }\n", "E003", false},
		{"bind-annotated-var-then-assign", "function main(): i32 { let x: string = 5; x = 7; return 0; }\n", "E003,E003", false},
		{"bind-annotated-var-then-call", "function g(a: string): i32 { return a.len(); }\nfunction main(): i32 { let x: string = 5; return g(x); }\n", "E003", false},
		{"bind-destructured-name-then-return", "function f(): string { let (a, b) = (1, \"x\"); return a; }\nfunction main(): i32 { return 0; }\n", "E002", false},
		{"bind-inferred-var-shadows-variant", "enum A { Red, Blue }\nenum B { Red, Green }\nfunction main(): i32 { let Red = 1; let y = Red; return 0; }\n", "", false},
		{"bind-inferred-slice-var-then-return", "function f(): [i32] { let xs: i32[] = [1, 2, 3]; let s = xs[0:2]; return s; }\nfunction main(): i32 { return 0; }\n", "E063", false},
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

			var codes []string
			for _, d := range driverDiags(out) {
				if tc.withMsg {
					codes = append(codes, d.code+"("+d.msg+")")
					continue
				}
				codes = append(codes, d.code)
			}
			got := strings.Join(codes, ",")
			if got != tc.want {
				t.Errorf("%s: diagnostic sequence = %q, want %q\n"+
					"    A change here means emission ORDER or MULTIPLICITY moved, which no\n"+
					"    other checker gate can see. Decide whether the new sequence is right,\n"+
					"    then move this row.", tc.name, got, tc.want)
			}
		})
	}
}
