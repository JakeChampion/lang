package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostTwoReferenceTailReturns(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct{ name, src string }{{"lifetime", twoReferenceLifetime}, {"zero-tuple-allocations", twoReferenceAlloc}, {"zero-quad-allocations", twoReferenceQuadAlloc}} {
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

const twoReferenceLifetime = `import "core/mem";
struct TwoRefDropCell { p: usize }
impl mem.Drop for TwoRefDropCell { function drop(self: Self): void { unsafe { __store_i64(self.p, __load_i64(self.p) + 1i64); } } }
struct A { xs: string[], cell: TwoRefDropCell }
struct B { label: string, cell: TwoRefDropCell }
@noinline function records(a: A, b: B, n: i32): (A, B, boolean) { return (a, b, n % 2 == 0); }
@noinline function quad(a: A, b: B, lo: boolean, hi: boolean): (A, B, boolean, boolean) { return (a, b, lo, hi); }
@noinline function texts(a: string, b: string, lo: boolean, hi: boolean): (string, string, boolean, boolean) { return (a, b, lo, hi); }
@noinline function wide(a: string[], b: string[], n: i64): (string[], string[], i64) { return (a, b, n + 4294967338i64); }
@noinline function floating(a: string[], b: string[], n: i32): (string[], string[], f64) { return (a, b, if (n % 2 == 0) { 1.5 } else { 2.5 }); }
function main(): i32 { unsafe {
 let counter: i64[] = __alloc_i64(1); let ptr = (counter as usize) + 8;
 for i in 0..100 {
  let s = "a dynamic string with heap storage " + i.to_string();
  let a = A { xs: [s], cell: TwoRefDropCell { p: ptr } }; let keep = a;
  let b = B { label: s, cell: TwoRefDropCell { p: ptr } };
  let (x, y, even) = records(a, b, i);
  if (x.xs[0] != s || keep.xs[0] != s || y.label != s || even != (i % 2 == 0)) { return 91; }
  let (qx, qy, low, high) = quad(x, y, even, i % 3 == 0);
  if (qx.xs[0] != s || qy.label != s || low != even || high != (i % 3 == 0)) { return 96; }
  let literal = if (even) { "" } else { "tiny" };
  let (ta, tb, tl, th) = texts(s, literal, even, i % 3 == 0);
  if (ta != s || tb != literal || tl != even || th != (i % 3 == 0)) { return 97; }
  let (u, v, n) = wide(x.xs, x.xs, i as i64);
  if (u[0] != s || v[0] != s || n != 4294967338i64 + (i as i64)) { return 92; }
  let (z, w, f) = floating(u, v, i);
  if (z[0] != s || w[0] != s || f != if (even) { 1.5 } else { 2.5 }) { return 93; }
  let grown = z.append("tail"); if (w.len() != 1 || grown.len() != 2) { return 94; }
 }
 if (counter[0] != 200i64) { return 95; }
 return 0;
} }
`
const twoReferenceAlloc = `@noinline function pair(a: i64[], b: i64[], n: i32): (i64[], i64[], boolean) { return (a, b, n % 2 == 0); }
function main(): i32 {
 let a = __alloc_i64(args().len() + 1); a = a.with(0, 17i64);
 let b = __alloc_i64(args().len() + 1); b = b.with(0, 23i64);
 let keep_a = a; let keep_b = b; let before = __heap_alloc_count();
 for i in 0..1000 { let (x, y, even) = pair(a, b, i); if (x[0] != 17i64 || y[0] != 23i64 || even != (i % 2 == 0)) { return 91; } }
 if (__heap_alloc_count() - before != 0i64 || keep_a[0] != 17i64 || keep_b[0] != 23i64) { return 92; }
 return 0;
}
`

const twoReferenceQuadAlloc = `@noinline function quad(a: i64[], b: i64[], lo: boolean, hi: boolean): (i64[], i64[], boolean, boolean) { return (a, b, lo, hi); }
function main(): i32 {
 let a = __alloc_i64(args().len() + 1); a = a.with(0, 17i64);
 let b = __alloc_i64(args().len() + 1); b = b.with(0, 23i64);
 let keep_a = a; let keep_b = b; let before = __heap_alloc_count();
 for i in 0..1000 { let (x, y, even, high) = quad(a, b, i % 2 == 0, i % 3 == 0); if (x[0] != 17i64 || y[0] != 23i64 || even != (i % 2 == 0) || high != (i % 3 == 0)) { return 91; } }
 if (__heap_alloc_count() - before != 0i64 || keep_a[0] != 17i64 || keep_b[0] != 23i64) { return 92; }
 return 0;
}
`
