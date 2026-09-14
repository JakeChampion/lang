package e2eselfhost

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Associated types (docs/ASSOCIATED-TYPES.md) were native-only until this
// port: the self-host lexer folded `::` into `.`, its trait body accepted only
// `function`, and nothing resolved a projection. This gate holds the two
// checkers in step on the whole feature — every case asserts the SAME
// diagnostics from both, in both directions, so a divergence fails whichever
// side drifts.
//
// The corpus is the semantics the doc states, one case each: resolution,
// conformance (bind every one, bind nothing else), a projection through a
// bounded generic, and object safety with and without the `dyn` pin.
var assocTypeCases = []struct {
	name string
	src  string
}{
	{
		// The happy path. `Self::Item` in the impl method resolves to the
		// bound `i32`, so the whole program type-checks clean.
		name: "resolves to the impl's binding",
		src: `trait Holder {
    type Item;
    function get(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Holder for IntBox {
    type Item = i32;
    function get(self: Self): Self::Item { return self.v; }
}
function main(): i32 {
    var b: IntBox = IntBox { v: 7 };
    return b.get();
}`,
	},
	{
		// Resolution is load-bearing, not decorative: the binding makes
		// `b.get()` an i32, so a string destination is a type error. A
		// checker that merely SKIPPED the projection would be silent here,
		// which is what the self-host did before the port.
		name: "resolution is observable at a use site",
		src: `trait Holder {
    type Item;
    function get(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Holder for IntBox {
    type Item = i32;
    function get(self: Self): Self::Item { return self.v; }
}
function main(): i32 {
    var b: IntBox = IntBox { v: 7 };
    var s: string = b.get();
    return 0;
}`,
	},
	{
		name: "impl must bind every associated type",
		src: `trait Holder {
    type Item;
    function get(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Holder for IntBox {
    function get(self: Self): Self::Item { return self.v; }
}
function main(): i32 { return 0; }`,
	},
	{
		name: "impl may not bind one the trait does not declare",
		src: `trait Holder {
    type Item;
    function get(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Holder for IntBox {
    type Item = i32;
    type Extra = i32;
    function get(self: Self): Self::Item { return self.v; }
}
function main(): i32 { return 0; }`,
	},
	{
		// `H::Item` through a bounded type parameter. Also the regression
		// guard for the conformance comparison: the impl method's projection
		// is resolved by the parser, so the TRAIT's requirement has to be
		// resolved the same way or every associated-type impl reads as a
		// signature mismatch.
		name: "projection through a bounded generic",
		src: `trait Holder {
    type Item;
    function get(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Holder for IntBox {
    type Item = i32;
    function get(self: Self): Self::Item { return self.v; }
}
function first[H: Holder](h: H): H::Item { return h.get(); }
function main(): i32 {
    var b: IntBox = IntBox { v: 7 };
    return first(b);
}`,
	},
	{
		name: "unpinned associated type is not object-safe",
		src: `trait Holder {
    type Item;
    function get(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Holder for IntBox {
    type Item = i32;
    function get(self: Self): Self::Item { return self.v; }
}
function take(h: dyn Holder): i32 { return 0; }
function main(): i32 { return 0; }`,
	},
	{
		name: "pinning the associated type restores object safety",
		src: `trait Holder {
    type Item;
    function get(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Holder for IntBox {
    type Item = i32;
    function get(self: Self): Self::Item { return self.v; }
}
function take(h: dyn Holder[Item = i32]): i32 { return 0; }
function main(): i32 { return 0; }`,
	},
}

func TestSelfHostAssocTypesDifferential(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	driver, err := filepath.Abs("../../examples/self_host/checker_codes_run.fern")
	if err != nil {
		t.Fatalf("abs driver path: %v", err)
	}

	for _, tc := range assocTypeCases {
		t.Run(tc.name, func(t *testing.T) {
			var goMsgs []string
			for _, d := range goCheckerDiags(t, t.TempDir(), tc.src) {
				goMsgs = append(goMsgs, d.code+": "+d.msg)
			}

			cmd := exec.Command(interpBin, "-interp", driver)
			cmd.Stdin = strings.NewReader(tc.src)
			out, _ := cmd.Output()
			var shMsgs []string
			for _, d := range driverDiags(string(out)) {
				shMsgs = append(shMsgs, d.code+": "+d.msg)
			}

			// Both must agree on the associated-type rules themselves, and
			// neither may be silent where the other rejects.
			if !equalStrings(assocOnly(goMsgs), assocOnly(shMsgs)) {
				t.Errorf("the two checkers disagree on the associated-type diagnostics.\nnative:    %s\nself-host: %s",
					joinOrNone(assocOnly(goMsgs)), joinOrNone(assocOnly(shMsgs)))
			}
			if (len(goMsgs) == 0) != (len(shMsgs) == 0) {
				t.Errorf("one checker accepts this program and the other rejects it.\nnative:    %s\nself-host: %s",
					joinOrNone(goMsgs), joinOrNone(shMsgs))
			}
			// Full equality is asserted everywhere it holds. Where it does
			// not, the surplus is native's CASCADE off an unresolvable
			// projection — an `E002 return type mismatch` against a declared
			// type that does not resolve. That gap is not this feature's: the
			// self-host declines the same E002 for any unknown nominal return
			// (`function f(): Nope { return 1; }`), which is its documented
			// zero-false-positive conservatism and a separate convergence
			// item. It is asserted here so a NEW divergence still fails.
			if !equalStrings(goMsgs, shMsgs) && !onlyCascadeSurplus(goMsgs, shMsgs) {
				t.Errorf("the two checkers disagree beyond the known unknown-nominal cascade.\nnative:    %s\nself-host: %s",
					joinOrNone(goMsgs), joinOrNone(shMsgs))
			}
		})
	}
}

func joinOrNone(ms []string) string {
	if len(ms) == 0 {
		return "<no diagnostics>"
	}
	return strings.Join(ms, "\n           ")
}

// assocOnly keeps the diagnostics about the associated-type rules — what this
// port owns and what both checkers must say identically.
func assocOnly(ms []string) []string {
	var out []string
	for _, m := range ms {
		if strings.Contains(m, "associated type") {
			out = append(out, m)
		}
	}
	return out
}

// onlyCascadeSurplus reports whether the self-host's diagnostics are native's
// with only `E002 return type mismatch` entries missing — the cascade the
// self-host declines for ANY unresolvable declared return type, associated or
// not. Anything else missing, or anything extra, is a real divergence.
func onlyCascadeSurplus(goMsgs, shMsgs []string) bool {
	have := map[string]int{}
	for _, m := range shMsgs {
		have[m]++
	}
	for _, m := range goMsgs {
		if have[m] > 0 {
			have[m]--
			continue
		}
		if !strings.HasPrefix(m, "E002: return type mismatch") {
			return false
		}
	}
	for _, n := range have {
		if n > 0 {
			return false
		}
	}
	return true
}
