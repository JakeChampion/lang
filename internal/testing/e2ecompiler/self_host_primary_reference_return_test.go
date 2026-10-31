package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostPrimaryReferenceReturns(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct{ name, src string }{{"lifetime", primaryReferenceLifetime}, {"zero-tuple-allocations", primaryReferenceAlloc}} {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(tc.src), 0600); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stderr, code := cli.exitOfFile(t, src, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("exit %d: %s", code, stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
		})
	}
}

const primaryReferenceLifetime = `import "core/mem";
struct DropCell { p: usize }
impl mem.Drop for DropCell {
 function drop(self: Self): void { unsafe { __store_i64(self.p, __load_i64(self.p) + 1i64); } }
}
struct Bundle { xs: string[], label: string, cell: DropCell }
@noinline function triple(xs: string[], n: i32): (string[], i32, boolean) { return (xs, n, n % 2 == 0); }
@noinline function text_result(s: string, n: i32): (string, i64, boolean) { return (s, 4294967338i64 + (n as i64), n % 2 == 0); }
@noinline function literal_result(n: i32): (string, i32, boolean) { if (n % 2 == 0) { return ("", n, true); } return ("short", n, false); }
@noinline function quad(b: Bundle, n: i32): (Bundle, i64, f64, boolean) {
 if (n % 2 == 0) { return (b, n as i64, 1.5, true); }
 return (b, n as i64, 2.5, false);
}
@noinline function whole(t: (Bundle, i64, f64, boolean)): i64 { return t.1; }
function main(): i32 { unsafe {
 let counter: i64[] = __alloc_i64(1);
 let ptr = (counter as usize) + 8;
 for i in 0..100 {
  let text = "a long dynamic heap string value " + i.to_string();
  let input = [text]; let alias = input;
  let (xs, n, even) = triple(input, i);
  let (label, wide, flag) = text_result(text, i);
  let (literal, at, lit_even) = literal_result(i);
  if (literal != if (even) { "" } else { "short" } || at != i || lit_even != even) { return 99; }
  if (label != text || wide != 4294967338i64 + (i as i64) || flag != even) { return 98; }
  if (xs[0] != text || alias[0] != text || n != i || even != (i % 2 == 0)) { return 92; }
  let grown = xs.append("tail");
  if (alias.len() != 1 || grown.len() != 2) { return 93; }
  let q = quad(Bundle { xs: grown, label: text, cell: DropCell { p: ptr } }, i);
  if (q.0.xs[0] != text || q.0.label != text || q.1 != (i as i64) || q.3 != even) { return 94; }
  if (q.2 != if (even) { 1.5 } else { 2.5 }) { return 95; }
  if (whole((q.0, q.1, q.2, q.3)) != (i as i64)) { return 96; }
 }
 if (counter[0] != 100i64 || counter.len() != 1) { return 97; }
 return 0;
} }
`
const primaryReferenceAlloc = `@noinline function first(xs: i64[], n: i32): (i64[], i32, boolean) { return (xs, n, n % 2 == 0); }
function main(): i32 {
 let xs: i64[] = __alloc_i64(args().len() + 1); xs = xs.with(0, 17i64); let before = __heap_alloc_count();
 for i in 0..1000 { let (a, n, even) = first(xs, i); if (a[0] != 17i64 || n != i || even != (i % 2 == 0)) { return 91; } }
 if (__heap_alloc_count() - before != 0i64) { return 92; }
 return 0;
}
`
