package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostPartialEnumInference(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct{ name, source string }{
		{"direct-result-payload-widening", `
function signed(n: i32): Result[i64, string] { return Ok(n); }
function unsigned(n: u8): Result[string, u64] { return Result.Err(n); }
function main(): i32 {
  match(signed(0 - 17)) { Ok(n) => { assert(n == -17i64); }, Err(_) => { assert(false); } }
  match(unsigned(255)) { Ok(_) => { assert(false); }, Err(n) => { assert(n == 255u64); } }
  return 0;
}`},
		{"retained-record-and-enum-dyn-templates", `import "std/i32";
trait Size { function size(self: Self): i32; }
struct Box[T] { value: T, tag: string }
impl[T] Size for Box[T] { function size(self: Self): i32 { return self.tag.len(); } }
enum Wrapped[T] { Full(Box[T]), Empty }
impl[T] Size for Wrapped[T] {
  function size(self: Self): i32 { match(self) { Full(b) => { return b.size(); }, Empty => { return 0; } } }
}
function exercise(i: i32): void {
  let b = Box { value: "payload" + i.to_string(), tag: "tag" + i.to_string() };
  let wrapped: Wrapped[string] = Full(b);
  let a: dyn Size = b; let c: dyn Size = wrapped;
  let n: Wrapped[i32] = Full(Box { value: i, tag: "n" + i.to_string() });
  let d: dyn Size = n;
  assert(a.size() == 4); assert(c.size() == 4); assert(d.size() == 2);
}
function main(): i32 { for i in 0..8 { exercise(i); } return 0; }`},
		{"derived-generic-methods", `import "core/cmp";
@derive(cmp.Display, cmp.Eq)
enum Opt[T] { Has(T), Nil }
function main(): i32 {
  let a: Opt[i32] = Has(9); let b: Opt[i32] = Nil;
  let s: Opt[string] = Has("payload"); let t: Opt[string] = Nil;
  assert(a.to_string() == "Has(9)"); assert(b.to_string() == "Nil");
  assert(s.to_string() == "Has(payload)"); assert(t.to_string() == "Nil");
  let b2: Opt[i32] = Nil; let t2: Opt[string] = Nil;
  assert(a.eq(Has(9))); assert(!a.eq(Has(10))); assert(!a.eq(b)); assert(b.eq(b2));
  assert(s.eq(Has("payload"))); assert(!s.eq(Has("other"))); assert(!s.eq(t)); assert(t.eq(t2));
  return 0;
}`},
		{"unused-derived-template", `import "core/cmp";
@derive(cmp.Display, cmp.Eq)
enum Unused[T] { Value(T), Empty }
function main(): i32 { let o = Ok(3); match(o) { Ok(v) => { assert(v == 3); }, Err(_) => { assert(false); } } return 0; }`},
		{"inferred-lambda-result", `function main(): i32 { let f = () => { return Ok(3); }; match(f()) { Ok(v) => { assert(v == 3); }, Err(_) => { assert(false); } } return 0; }`},
		{"inferred-lambda-result-join", `function exercise(flag: boolean): void { let f = () => { if (flag) { return Ok(3); } return Err("failure"); }; match(f()) { Ok(v) => { assert(flag); assert(v == 3); }, Err(e) => { assert(!flag); assert(e == "failure"); } } } function main(): i32 { exercise(true); exercise(false); return 0; }`},
		{"escaping-owned-closure", `import "std/string";
function invoke(f: () => i32): i32 { return f(); }
function exercise(i: i32): void { let o = Err("payload" + i.to_string()); let copy = o; let f = (): i32 => { match(o) { Ok(_) => { return 0; }, Err(e) => { return e.len(); } } }; assert(invoke(f) == 8); match(copy) { Ok(_) => { assert(false); }, Err(e) => { assert(e == "payload" + i.to_string()); } } }
function main(): i32 { for i in 0..8 { exercise(i); } return 0; }`},
		{"user-closure-context", `enum Choice[T, E] { Pick(T), Reject(E) }
function invoke(f: () => i64): i64 { return f(); }
function main(): i32 { let o = Choice.Pick(2147483647 + 1); let f = (): i64 => { match(o) { Pick(v) => { return v; }, Reject(_) => { return 0i64; } } }; assert(invoke(f) == 2147483648i64); return 0; }`},
		{"direct-extracted", `function make(): i64 { match (Ok(3)) { Ok(v) => { return v; }, Err(_) => { return 0i64; } } } function main(): i32 { assert(make() == 3i64); return 0; }`},
		{"same-parameter", `enum Choice[T, E] { Pair(T, T), Reject(E) } function main(): i32 { let o = Pair(1, 4294967297i64); match(o) { Pair(a, b) => { assert(a == 1i64); assert(b == 4294967297i64); }, Reject(_) => { return 1; } } return 0; }`},
		{"same-parameter-reversed", `enum Choice[T, E] { Pair(T, T), Reject(E) } function main(): i32 { let o = Pair(4294967297i64, 1); match(o) { Pair(a, b) => { assert(a == 4294967297i64); assert(b == 1i64); }, Reject(_) => { return 1; } } return 0; }`},
		{"closure-context", `function main(): i32 { let o = Ok(2147483647 + 1); let f = (): i64 => { match(o) { Ok(v) => { return v; }, Err(_) => { return 0i64; } } }; assert(f() == 2147483648i64); return 0; }`},
		{"qualified-collision", `enum Left[T, E] { Pick(T), Reject(E) } enum Right[T, E] { Pick(T), Reject(E) } function make(): Right[i64, string] { let o = Right.Pick(2147483647 + 1); return o; } function main(): i32 { match(make()) { Pick(v) => { assert(v == 2147483648i64); }, Reject(_) => { return 1; } } return 0; }`},
		{"narrow-literal-context", `function make(): Result[u8, string] { let o = Ok(255); return o; }
function main(): i32 { match (make()) { Ok(v) => { assert(v == 255u8); }, Err(_) => { return 1; } } return 0; }`},
		{"unsigned-literal-context", `function make(): Result[u32, string] { let o = Ok(3); return o; }
function main(): i32 { match (make()) { Ok(v) => { assert(v == 3u32); }, Err(_) => { return 1; } } return 0; }`},
		{"wide-literal-default", `function main(): i32 { let o = Ok(4294967297); match (o) { Ok(v) => { assert(v == 4294967297i64); }, Err(_) => { return 1; } } return 0; }`},
		{"literal-arithmetic-context", `function make(): Result[i64, string] { let o = Ok(2147483647 + 1); return o; }
function main(): i32 { match (make()) { Ok(v) => { assert(v == 2147483648i64); }, Err(_) => { return 1; } } return 0; }`},
		{"extracted-literal-context", `function make(): i64 { let o = Ok(3); match (o) { Ok(v) => { return v; }, Err(_) => { return 0i64; } } }
function main(): i32 { assert(make() == 3i64); return 0; }`},
		{"literal-context", `function make(): Result[i64, string] { let o = Ok(3); return o; }
function main(): i32 { match (make()) { Ok(v) => { assert(v == 3i64); }, Err(_) => { return 1; } } return 0; }`},
		{"literal-width-join", `function exercise(flag: boolean): void { let o = if (flag) { Ok(1) } else { Ok(4294967297i64) }; match (o) { Ok(v) => { assert(v == if (flag) { 1i64 } else { 4294967297i64 }); }, Err(_) => { assert(false); } } }
function main(): i32 { exercise(true); exercise(false); return 0; }`},
		{"user-literal-context", `enum Choice[T, E] { Pick(T), Reject(E) }
function make(): Choice[i64, string] { let o = Pick(-3); return o; }
function main(): i32 { match (make()) { Pick(v) => { assert(v == -3i64); }, Reject(_) => { return 1; } } return 0; }`},
		{"shared-literal-context", `function wide(o: Result[i64, string]): i64 { match (o) { Ok(v) => { return v; }, Err(_) => { return 0i64; } } }
function main(): i32 { let o = Ok(-3); let copy = o; assert(wide(o) == -3i64); match (copy) { Ok(v) => { assert(v == -3); }, Err(_) => { return 1; } } return 0; }`},
		{"wide-ok", `function main(): i32 { let o = Ok(4294967297i64); match (o) { Ok(v) => { assert(v + 1i64 == 4294967298i64); }, Err(_) => { return 1; } } return 0; }`},
		{"float-ok", `function main(): i32 { match (Ok(1.5 as f32)) { Ok(v) => { assert(f32_bits(v) == 1069547520); }, Err(_) => { return 1; } } return 0; }`},
		{"later-ok-context", `function make(): Result[i64, string] { let o = Ok(4294967297i64); return o; }
function main(): i32 { match (make()) { Ok(v) => { assert(v == 4294967297i64); }, Err(_) => { return 1; } } return 0; }`},
		{"later-error-context", `function make(): Result[i64, string] { let o = Err("payload" + "!"); return o; }
function main(): i32 { match (make()) { Ok(_) => { return 1; }, Err(e) => { assert(e == "payload!"); } } return 0; }`},
		{"shared-context", `import "std/string";
function length(o: Result[i64, string]): i32 { match (o) { Ok(_) => { return 0; }, Err(e) => { return e.len(); } } }
function exercise(i: i32): void { let o = Err("payload" + i.to_string()); let copy = o; assert(length(o) == 8); match (copy) { Ok(_) => { assert(false); }, Err(e) => { assert(e == "payload" + i.to_string()); } } }
function main(): i32 { for i in 0..8 { exercise(i); } return 0; }`},
		{"branch-join", `function exercise(flag: boolean): void { let o = if (flag) { Ok(4294967297i64) } else { Err("payload" + "!") }; match (o) { Ok(v) => { assert(flag); assert(v == 4294967297i64); }, Err(e) => { assert(!flag); assert(e == "payload!"); } } }
function main(): i32 { exercise(true); exercise(false); return 0; }`},
		{"owned-error", `import "std/string";
function exercise(i: i32): void { let o = Err("payload" + i.to_string()); match (o) { Ok(_) => { assert(false); }, Err(e) => { assert(e == "payload" + i.to_string()); assert(e.len() > 7); } } }
function main(): i32 { for i in 0..8 { exercise(i); } return 0; }`},
		{"user-enum", `enum Choice[T, E] { Pick(T), Reject(E) }
function main(): i32 { let o = Pick(4294967297i64); match (o) { Pick(v) => { assert(v + 1i64 == 4294967298i64); }, Reject(_) => { return 1; } } return 0; }`},
		{"user-owned", `import "std/string";
enum Choice[T, E] { Pick(T), Reject(E) }
function exercise(i: i32): void { let o = Choice.Reject("payload" + i.to_string()); let copy = o; match (o) { Pick(_) => { assert(false); }, Reject(e) => { assert(e.len() == 8); } } match (copy) { Pick(_) => { assert(false); }, Reject(e) => { assert(e == "payload" + i.to_string()); } } }
function main(): i32 { for i in 0..8 { exercise(i); } return 0; }`},
		{"user-two-instances", `enum Choice[T, E] { Pick(T), Reject(E) }
function main(): i32 { let a = Pick(4294967297i64); let b = Pick(1.5 as f32); match (a) { Pick(v) => { assert(v == 4294967297i64); }, Reject(_) => { return 1; } } match (b) { Pick(v) => { assert(f32_bits(v) == 1069547520); }, Reject(_) => { return 2; } } return 0; }`},
		{"user-context", `enum Choice[T, E] { Pick(T), Reject(E) }
function make(): Choice[i64, string] { let o = Pick(4294967297i64); return o; }
function main(): i32 { match (make()) { Pick(v) => { assert(v == 4294967297i64); }, Reject(_) => { return 1; } } return 0; }`},
		{"user-shared-context", `import "std/string";
enum Choice[T, E] { Pick(T), Reject(E) }
function length(o: Choice[i64, string]): i32 { match (o) { Pick(_) => { return 0; }, Reject(e) => { return e.len(); } } }
function exercise(i: i32): void { let o = Choice.Reject("payload" + i.to_string()); let copy = o; assert(length(o) == 8); match (copy) { Pick(_) => { assert(false); }, Reject(e) => { assert(e == "payload" + i.to_string()); } } }
function main(): i32 { for i in 0..8 { exercise(i); } return 0; }`},
		{"generic-join", `function first[T](a: T, b: T): T { return a; }
function main(): i32 { let o = first(Ok(4294967297i64), Err("payload" + "!")); match (o) { Ok(v) => { assert(v == 4294967297i64); }, Err(_) => { return 1; } } return 0; }`},
	} {
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(tc.name+"/"+target, func(t *testing.T) {
				stderr, code := cli.exitOf(t, tc.source, target, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("exit %d: %s", code, stderr)
				}
				if tc.name == "owned-error" || tc.name == "shared-context" || tc.name == "user-owned" || tc.name == "user-shared-context" {
					assertBalancedCensus(t, stderr)
				} else {
					// Scalar variants may be completely eliminated. A census
					// must still be present and balanced, including at zero.
					allocs, frees, live := leakSummaryOf(t, tc.name, stderr)
					if allocs != frees || live != 0 {
						t.Fatalf("unbalanced census: allocs=%d frees=%d live=%d", allocs, frees, live)
					}
				}
			})
		}
	}
}

// Lifted partial spellings must retain absent enum arguments even when the
// receiving scope also substitutes a declaration's type variables.
func TestSelfHostPartialEnumSubstitution(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "checker.fern")
	source := `import "./checker";
import "./lexer";
import "./parser";
import "./typeinfo";
function partial(t: typeinfo.Type): boolean {
  if let typeinfo.TypeUnion(u) = t {
    return u.args.len() == 2 && checker.unbound_enum_argument(u.args[1]) && !checker.unbound_enum_argument(u.args[0]);
  }
  return false;
}
function main(): i32 {
  let mod = parser.parse_module(lexer.tokenize("trait Mark { function mark(self: Self): i32; } struct Box[T] { value: T } enum Choice[T, E] { Pick(T), Reject(E) } function f[T: Mark](x: T): T { return x; }"));
  let scope = checker.module_scopes(mod)[0];
  assert(scope.tparams.len() == 1);
  assert(partial(checker.spelled_type(scope, "Result[T, $unbound]")));
  assert(partial(checker.spelled_type(scope, "Choice[T, $unbound]")));
  let nested = checker.spelled_type(scope, "Result[Result[T, $unbound], $unbound]");
  if let typeinfo.TypeUnion(u) = nested { assert(partial(u.args[0])); assert(checker.unbound_enum_argument(u.args[1])); } else { return 1; }
  let array = checker.spelled_type(scope, "Result[T, $unbound][]");
  if let typeinfo.TypeArray(a) = array { assert(partial(a.elem)); } else { return 2; }
  // The internal marker is only valid in enum argument positions.
  assert(!checker.unbound_enum_argument(checker.spelled_type(scope, "$unbound")));
  let bad = checker.spelled_type(scope, "Box[$unbound]");
  if let typeinfo.TypeStruct(b) = bad { assert(!checker.unbound_enum_argument(b.args[0])); } else { return 3; }
  print("partial substitution preserved");
  return 0;
}`
	if err := os.WriteFile(filepath.Join(dir, "partial_substitution.fern"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "partial_substitution.fern", "partial_substitution")
	out, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil || string(out) != "partial substitution preserved\n" {
		t.Fatalf("partial substitution: %v\n%s", err, out)
	}
}

// Cloned instantiation metadata must follow the module prefix just as the
// declaration does. Two modules may declare the same local enum/variant names.
func TestSelfHostPartialEnumModuleContext(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	files := map[string]string{
		"left.fern": `pub enum Choice[T, E] { Pick(T), Reject(E) }
pub function make(): Choice[i64, string] { let o = Choice.Pick(4294967297i64); return o; }
pub function value(): i64 { match (make()) { Pick(v) => { return v; }, Reject(_) => { return 0i64; } } }`,
		"right.fern": `pub enum Choice[T, E] { Pick(T), Reject(E) }
pub function make(): Choice[i64, string] { let o = Choice.Pick(2147483647 + 1); return o; }
pub function value(): i64 { match (make()) { Pick(v) => { return v; }, Reject(_) => { return 0i64; } } }`,
		"main.fern": `import "./left"; import "./right";
function main(): i32 { assert(left.value() == 4294967297i64); assert(right.value() == 2147483648i64); return 0; }`,
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, filepath.Join(dir, "main.fern"), target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			allocs, frees, live := leakSummaryOf(t, target, stderr)
			if allocs != frees || live != 0 {
				t.Fatalf("unbalanced census: allocs=%d frees=%d live=%d", allocs, frees, live)
			}
		})
	}
}
