package e2eselfhost

import (
	"testing"
)

// arrPushCliffIRCases pin `__arr_push_shared_count()` — the rc==1 append cliff
// counter — on the self-host IR path. `__fern_arr_push` mutates in place only
// at rc == 1, so that threshold is a performance-correctness boundary with no
// diagnostic of its own: one stray retain upstream makes every append in a
// threaded accumulator copy the whole buffer, and the program stays CORRECT
// while going quadratic.
//
// BOTH halves are pinned, and the healthy case alone is not enough: the
// self-host lowering has to know the builtin, and a reader wired up without a
// bump site returns 0 forever — which reads as a clean run rather than as
// missing instrumentation. The shared case is what proves the
// counter can fire.
//
// The native backend is the oracle (interp has no refcounts and copies
// nothing, so it reports 0 for both). Exit codes stay well under the
// wasmtime clamp.
var arrPushCliffIRCases = []struct {
	name string
	main string
	want int
}{
	// A threaded accumulator handed back through a borrowed param — the shape
	// every byte-emitter in the self-host compiler is built from. Nothing else
	// holds the buffer, so every append after a grow mutates in place.
	{"healthy-threaded-accumulator", `function step(acc: i32[], v: i32): i32[] { return acc.append(v); }
function main(): i32 {
    var acc: i32[] = [];
    var i: i32 = 0;
    while (i < 200) { acc = step(acc, i); i = i + 1; }
    if (acc.len() != 200) { return 254; }
    if (acc[7] != 7 || acc[199] != 199) { return 253; }
    return __arr_push_shared_count();
}`, 0},
	// Crosses the cliff exactly once, deliberately. The loop leaves the buffer
	// with spare capacity; `b` then takes a second reference, so the append
	// that follows cannot mutate in place despite the room and must copy.
	// Reading both afterwards proves the copy really happened — had the append
	// mutated in place, b would see the longer length.
	{"shared-buffer-with-spare-capacity", `function main(): i32 {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < 5) { a = a.append(i); i = i + 1; }
    var b: i32[] = a;
    var c: i32[] = a.append(99);
    if (b.len() != 5 || c.len() != 6) { return 250; }
    if (c[5] != 99 || b[4] != 4) { return 251; }
    return __arr_push_shared_count();
}`, 1},
	// The accumulator handed in as a PLAIN PARAMETER and appended to in a
	// LOOP — coreutils/echo.fern's `append_raw`, and neither of the two shapes
	// the tail-form case above covers. The self-host used to un-share this one
	// ITSELF, with an `__fern_arr_slice` ahead of the consuming push, so the
	// copy never reached `__fern_arr_push`'s own shared-cliff path: the count
	// read 0 while the program copied the whole accumulator per call, which is
	// the reading this counter exists to make impossible (#9526).
	{"shared-param-accumulator-appended-in-a-loop", `function chunk(out: i32[], s: i32[]): i32[] {
    var bs: i32[] = out;
    var i: i32 = 0;
    while (i < s.len()) { bs = bs.append(s[i]); i = i + 1; }
    return bs;
}
function main(): i32 {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < 5) { a = a.append(i); i = i + 1; }
    var s: i32[] = [];
    s = s.append(9);
    var keep: i32[] = a;
    var c: i32[] = chunk(a, s);
    if (keep.len() != 5 || c.len() != 6) { return 250; }
    if (c[5] != 9 || keep[4] != 4) { return 251; }
    return __arr_push_shared_count();
}`, 1},
}

// TestSelfHostArrPushCliffIR runs each case through the self-host CLI on
// x86-64 and wasm and cross-checks the native backend, which lowers the same
// builtin over its own BSS counter. Wasm keeps the counter in a fixed
// low-memory slot (`arr_push_shared_addr`) instead.
func TestSelfHostArrPushCliffIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrPushCliffIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			if _, code := compileAndRunX86_64(t, tc.main+"\n"); code != tc.want {
				t.Fatalf("%s native exited %d, want %d", tc.name, code, tc.want)
			}
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
