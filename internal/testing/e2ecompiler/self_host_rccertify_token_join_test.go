package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Each program takes a field out of an enum box in one match arm and keeps
// the shell as a reuse token. The arms' tokens join in a phi of phis, which is
// spent by a reuse on one branch and freed behind a zero test on the other
// (#13045; nightly fuzz seed 116143, and fernsmith seed 330 with string escapes).
var tokenJoinSrcs = []struct {
	name string
	env  []string
	src  string
}{
	{"closure-payload", nil, `function id[T](x: T): T { return x; }
function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
enum E0 { __E0_V0(boolean, boolean), __E0_V1, __E0_V2(i32, (i32) => i32) }
struct G0[T] { v: T, n: i32 }
function (g: G0[T]) get(): T { return g.v; }
function (g: G0[T]) bump(k: i32): G0[T] { return G0 { ...g, n: ((g.n * 3i32) + k) }; }
enum GO0[T] { __G0_Nil, __G0_Has(T, i32) }
function (o: GO0[T]) or(d: T): T { match (o) { __G0_Has(x, _) => { return x; }, __G0_Nil => { return d; } } }
function (o: GO0[T]) count(): i32 { match (o) { __G0_Has(_, k) => { return k; }, __G0_Nil => { return 0i32; } } }
function gmk0[T](v: T, n: i32): G0[T] { return G0 { v: v, n: n }; }
function main(): i32 { let v0: E0 = pick(false, (__E0_V0((235i64 != 808i64), (861i32 != 589i32))), (__E0_V2(628i32, ((__lam_x1: i32) => __lam_x1)))); let __gen_x0: Result[i32, i32] = Ok(740i32); let __gen_a0: G0[Result[i32, i32]] = gmk0(__gen_x0, 569i32); let __gen_b0: G0[Result[i32, i32]] = __gen_a0.bump((match (v0) { __E0_V0(__dv0_0_0, __dv0_0_1) => 752i32, __E0_V1 => 55i32, __E0_V2(__dv0_2_0, __dv0_2_1) => 425i32 })); let __gen_o0: GO0[Result[i32, i32]] = __G0_Nil; if (id(true)) { __gen_o0 = __G0_Has(__gen_b0.get(), __gen_a0.n); } let __gen_e0: Result[i32, i32] = __gen_o0.or(__gen_a0.get()); let __gen_r0: i32 = 0i32; return (__gen_r0 & 255i32); }
`},
	{"string-payload", []string{"FERN_SEM_REUSE=", "FERN_SEM_INLINE="}, `enum E1 { __E1_V0(boolean, boolean), __E1_V1(string, i64), __E1_V2(boolean, i32) }
struct G0[T] { v: T, n: i32 }
function (g: G0[T]) get(): T { return g.v; }
function (g: G0[T]) bump(k: i32): G0[T] { return G0 { ...g, n: ((g.n * 3i32) + k) }; }
enum GO0[T] { __G0_Nil, __G0_Has(T, i32) }
function (o: GO0[T]) or(d: T): T { match (o) { __G0_Has(x, _) => { return x; }, __G0_Nil => { return d; } } }
function (o: GO0[T]) count(): i32 { match (o) { __G0_Has(_, k) => { return k; }, __G0_Nil => { return 0i32; } } }
function gmk0[T](v: T, n: i32): G0[T] { return G0 { v: v, n: n }; }
function main(): i32 { let __clo_c0: i32 = 278i32; let __clo_r0: i32 = 5i32; let __gen_x0: E1 = (__E1_V1("dzswv", 161i64)); let __gen_a0: G0[E1] = gmk0(__gen_x0, __clo_c0); let __gen_b0: G0[E1] = __gen_a0.bump(__clo_r0); let __gen_o0: GO0[E1] = __G0_Nil; if ((!false)) { __gen_o0 = __G0_Has(__gen_b0.get(), __gen_a0.n); } let __gen_e0: E1 = __gen_o0.or(__gen_a0.get()); let __gen_r0: i32 = 7i32; let __gen_x1: string = "bje"; let __gen_a1: G0[string] = gmk0(__gen_x1, __gen_r0); let __gen_b1: G0[string] = G0 { ...__gen_a1, n: 149i32 }; let __gen_o1: GO0[string] = __G0_Nil; if (((match (__gen_e0) { __E1_V0(__dv0_0_0, __dv0_0_1) => 0i64, __E1_V1(__dv0_1_0, __dv0_1_1) => __dv0_1_1, __E1_V2(__dv0_2_0, __dv0_2_1) => 2147483648i64 }) < 2147483648i64)) { __gen_o1 = __G0_Has(__gen_b1.get(), __gen_a1.n); } let __gen_e1: string = __gen_o1.or(__gen_a1.get()); return (__clo_c0 & 255i32) - 22i32; }

`},
}

func TestSelfHostCertifiesATokenJoinedFromTwoArms(t *testing.T) {
	for _, tc := range tokenJoinSrcs {
		for _, target := range selfHostFusionTargets {
			t.Run(tc.name+"/"+target, func(t *testing.T) {
				env := append([]string{"FERN_LEAKCHECK=1", "FERN_RC_CERTIFY_MEMO=0"}, tc.env...)
				bin := e2eharness.CompileSelfHostSource(t, target, tc.src, env)
				out, err := runScaleTarget(t, target, bin).CombinedOutput()
				if err != nil {
					t.Fatalf("%v:\n%s", err, out)
				}
				var allocs, frees, live int64
				if _, err := fmtSscan(leakSummaryLine(string(out)), &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
					t.Fatalf("ownership: %v\n%s", err, out)
				}
			})
		}
	}
}
