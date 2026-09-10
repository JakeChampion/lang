package e2eselfhost

import (
	"strconv"
	"strings"
	"testing"
)

// #8774: a struct literal whose array field is set from a tuple ELEMENT —
// `Holder { bytes: m.0 }` over `m: (u8[], boolean)` — bailed the self-host,
// while the same value bound to a local first lowered. The element is an
// alias of a buffer the tuple box still owns, so it takes the alias-inc the
// ident and field-read sources take, and the field's drop and the tuple's
// reclaim then balance on the one inc.
//
// Each case is written twice, the direct spelling and the local-first one it
// replaces, and the two must leave the SAME number of blocks unreclaimed at
// both round counts: the admission may add nothing to what the shape already
// costs. (A tuple-returning call's result box is itself still leak-mode
// under the self-host, so the census is not flat on its own — that residue
// belongs to the call, and both spellings pay it alike.) The sanitizer leg
// keeps the over-release trap silent. Exits are the interpreter's.

const tupleElemFieldCommon = `struct Holder { bytes: u8[], flag: boolean }
struct P { x: i32 }
struct Bag { ps: P[], n: i32 }

function make(): (u8[], boolean) {
  var t: u8[] = __alloc_u8(4);
  return (t, true);
}
function mk_bag(): (P[], i32) {
  var ps: P[] = [P { x: 1 }, P { x: 2 }, P { x: 3 }];
  return (ps, 9);
}
function nest(): (i32, (u8[], boolean)) {
  var t: u8[] = __alloc_u8(6);
  return (1, (t, false));
}
`

var tupleElemFieldCases = []struct {
	name, direct, local string
}{
	// The issue's reproducer: a scalar-element array out of a tuple local.
	{"u8_elem_direct",
		`var m: (u8[], boolean) = make();
    var h: Holder = Holder { bytes: m.0, flag: m.1 };
    if (!h.flag) { return 2; }
    acc = acc + h.bytes.len();`,
		`var m: (u8[], boolean) = make();
    var b: u8[] = m.0;
    var h: Holder = Holder { bytes: b, flag: m.1 };
    if (!h.flag) { return 2; }
    acc = acc + h.bytes.len();`},
	// An array-of-struct element, whose field drop walks the elements.
	{"struct_arr_elem",
		`var m: (P[], i32) = mk_bag();
    var b: Bag = Bag { ps: m.0, n: m.1 };
    if (b.n != 9) { return 3; }
    acc = acc + b.ps[2].x;`,
		`var m: (P[], i32) = mk_bag();
    var ps: P[] = m.0;
    var b: Bag = Bag { ps: ps, n: m.1 };
    if (b.n != 9) { return 3; }
    acc = acc + b.ps[2].x;`},
	// A nested element, `n.1.0`, read through the outer element's tuple tag.
	{"nested_tuple_elem",
		`var n: (i32, (u8[], boolean)) = nest();
    var h: Holder = Holder { bytes: n.1.0, flag: n.1.1 };
    if (h.flag) { return 4; }
    acc = acc + h.bytes.len();`,
		`var n: (i32, (u8[], boolean)) = nest();
    var b: u8[] = n.1.0;
    var h: Holder = Holder { bytes: b, flag: n.1.1 };
    if (h.flag) { return 4; }
    acc = acc + h.bytes.len();`},
}

func tupleElemFieldSrc(run string, rounds int) string {
	return tupleElemFieldCommon + "function main(): i32 {\n  var acc: i32 = 0;\n  var i: i32 = 0;\n  while (i < " +
		strconv.Itoa(rounds) + ") {\n    " + run + "\n    i = i + 1;\n  }\n  return acc % 200;\n}\n"
}

var tupleElemFieldRounds = [2]int{200, 2000}

func TestSelfHostStructLitTupleElemFieldX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleElemFieldCases {
		t.Run(tc.name, func(t *testing.T) {
			for _, rounds := range tupleElemFieldRounds {
				var leaked [2]int64
				for k, run := range []string{tc.direct, tc.local} {
					src := tupleElemFieldSrc(run, rounds)
					want := interpExit(t, interpBin, src)
					asm := runCaptureEnv(t, runner, driverBin, []byte(src),
						[]string{"PATH=/usr/bin:/bin", "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1"}, "-ir")
					if len(asm) == 0 {
						t.Fatal("self-host compiler emitted 0 bytes")
					}
					bin := buildBin(t, gcc, dir, "tef_"+tc.name+"_"+strconv.Itoa(rounds)+"_"+strconv.Itoa(k), string(asm))
					stderr, code := runCaptureStderrExit(t, runner, bin)
					if code != want {
						t.Fatalf("%s (spelling %d) at %d rounds exited %d, want %d (interp oracle)", tc.name, k, rounds, code, want)
					}
					leaked[k] = tupleHandbackCensus(t, tc.name, stderr)
				}
				if leaked[0] != leaked[1] {
					t.Errorf("%s at %d rounds: the direct element leaves %d blocks unreclaimed, the local-first spelling %d — the admission must cost nothing the local did not",
						tc.name, rounds, leaked[0], leaked[1])
				}
			}
		})
	}
}

func TestSelfHostStructLitTupleElemFieldSanitizeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleElemFieldCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tupleElemFieldSrc(tc.direct, tupleElemFieldRounds[1])
			want := interpExit(t, interpBin, src)
			asm := runCaptureEnv(t, runner, driverBin, []byte(src),
				[]string{"PATH=/usr/bin:/bin", "FERN_STRICT_IR=1", "FERN_SANITIZE=1"}, "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, "tefsan_"+tc.name, string(asm))
			stderr, code := runCaptureStderrExit(t, runner, bin)
			if code != want {
				t.Fatalf("%s exited %d under the sanitizer, want %d (interp oracle)", tc.name, code, want)
			}
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "fern-sanitizer:") && !strings.HasPrefix(line, "fern-sanitizer: leak") {
					t.Errorf("%s: %s", tc.name, line)
				}
			}
		})
	}
}
