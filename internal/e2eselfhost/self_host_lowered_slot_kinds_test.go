package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostLoweredSlotKinds pins the value kinds lowering carries out to
// LoweredFn (#6639 slice 1.5): which local slots hold i64 / f64 / string /
// array values, and the function's declared return type.
//
// These facts exist in LowerState and used to die with it. A pass that
// ANALYSES the op stream — as opposed to emitting from it — cannot recover
// them: the ops say `load_local 1`, not what slot 1 holds. Native's
// verifyStack is type-directed for exactly this reason, and its own answer is
// not uniform (a string is one operand slot on the register backends and TWO
// on wasm32, per ir.UseTwoWordStrings), so a depth-only model would diverge at
// the first string and never recover. `checker.fern`, `parser.fern` and
// `irlower.fern` are string-heavy, so that is most of the compiler.
//
// The gate is the classification itself rather than a downstream consumer,
// because the failure mode is silent: a slot dropped from a list produces no
// error anywhere, it just makes a later analysis quietly wrong about that
// function.
func TestSelfHostLoweredSlotKinds(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irlower_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "irlower_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "irlower_run.fern", "irlower_run")

	// `widths` holds a value of every kind, so a class dropped from the dump is
	// visible as an absence rather than as a wrong number. Two of its slots are
	// PARAMS — the string `s` and the array `ns`: params and body locals share
	// one frame, and a classification that only walked the body would miss
	// both.
	//
	// `containers` is the other half of the gate and the reason the probe is
	// not just `widths` (#7322). A struct, a tuple and an option are heap
	// values the exit sweep reclaims, but they are not arrays, and `arr` is a
	// value kind rather than a reclaim set. A probe without them cannot tell
	// the two apart, and this test read green for months while the
	// classification unioned in every rc container it could name.
	const src = `struct P { x: i32, y: i32 }
function widths(n: i32, s: string, ns: i32[]): i64 {
    var big: i64 = 7i64;
    var f: f64 = 1.5;
    var txt: string = s + "x";
    var xs: i32[] = [n];
    var strs: string[] = [txt];
    if (f > 0.0 && txt.len() > 0 && xs.len() > 0 && strs.len() > 0 && ns.len() > 0) { return big; }
    return 0i64;
}
function containers(n: i32): i32 {
    var p: P = P { x: n, y: 1 };
    var tp: (i32, i32) = (n, 2);
    var o: Option[P] = Some(P { x: n, y: 3 });
    var got: i32 = 0;
    match (o) { Some(q) => { got = q.x; }, None => {} }
    return p.x + tp.0 + got;
}
function only_ints(a: i32): i32 { var b: i32 = a + 1; return b; }
function main(): i32 { return (widths(1, "a", [1]) as i32) + containers(1) + only_ints(1); }
`

	cmd := exec.Command(bin, "-slots")
	cmd.Stdin = strings.NewReader(src)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("irlower_run -slots: %v\n%s", err, out)
	}
	got := string(out)

	// The declared return type reaches LoweredFn verbatim — it is what the ops
	// must leave on the stack, and lowering is the last place that knows it.
	//
	// Every probe function is listed, `containers` included: the dump prints
	// only the functions that LOWERED, so a function that stopped lowering
	// would otherwise turn its slot assertions below into assertions about an
	// absent map entry, which hold for free.
	for _, want := range []string{"widths ret=i64 params=3", "containers ret=i32 params=1", "only_ints ret=i32 params=1", "main ret=i32"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in -slots dump:\n%s", want, got)
		}
	}

	kinds := map[string][]string{}
	var fn string
	for _, line := range strings.Split(got, "\n") {
		if !strings.HasPrefix(line, "  ") {
			fn, _, _ = strings.Cut(line, " ")
			continue
		}
		class, slots, _ := strings.Cut(strings.TrimSpace(line), ":")
		kinds[fn+"/"+class] = strings.Fields(slots)
	}

	// Slot 0 is `n` (i32), 1 is `s` (string) and 2 is `ns` (i32[]); the body
	// locals follow. The two PARAM slots are asserted by index — param slots
	// are fixed by the signature — and the body slots only by count, so a
	// renumbering upstream does not make this test fail for the wrong reason.
	if len(kinds["widths/i64"]) != 1 {
		t.Errorf("widths: want exactly one i64 slot (`big`), got %v", kinds["widths/i64"])
	}
	if len(kinds["widths/f64"]) != 1 {
		t.Errorf("widths: want exactly one f64 slot (`f`), got %v", kinds["widths/f64"])
	}
	if a := kinds["widths/arr"]; len(a) != 3 || a[0] != "2" {
		t.Errorf("widths: want three array slots with the PARAM at slot 2 (`ns`, `xs` and `strs`), got %v", a)
	}
	if s := kinds["widths/str"]; len(s) != 2 || s[0] != "1" {
		t.Errorf("widths: want two string slots with the PARAM at slot 1 (`s` and `txt`), got %v", s)
	}

	// A slot holds one kind of value, so the four lists partition the slots
	// they name. The union that `arr` used to carry showed up here first: a
	// `string[]` local is an array and not a string, and a slot claimed by two
	// classes means one of them is answering a question about ownership rather
	// than about the value.
	owner := map[string]string{}
	for _, class := range []string{"i64", "f64", "str", "arr"} {
		for _, slot := range kinds["widths/"+class] {
			if prev, dup := owner[slot]; dup {
				t.Errorf("widths: slot %s classified as both %s and %s", slot, prev, class)
				continue
			}
			owner[slot] = class
		}
	}

	// The other direction, twice over. `only_ints` holds nothing but i32s, so
	// a pass that reported every slot as some kind would pass every assertion
	// above and fail here. `containers` holds a struct, a tuple and an option
	// — heap values the exit sweep reclaims, none of them an array, none of
	// them any other class either.
	for _, fn := range []string{"only_ints", "containers"} {
		for _, class := range []string{"i64", "f64", "str", "arr"} {
			if got := kinds[fn+"/"+class]; len(got) != 0 {
				t.Errorf("%s holds no %s value, but %s slots came back as %v", fn, class, class, got)
			}
		}
	}
}
