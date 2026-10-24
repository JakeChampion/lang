package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostBuildGateX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	cases := []struct {
		name     string
		src      string
		wantDiag string
	}{
		{
			name:     "assign-mismatch-E003",
			src:      "function main(): i32 { let s: string = 5; return 0; }\n",
			wantDiag: "error[E003]",
		},
		{
			name:     "map-without-core-map-E001",
			src:      "function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 2); return m.get_or(1, 0) - 2; }\n",
			wantDiag: "error[E001]",
		},
		{
			name: "map-with-core-map-through-an-import",
			src:  "import \"std/dotenv\";\nfunction main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 2); return m.get_or(1, 0) - 2; }\n",
		},
		{
			name:     "retired-map-set-E043",
			src:      "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, i32] = map_new(8); m.set(1, 2); return m.get_or(1, 0); }\n",
			wantDiag: "error[E043]",
		},
		{
			name:     "map-unknown-method-E043",
			src:      "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, i32] = map_new(8); return m.entries().len() + m.frob(); }\n",
			wantDiag: "error[E043]",
		},
		{
			name: "map-core-map-receiver-method",
			src:  "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(1, 2); return m.entries().len() - 1; }\n",
		},
		{
			name:     "wildcard-arm-not-last-E026",
			src:      "enum O { Sm(i32), Nn }\nfunction main(): i32 { let o: O = O.Nn; match (o) { _ => { return 1; }, Nn => { return 3; } } }\n",
			wantDiag: "error[E026]",
		},
		{
			name:     "variant-covered-twice-E028",
			src:      "enum O { Sm(i32), Nn }\nfunction main(): i32 { let o: O = O.Sm(1); match (o) { Sm(a) => { return a; }, Sm(b) => { return b; }, Nn => { return 3; } } }\n",
			wantDiag: "error[E028]",
		},
		{
			name:     "const-not-a-constant",
			src:      "function g(): i32 { return 1; }\nconst E = g();\nfunction main(): i32 { return E; }\n",
			wantDiag: "const E: expression is not a constant",
		},
		{
			name: "const-composite",
			src:  "const XS: i32[] = [1, 2];\nconst T = (3, \"a\");\nfunction main(): i32 { return XS[1] + T.0; }\n",
		},
		{
			name:     "const-composite-holding-a-call",
			src:      "function g(): i32 { return 1; }\nconst E: i32[] = [g()];\nfunction main(): i32 { return E[0]; }\n",
			wantDiag: "const E: expression is not a constant",
		},
		{
			name: "const-without-a-type",
			src:  "const W = 9;\nconst A = W * 2;\nfunction main(): i32 { return A; }\n",
		},
		{
			name:     "field-assign-E048",
			src:      "struct P { x: i32 }\nfunction main(): i32 { let p: P = P { x: 1 }; p.x = 5; return p.x; }\n",
			wantDiag: "error[E048]",
		},
		{
			name:     "call-too-few-args-E004",
			src:      "function two(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return two(1); }\n",
			wantDiag: "error[E004]",
		},
		{
			name:     "fn-typed-param-shadows-module-fn-compiles",
			src:      "function apply(f: (i32) => i32, v: i32): i32 { return f(v); }\nfunction f(): i32 { return 1; }\nfunction main(): i32 { return apply((x: i32) => x + 1, 1) + f(); }\n",
			wantDiag: "",
		},
		{
			name:     "array-builtin-arity-E004",
			src:      "function main(): i32 { let a: i32[] = [1, 2]; return a.len(3); }\n",
			wantDiag: "error[E004]",
		},
		{
			name:     "array-builtins-at-correct-arity-compile",
			src:      "function main(): i32 { let a: i32[] = [1, 2]; let b: i32[] = a.append(3); let c: i32[] = b.with(0, 9); return c.len(); }\n",
			wantDiag: "",
		},
		{
			name:     "keyword-fn-name-P001",
			src:      "struct B { items: i32[] }\nfunction use(own p: B): i32 { return p.items.len(); }\nfunction main(): i32 { let a: B = B { items: [] }; return use(a); }\n",
			wantDiag: "error[P001]",
		},
		{
			name:     "keyword-struct-name-P001",
			src:      "struct use { x: i32 }\nfunction main(): i32 { return 0; }\n",
			wantDiag: "error[P001]",
		},
		{
			name:     "non-keyword-fn-name-compiles",
			src:      "function consume(n: i32): i32 { return n + 1; }\nfunction main(): i32 { return consume(1); }\n",
			wantDiag: "",
		},
		{
			name:     "struct-type-param-bound-P001",
			src:      "trait B { function b(self: Self): i32; }\nstruct W[T: B] { v: T }\nfunction main(): i32 { return 0; }\n",
			wantDiag: "error[P001]: at top level: struct W: a bound on type parameter T belongs on an impl",
		},
		{
			name:     "enum-type-param-bound-P001",
			src:      "trait B { function b(self: Self): i32; }\nenum E[T: B] { A(T), N }\nfunction main(): i32 { return 0; }\n",
			wantDiag: "error[P001]: at top level: enum E: a bound on type parameter T belongs on an impl",
		},
		{
			name:     "union-alias-type-param-bound-P001",
			src:      "trait B { function b(self: Self): i32; }\nstruct L[T] { v: T }\nstruct Q { n: i32 }\ntype V[T: B] = L[T] | Q;\nfunction main(): i32 { return 0; }\n",
			wantDiag: "error[P001]: at top level: type V: a bound on type parameter T belongs on an impl",
		},
		{
			name:     "type-tuple-trailing-comma",
			src:      "function main(): i32 { let t: (i32,) = (1,); return 0; }\n",
			wantDiag: "error[P001]",
		},
		{
			name:     "type-tuple-empty-element",
			src:      "function main(): i32 { let t: (i32, , i32) = (1, 2); return 0; }\n",
			wantDiag: "error[P001]",
		},
		{
			name:     "type-annotation-missing",
			src:      "function main(): i32 { let x: = 3; return x; }\n",
			wantDiag: "error[P001]",
		},
		{
			name:     "type-param-tuple-trailing-comma",
			src:      "function f(t: (i32,)): i32 { return 0; }\nfunction main(): i32 { return 0; }\n",
			wantDiag: "error[P001]",
		},
		{
			name:     "type-fn-params-trailing-comma",
			src:      "function main(): i32 { let f: (i32,) => i32 = (a: i32) => a; return 0; }\n",
			wantDiag: "error[P001]",
		},
		{
			name:     "type-literal",
			src:      "function main(): i32 { let x: 5 = 3; return 0; }\n",
			wantDiag: "error[P001]",
		},
		{
			name:     "type-tuple-and-fn-compile",
			src:      "function main(): i32 { let t: (i32, string) = (1, \"a\"); let f: (i32) => i32 = (a: i32) => a; let g: () => i32 = () => 2; return t.0 + f(1) + g(); }\n",
			wantDiag: "",
		},
		{
			name:     "string-builtin-arity-E004",
			src:      "function main(): i32 { let s: string = \"abc\"; return s.len(1); }\n",
			wantDiag: "error[E004]",
		},
		{
			name:     "free-builtin-arity-E004",
			src:      "function main(): i32 { print(\"a\", \"b\"); return 0; }\n",
			wantDiag: "error[E004]",
		},
		{
			name:     "closure-call-arity-E004",
			src:      "function main(): i32 { let g = (a: i32, b: i32): i32 => { return a + b; }; return g(1); }\n",
			wantDiag: "error[E004]",
		},
		{
			name:     "fn-value-call-arity-E004",
			src:      "function dbl(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { let g = dbl; return g(1); }\n",
			wantDiag: "error[E004]",
		},
		{
			name:     "fn-typed-param-call-arity-E004",
			src:      "function ap(f: (i32, i32) => i32): i32 { return f(1); }\nfunction main(): i32 { return ap((a: i32, b: i32): i32 => a + b); }\n",
			wantDiag: "error[E004]",
		},
		{
			name:     "closure-and-fn-value-at-correct-arity-compile",
			src:      "function dbl(a: i32, b: i32): i32 { return a + b; }\nfunction z(f: () => i32): i32 { let q = f; return q(); }\nfunction main(): i32 { let g = (a: i32, b: i32): i32 => { return a + b; }; let h = dbl; return g(1, 2) + h(3, 4) + z((): i32 => 0); }\n",
			wantDiag: "",
		},
		{
			name:     "fn-typed-struct-field-rebind-has-no-arity-to-check",
			src:      "struct H { f: (i32) => i32 }\nfunction apply_h(h: H): i32 { let g = h.f; return g(2); }\nfunction inc(x: i32): i32 { return x + 1; }\nfunction main(): i32 { return apply_h(H { f: inc }); }\n",
			wantDiag: "",
		},
		{
			name:     "string-and-free-builtins-at-correct-arity-compile",
			src:      "function main(): i32 { print(\"a\"); let s: string = \"abc\"; return s.len() + s.as_bytes().len(); }\n",
			wantDiag: "",
		},
		{
			name:     "i64-program-compiles",
			src:      "import \"std/i64\";\nfunction main(): i32 { let a: i64 = 9i64; let b: i64 = 3i64; return (a / b) as i32; }\n",
			wantDiag: "",
		},
		{
			name:     "unimported-i32-method-E043",
			src:      "function main(): i32 { let t: string = 7.to_string(); return t.len(); }\n",
			wantDiag: "error[E043]",
		},
		{
			name:     "unimported-array-method-E043",
			src:      "function main(): i32 { let xs: string[] = [\"ab\", \"cd\"]; let t: string = xs.join(\",\"); return t.len(); }\n",
			wantDiag: "error[E043]",
		},
		{
			name:     "unimported-string-method-E043",
			src:      "function main(): i32 { let s: string = \"aXb\"; let t: string = s.replace(\"X\", \"Y\"); return t.len(); }\n",
			wantDiag: "error[E043]",
		},
		{
			name:     "imported-stdlib-methods-compile",
			src:      "import \"std/i32\";\nimport \"std/array\";\nimport \"std/string\";\nfunction main(): i32 { let t: string = 7.to_string(); let xs: string[] = [\"ab\", \"cd\"]; let j: string = xs.join(\",\"); let r: string = \"aXb\".replace(\"X\", \"Y\"); return t.len() + j.len() + r.len(); }\n",
			wantDiag: "",
		},
		{
			name:     "type-param-name-collision-compiles",
			src:      "import \"std/array\";\nstruct T { z: i32 }\nstruct K { a: i32 }\nfunction main(): i32 { let xs: i32[] = [1, 2, 3]; let t: T = T { z: xs.sum() }; let k: K = K { a: xs.len() }; return t.z + k.a; }\n",
			wantDiag: "",
		},
		{
			name:     "unknown-stdlib-type-not-reported-E064",
			src:      "import \"std/io\";\nfunction main(): i32 { let r: i32 = 0; return r; }\n",
			wantDiag: "",
		},
		{
			name:     "builtin-struct-decls-registered-time",
			src:      "import \"std/time\";\nfunction main(): i32 { return 0; }\n",
			wantDiag: "",
		},
		{
			name:     "builtin-struct-decls-registered-platform",
			src:      "import \"std/platform\";\nfunction main(): i32 { return 0; }\n",
			wantDiag: "",
		},
		{
			name:     "array-equality-E041",
			src:      "function main(): i32 { let xs: i32[] = [1, 2, 3]; let ys: i32[] = [1, 2, 3]; if (xs == ys) { return 1; } return 0; }\n",
			wantDiag: "error[E041]",
		},
		{
			name:     "array-element-equality-compiles",
			src:      "function main(): i32 { let xs: i32[] = [1, 2, 3]; let ys: i32[] = [1, 2, 3]; if (xs[0] == ys[0]) { return 1; } return 0; }\n",
			wantDiag: "",
		},
		{
			name:     "discard-read-back-E001",
			src:      "function pair(): (i32, i32) { return (1, 2); }\nfunction main(): i32 { let (a, _) = pair(); return _; }\n",
			wantDiag: "error[E001]",
		},
		{
			name:     "owned-arg-used-twice-E051",
			src:      "struct B { items: i32[] }\nfunction consume(own p: B): i32 { return p.items.len(); }\nfunction main(): i32 { let a: B = B { items: [1] }; let n: i32 = consume(a); return n + consume(a); }\n",
			wantDiag: "error[E051]",
		},
		{
			name:     "missing-return-E052",
			src:      "function f(n: i32): i32 { if (n > 0) { return 1; } }\nfunction main(): i32 { return f(1); }\n",
			wantDiag: "error[E052]",
		},
		{
			name:     "tuple-match-exhausts-so-no-E052",
			src:      "function f(t: (i32, i32)): i32 { match (t) { (0, b) => { return b; }, (a, _) => { return a; } } }\nfunction main(): i32 { return f((0, 7)); }\n",
			wantDiag: "",
		},
		{
			name:     "repeated-discard-compiles",
			src:      "function constant(_: i32, _: string): i32 { return 7; }\nfunction main(): i32 { let _ = 99; let _ = 98; return constant(1, \"a\"); }\n",
			wantDiag: "",
		},
		{
			name:     "discard-var-read-back-E001",
			src:      "function main(): i32 { let _ = 99; return _; }\n",
			wantDiag: "error[E001]",
		},
		{
			name:     "discard-param-read-back-E001",
			src:      "function f(_: i32): i32 { return _; }\nfunction main(): i32 { return f(1); }\n",
			wantDiag: "error[E001]",
		},
		{
			name:     "discard-for-header-read-back-E001",
			src:      "function main(): i32 { let xs: (i32, i32)[] = [(1, 2)]; for (a, _) in xs { return _; } return 0; }\n",
			wantDiag: "error[E001]",
		},
		{
			name:     "for-nested-tuple-pattern-compiles",
			src:      "function main(): i32 { let deep: ((i32, i32), string)[] = [((2, 3), \"xy\")]; let s: i32 = 0; for ((a, b), c) in deep { s = s + a * b + c.len(); } return s; }\n",
			wantDiag: "",
		},
		{
			name:     "at-binder-compiles",
			src:      "enum One { Only(string) }\nfunction whole(o: One): i32 { return 1; }\nfunction main(): i32 { let o: One = Only(\"e\"); match (o) { w @ Only(v) => { return whole(w) + v.len(); } } }\n",
			wantDiag: "",
		},
		{
			name:     "if-expr-in-generic-compiles",
			src:      "function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }\nfunction main(): i32 { return pick(true, 1, 2); }\n",
			wantDiag: "",
		},
		{
			name:     "enum-associated-fn-compiles",
			src:      "trait Empty { function empty(): Self; }\nenum Opt { Nothing, Just(i32) }\nimpl Empty for Opt { function empty(): Self { return Nothing; } }\nfunction main(): i32 { let o: Opt = Opt.empty(); match (o) { Nothing => { return 0; }, Just(n) => { return n; } } }\n",
			wantDiag: "",
		},
		{
			name:     "derived-associated-fn-compiles",
			src:      "import \"std/json\";\n@derive(json.FromJson) struct User { id: i32, name: string }\nfunction main(): i32 { match (User.from_json(\"{\\\"id\\\":7,\\\"name\\\":\\\"g\\\"}\")) { Ok(u) => { return u.id; }, Err(e) => { return 1; } } }\n",
			wantDiag: "",
		},
		{
			name:     "derived-enum-from-json-compiles",
			src:      "import \"std/json\";\n@derive(json.FromJson)\nenum Shape { Empty, Circle(f64), Rect { w: i32, h: i32 }, Tags(string[]), Maybe(Option[i32]) }\nfunction main(): i32 { match (Shape.from_json(\"{\\\"Rect\\\":{\\\"w\\\":2,\\\"h\\\":3}}\")) { Ok(_) => { return 0; }, Err(_) => { return 1; } } }\n",
			wantDiag: "",
		},
		{
			name:     "derived-enum-from-json-nested-array-E021",
			src:      "import \"std/json\";\n@derive(json.FromJson)\nenum Grid { Rows(i32[][]) }\nfunction main(): i32 { return 0; }\n",
			wantDiag: "error[E021]",
		},
		{
			name:     "uncoded-partial-checker-hint-does-not-gate",
			src:      "enum W { Wrap(i32), Er2 }\nfunction main(): i32 { let w: W = W.Er2; match (w) { Wrap(Er2) => { return 1; }, Er2 => { return 2; } } }\n",
			wantDiag: "",
		},
		{
			name:     "slice-payload-is-a-view-E003",
			src:      "function main(): i32 { let s: string = \"abcdef\"; match (s[0:3]) { Some(v) => { let w: string = v; return w.len(); }, None => { return 0; } } }\n",
			wantDiag: "error[E003]",
		},
		{
			name:     "slice-payload-at-its-own-type-compiles",
			src:      "function main(): i32 { let s: string = \"abcdef\"; match (s[0:3]) { Some(v) => { let w: str = v; return w.len(); }, None => { return 0; } } }\n",
			wantDiag: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			progDir := t.TempDir()
			prog := filepath.Join(progDir, "prog.fern")
			if err := os.WriteFile(prog, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write prog: %v", err)
			}
			out := filepath.Join(progDir, "prog.bin")
			cmd := exec.Command(fernBin, "-target", "x86-64-linux", "-o", out, prog, stdlibRoot)
			stderr, _ := cmd.CombinedOutput()
			code := cmd.ProcessState.ExitCode()

			if tc.wantDiag == "" {
				if code != 0 {
					t.Fatalf("valid program rejected by the build gate: exit=%d\n%s", code, stderr)
				}
				return
			}
			if code == 0 {
				t.Fatalf("ill-typed program compiled clean (exit 0); wanted %s.\n"+
					"The compile path is not gating on this code — see #6961.", tc.wantDiag)
			}
			if !strings.Contains(string(stderr), tc.wantDiag) {
				t.Errorf("exit=%d but stderr missing %q\ngot: %s", code, tc.wantDiag, stderr)
			}
		})
	}
}

func TestSelfHostBuildGateMatchesCheckX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	srcs := []string{
		"function main(): i32 { let s: string = 5; return 0; }\n",
		"enum O { Sm(i32), Nn }\nfunction main(): i32 { let o: O = O.Sm(1); match (o) { Sm(a) => { return a; }, Sm(b) => { return b; }, Nn => { return 3; } } }\n",
		"struct P { x: i32 }\nfunction main(): i32 { let p: P = P { x: 1 }; p.x = 5; return p.x; }\n",
		"enum O { Sm(i32), Nn }\nfunction main(): i32 { let o: O = O.Nn; match (o) { _ => { return 1; }, Nn => { return 3; } } }\n",
		"function two(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return two(1); }\n",
		"function g(): i32 { return 1; }\nconst E = g();\nfunction main(): i32 { return E; }\n",
	}
	for i, src := range srcs {
		progDir := t.TempDir()
		prog := filepath.Join(progDir, "prog.fern")
		if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
			t.Fatalf("write prog: %v", err)
		}
		checkCmd := exec.Command(fernBin, "-check", prog, stdlibRoot)
		checkOut, _ := checkCmd.CombinedOutput()
		if checkCmd.ProcessState.ExitCode() == 0 {
			t.Errorf("case %d: -check accepted a program the corpus says it rejects:\n%s", i, src)
			continue
		}
		buildCmd := exec.Command(fernBin, "-target", "x86-64-linux",
			"-o", filepath.Join(progDir, "prog.bin"), prog, stdlibRoot)
		buildOut, _ := buildCmd.CombinedOutput()
		if buildCmd.ProcessState.ExitCode() == 0 {
			t.Errorf("case %d: -check rejected but -target built it (#6961).\nsrc: %s-check said: %s\n-target said: %s",
				i, src, checkOut, buildOut)
		}
	}
}

type formerlyExemptCode struct {
	code string
	src  string
}

func TestSelfHostFormerlyExemptCodesGateX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	rows := []formerlyExemptCode{
		{code: "E001", src: "function main(): i32 { let a: i32 = zz; return a; }\n"},
		{code: "E009", src: "function main(): i32 { let s: string = \"x\"; if (s && true) { return 1; } return 0; }\n"},
		{code: "E013", src: "function main(): i32 { let a: i32 = 1; let a: i32 = 2; return a; }\n"},
		{code: "E018", src: "@noinline function f(a: i32, a: i32): i32 { return a; }\nfunction main(): i32 { return f(1, 2); }\n"},
		{code: "E019", src: "struct Box[T] { v: T }\nfunction f(b: Box[i32, string]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n"},
		{code: "E021", src: "trait Greet { function hello(): i32; }\nstruct Dog {}\nimpl Greet for Dog {}\nfunction main(): i32 { return 0; }\n"},
		{code: "E024", src: "function pair(): (i32, i32) { return (1, 2); }\nfunction main(): i32 { let (a, b, c) = pair(); return a; }\n"},
		{code: "E031", src: "enum O { Aa, Bb }\nfunction main(): i32 { let o: O = O.Aa; let r = match (o) { Aa => 1, Bb => \"x\" }; return 0; }\n"},
		{code: "E034", src: "function main(): i32 { let xs: i32[] = [1, \"two\"]; return xs.len(); }\n"},
		{code: "E036", src: "enum O { Aa, Bb }\nfunction main(): i32 { let o: O = O.Cc; return 0; }\n"},
		{code: "E038", src: "function g(n: i32): i32 { return n; }\nfunction main(): i32 { return g(\"x\"); }\n"},
		{code: "E040", src: "function pick[T](a: T): T { return a; }\nfunction main(): i32 { return pick[i32, string](1); }\n"},
		{code: "E041", src: "function main(): i32 { let xs: i32[] = [1,2]; let ys: i32[] = [1,2]; if (xs == ys) { return 1; } return 0; }\n"},
		{code: "E042", src: "function main(): i32 { let n: i32 = 5; let m: i32 = n?; return m; }\n"},
		{code: "E044", src: "function nothing(): void { }\nfunction main(): i32 { let v = nothing(); let g = (): i32 => { v; return 2; }; return g(); }\n"},
		{code: "E051", src: "struct B { items: i32[] }\nfunction consume(own p: B): i32 { return p.items.len(); }\nfunction main(): i32 { let a: B = B { items: [1] }; let n: i32 = consume(a); return n + consume(a); }\n"},
		{code: "E052", src: "function f(n: i32): i32 { if (n > 0) { return 1; } }\nfunction main(): i32 { return f(1); }\n"},
		{code: "E064", src: "function f(a: Wibble): i32 { return 0; }\nfunction main(): i32 { return 0; }\n"},
	}
	if len(rows) != 18 {
		t.Fatalf("the exclusion list #8461 measured held 18 codes; matrix has %d", len(rows))
	}

	for _, row := range rows {
		t.Run(row.code, func(t *testing.T) {
			progDir := t.TempDir()
			prog := filepath.Join(progDir, "prog.fern")
			if err := os.WriteFile(prog, []byte(row.src), 0o644); err != nil {
				t.Fatalf("write prog: %v", err)
			}

			native := goCheckerCodes(t, progDir, row.src)
			if len(native) != 1 || native[0] != row.code {
				t.Fatalf("native reports %v, want exactly [%s] — the row no longer probes its code", native, row.code)
			}

			checkCmd := exec.Command(fernBin, "-check", prog, stdlibRoot)
			checkOut, _ := checkCmd.CombinedOutput()
			if checkCmd.ProcessState.ExitCode() == 0 {
				t.Fatalf("self-host -check accepted a program native rejects with %s:\n%s", row.code, checkOut)
			}
			if !strings.Contains(string(checkOut), "error["+row.code+"]") {
				t.Fatalf("self-host -check rejected but not with %s:\n%s", row.code, checkOut)
			}

			buildCmd := exec.Command(fernBin, "-target", "x86-64-linux",
				"-o", filepath.Join(progDir, "prog.bin"), prog, stdlibRoot)
			buildOut, _ := buildCmd.CombinedOutput()
			built := buildCmd.ProcessState.ExitCode() == 0
			if built {
				t.Errorf("%s: -check rejects and -target builds it anyway — the exemption is back (#8461).\n"+
					"src: %s-check said: %s", row.code, row.src, checkOut)
			}
			if !strings.Contains(string(buildOut), "error["+row.code+"]") {
				t.Errorf("%s: the build refused but did not name the code:\n%s", row.code, buildOut)
			}
		})
	}
}
