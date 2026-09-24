package e2eselfhost

import "testing"

// #8609: a producer of a struct array registered under STRUCTARRF: /
// ARRSTRUCTF: only when every element was a struct LITERAL. One whose elements
// are calls to a strict-fresh producer (`[mkop(0), …]`, `pre.append(mkop(i))`)
// registered nothing, so `var v: Op[] = three()` took the shallow buffer dec
// and stranded every element box, although the store rule already admitted the
// same call as a fresh element. Both element classes are covered: scalar-field
// structs and structs with an array field (the deep walk).
var structArrCallProducerCases = []struct{ name, src string }{
	{"literal-of-calls", `struct Op { a: i32, b: i32 }
function mkop(i: i32): Op { return Op { a: i, b: i + 1 }; }
function three(): Op[] { return [mkop(0), mkop(1), mkop(2)]; }
function round(r: i32): i32 { var v: Op[] = three(); return v[0].a + v[1].b + v[2].a + v.len(); }
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 20) { t = t + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 100; }`},
	{"append-of-calls", `struct Op { a: i32, b: i32 }
function mkop(i: i32): Op { return Op { a: i, b: i + 1 }; }
function three(): Op[] { var pre: Op[] = []; var i: i32 = 0; while (i < 3) { pre = pre.append(mkop(i)); i = i + 1; } return pre; }
function round(r: i32): i32 { var v: Op[] = three(); return v[0].a + v[1].b + v[2].a + v.len(); }
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 20) { t = t + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 100; }`},
	{"deep-literal-of-calls", `struct Q { xs: i32[], k: i32 }
function mkq(i: i32): Q { return Q { xs: [i, i + 1], k: i }; }
function three(): Q[] { return [mkq(0), mkq(1), mkq(2)]; }
function round(r: i32): i32 { var v: Q[] = three(); return v[0].xs.len() + v[1].k + v[2].xs[1] + v.len(); }
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 20) { t = t + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 100; }`},
	{"deep-append-of-calls", `struct Q { xs: i32[], k: i32 }
function mkq(i: i32): Q { return Q { xs: [i, i + 1], k: i }; }
function three(): Q[] { var pre: Q[] = []; var i: i32 = 0; while (i < 3) { pre = pre.append(mkq(i)); i = i + 1; } return pre; }
function round(r: i32): i32 { var v: Q[] = three(); return v[0].xs.len() + v[1].k + v[2].xs[1] + v.len(); }
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 20) { t = t + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 100; }`},
}

func TestSelfHostStructArrCallProducerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range structArrCallProducerCases {
		t.Run(tc.name, func(t *testing.T) {
			natV, natExit := nativeLeakVerdict(t, cli, dir, "sacp_"+tc.name, tc.src)
			shV, shExit := selfHostLeakVerdict(t, gcc, runner, driverBin, dir, "sacp_"+tc.name, tc.src)
			if natV != verdictClean {
				t.Fatalf("native verdict %s (exit %d): the oracle itself is not clean", natV, natExit)
			}
			if shExit != natExit {
				t.Fatalf("self-host exited %d, native %d (99 = rc underflow)", shExit, natExit)
			}
			if shV != verdictClean {
				t.Fatalf("self-host verdict %s, want clean", shV)
			}
		})
	}
}
