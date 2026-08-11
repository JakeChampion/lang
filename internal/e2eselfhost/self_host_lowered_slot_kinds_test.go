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

	// One function holding a value of every kind, so a class dropped from the
	// dump is visible as an absence rather than as a wrong number. The string
	// PARAM matters most: params and body locals share one frame, and a
	// classification that only walked the body would miss it.
	const src = `function widths(n: i32, s: string): i64 {
    var big: i64 = 7i64;
    var f: f64 = 1.5;
    var txt: string = s + "x";
    var xs: i32[] = [n];
    if (f > 0.0 && txt.len() > 0 && xs.len() > 0) { return big; }
    return 0i64;
}
function only_ints(a: i32): i32 { var b: i32 = a + 1; return b; }
function main(): i32 { return (widths(1, "a") as i32) + only_ints(1); }
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
	for _, want := range []string{"widths ret=i64 params=2", "only_ints ret=i32 params=1", "main ret=i32"} {
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

	// Slot 0 is `n` (i32) and 1 is `s` (string); the body locals follow. Only
	// the string list's membership is asserted by index — the others are
	// asserted by count, so a renumbering upstream does not make this test
	// fail for the wrong reason.
	if len(kinds["widths/i64"]) != 1 {
		t.Errorf("widths: want exactly one i64 slot (`big`), got %v", kinds["widths/i64"])
	}
	if len(kinds["widths/f64"]) != 1 {
		t.Errorf("widths: want exactly one f64 slot (`f`), got %v", kinds["widths/f64"])
	}
	if len(kinds["widths/arr"]) != 1 {
		t.Errorf("widths: want exactly one array slot (`xs`), got %v", kinds["widths/arr"])
	}
	if s := kinds["widths/str"]; len(s) != 2 || s[0] != "1" {
		t.Errorf("widths: want two string slots with the PARAM at slot 1 (`s` and `txt`), got %v", s)
	}

	// The other direction: a function with no wide, float, string or array
	// value classifies nothing. A pass that reported every slot as some kind
	// would pass every assertion above and fail here.
	for _, class := range []string{"i64", "f64", "str", "arr"} {
		if got := kinds["only_ints/"+class]; len(got) != 0 {
			t.Errorf("only_ints holds only i32s, but %s slots came back as %v", class, got)
		}
	}
}
