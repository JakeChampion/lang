package e2eselfhost

import (
	"strconv"
	"strings"
	"testing"
)

// --- String-literal tuple element: reclaim the BOX (#6127) ------------------
//
// tuple_lit_is_fresh_scalar admitted number / boolean / bare-ident elements but not
// a string LITERAL, so `(i, "hi")` earned no "TUP:" credit at all and a single bind
// leaked the tuple box AND its string: 200 allocs / 0 frees / 6400 live_bytes over
// 100 rounds, against 0 on native.
//
// The exclusion was a conservative bound the function's own comment already
// disclaims ("not a safety requirement"), and a string literal is admitted more
// easily than the bare ident that was already there: the release is a SHALLOW
// __fern_rc_dec that never reads an element, and a literal has no other owner to
// dangle. The discriminator was the element FORM, not its type — the same tuple
// with a bare-ident string element measured 101/100/24, i.e. already reclaimed.
// String length is irrelevant (a 2-byte and a 25-byte literal measure identically),
// so this is not an SSO boundary.
//
// The string box itself stays LEAK-MODE, exactly as it does for an ident element.
// Releasing it is a release-side change on strings — the class
// strfld_reclaim_ok_types_of's history warns about, where the per-module compiler
// self-run segfaulted — and is deliberately not part of this.
//
// So the assertion is not live_bytes == 0. It is that exactly ONE block per round
// survives (the string), where two did before: the tuple box is reclaimed and the
// string is not.

func tupleStringElemSrc(rounds int) string {
	return `function round(i: i32): i32 {
    var p: (i32, string) = (i, "a_much_longer_string_here");
    return p.0;
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < ` + strconv.Itoa(rounds) + `) { t = t + round(r); r = r + 1; }
    return t % 7;
}`
}

// TestSelfHostTupleStringElemBoxReclaimX86_64 — a tuple with a string-literal
// element reclaims its BOX. Asserted as unfreed-blocks-per-round == 1: before the
// change it was 2 (box + string), after it is 1 (the leak-mode string alone).
// Measured at two round counts so the ratio is proven per-round rather than read
// off a single total.
func TestSelfHostTupleStringElemBoxReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	measure := func(t *testing.T, rounds int) (allocs, frees, live int64) {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, tupleStringElemSrc(rounds), []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, "tuple_string_elem_"+strconv.Itoa(rounds), asm)
		stderr, exit := hevRun(t, runner, progBin)
		// sum(0..rounds-1) % 7, confirmed against both oracles (bin/fern -interp
		// and native -target x86-64), not read off the self-host run under test.
		want := (rounds * (rounds - 1) / 2) % 7
		if exit != want {
			t.Fatalf("rounds=%d: exited %d, want %d", rounds, exit, want)
		}
		summary := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "leakcheck: ") {
				summary = line
			}
		}
		if summary == "" {
			t.Fatalf("rounds=%d: no leakcheck summary", rounds)
		}
		if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
			t.Fatalf("rounds=%d: parse %q: %v", rounds, summary, err)
		}
		if allocs == 0 {
			t.Fatalf("rounds=%d: allocated nothing — the probe is not exercising the path", rounds)
		}
		if frees > allocs {
			t.Fatalf("rounds=%d: allocs=%d frees=%d — frees above allocs means the box was "+
				"released twice", rounds, allocs, frees)
		}
		return allocs, frees, live
	}

	for _, rounds := range []int{100, 200} {
		allocs, frees, live := measure(t, rounds)
		unfreed := allocs - frees
		if want := int64(rounds); unfreed != want {
			t.Errorf("rounds=%d: allocs=%d frees=%d live_bytes=%d — %d unfreed blocks, want %d "+
				"(exactly one leak-mode string per round). %d per round means the tuple BOX is "+
				"leaking too, which is what this closes; fewer would mean the string element is "+
				"being released, which this deliberately does not do",
				rounds, allocs, frees, live, unfreed, want, unfreed/int64(rounds))
		}
	}
}

// TestSelfHostTupleStringElemHazardsX86_64 — admitting a string-literal element
// must not change the answer for the shapes around it. Each `want` was confirmed
// against both the interpreter and the native x86-64 backend.
func TestSelfHostTupleStringElemHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			// The string element is READ after the box would be released. The
			// shallow release never touches the element, so the read must still
			// see the original bytes — this is the assertion that the release is
			// genuinely shallow rather than walking elements.
			name: "string_element_read_after_rebind",
			src: `function round(): i32 {
    var p: (i32, string) = (0, "hello_there_world");
    var i: i32 = 0;
    while (i < 4) { p = (i, "hello_there_world"); i = i + 1; }
    if (p.1 != "hello_there_world") { return 1; }
    return p.0 + p.1.len();
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(); r = r + 1; } return t % 97; }`,
			want: 60,
		},
		{
			// The string element is moved into a container that outlives the tuple.
			// The box release must not disturb it.
			name: "string_element_escapes_to_container",
			src: `function round(): i32 {
    var out: string[] = [];
    var i: i32 = 0;
    while (i < 3) { var p: (i32, string) = (i, "hello_there_world"); out = out.append(p.1); i = i + 1; }
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < out.len()) { t = t + out[k].len(); k = k + 1; }
    return t;
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(); r = r + 1; } return t % 97; }`,
			want: 56,
		},
		{
			// A mixed tuple whose string element sits beside a bare ident — the two
			// element forms in one literal, both now admitted.
			name: "mixed_ident_and_string_elements",
			src: `function round(i: i32, s: string): i32 {
    var p: (i32, string, string) = (i, s, "literal_string_here");
    return p.0 + p.1.len() + p.2.len();
}
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    var s: string = "ident_string_here";
    while (r < 100) { t = t + round(r, s); r = r + 1; }
    return t % 97;
}`,
			want: 14,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "tuple_strelem_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash means the box release "+
					"reached the string element it must leave alone", exit, tc.want)
			}
		})
	}
}
