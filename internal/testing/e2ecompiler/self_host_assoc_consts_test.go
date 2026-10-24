package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var assocConstCases = []struct {
	name string
	src  string
}{
	{
		name: "reads resolve on a concrete type, through a bound and as Self",
		src: `trait Lanes {
    const LANES: i32;
    const NAME: string = "lanes";
    function width(self: Self): i32;
}
struct V2 { a: f64 }
impl Lanes for V2 {
    const LANES: i32 = 2;
    function width(self: Self): i32 { return Self.LANES; }
}
impl Lanes for f64 {
    const LANES: i32 = 1;
    function width(self: Self): i32 { return Self.LANES; }
}
function chunk[T: Lanes](n: i32): i32 { return n / T.LANES; }
function main(): i32 {
    let s: string = V2.NAME;
    return V2.LANES + f64.LANES + chunk[V2](8) + s.len();
}`,
	},
	{
		name: "a read is typed at the constant's type",
		src: `trait Lanes { const LANES: i32; }
struct V2 { a: f64 }
impl Lanes for V2 { const LANES: i32 = 2; }
function main(): i32 {
    let s: string = V2.LANES;
    return 0;
}`,
	},
	{
		name: "impl must bind every abstract constant",
		src: `trait Lanes { const LANES: i32; }
struct V2 { a: f64 }
impl Lanes for V2 { }
function main(): i32 { return 0; }`,
	},
	{
		name: "a constant is not bound by a function",
		src: `trait Lanes { const LANES: i32; }
struct V2 { a: f64 }
impl Lanes for V2 { function LANES(): i32 { return 2; } }
function main(): i32 { return 0; }`,
	},
	{
		name: "a function is not bound by a const",
		src: `trait Lanes { function lanes(): i32; }
struct V2 { a: f64 }
impl Lanes for V2 { const lanes: i32 = 2; }
function main(): i32 { return 0; }`,
	},
	{
		name: "a binding takes the trait's type",
		src: `trait Lanes { const LANES: i32; }
struct V2 { a: f64 }
impl Lanes for V2 { const LANES: string = "two"; }
function main(): i32 { return 0; }`,
	},
	{
		name: "a read names a constant, not a function",
		src: `trait Lanes { function lanes(): i32; }
struct V2 { a: f64 }
impl Lanes for V2 { function lanes(): i32 { return 2; } }
function f[T: Lanes](): i32 { return T.lanes; }
function main(): i32 { return V2.lanes + f[V2](); }`,
	},
	{
		name: "a call names a function, not a constant",
		src: `trait Lanes { const LANES: i32; }
struct V2 { a: f64 }
impl Lanes for V2 { const LANES: i32 = 2; }
function f[T: Lanes](): i32 { return T.LANES(); }
function main(): i32 { return V2.LANES() + f[V2](); }`,
	},
	{
		name: "a name neither declares",
		src: `trait Lanes { const LANES: i32; }
struct V2 { a: f64 }
impl Lanes for V2 { const LANES: i32 = 2; }
function f[T: Lanes](): i32 { return T.WIDTH; }
function main(): i32 { return V2.WIDTH + f[V2](); }`,
	},
	{
		name: "an unbounded parameter declares no constant",
		src: `function f[T](x: T): i32 { return T.NOPE; }
function main(): i32 { return f(1); }`,
	},
	{
		name: "an unbounded parameter declares no associated function",
		src: `import "core/cmp";
struct Foo { a: i32 }
impl cmp.Default for Foo { function default(): Foo { return Foo { a: 7 }; } }
function f[T](x: T): T { return T.default(); }
function main(): i32 { return f(Foo { a: 1 }).a; }`,
	},
	{
		name: "a bound that does not declare the function",
		src: `import "core/cmp";
function f[T: cmp.Eq](x: T): T { return T.nope(); }
function main(): i32 { return 0; }`,
	},
	{
		name: "an enum's unknown field is a missing variant",
		src: `enum Kind { A, B }
function main(): i32 {
    let k: Kind = Kind.C;
    return 0;
}`,
	},
	{
		name: "an enum reads its own constant",
		src: `trait Lanes { const LANES: i32; }
enum Kind { A, B }
impl Lanes for Kind { const LANES: i32 = 3; }
function main(): i32 {
    let k: Kind = Kind.A;
    return Kind.LANES;
}`,
	},
}

func TestSelfHostAssocConstsDifferential(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	driver, err := filepath.Abs("../../../compiler/drivers/checker_codes_run.fern")
	if err != nil {
		t.Fatalf("abs driver path: %v", err)
	}

	for _, tc := range assocConstCases {
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

			if !equalStrings(assocConstOnly(goMsgs), assocConstOnly(shMsgs)) {
				t.Errorf("the two checkers disagree on the associated-constant diagnostics.\nnative:    %s\nself-host: %s",
					joinOrNone(assocConstOnly(goMsgs)), joinOrNone(assocConstOnly(shMsgs)))
			}
			if (len(goMsgs) == 0) != (len(shMsgs) == 0) {
				t.Errorf("one checker accepts this program and the other rejects it.\nnative:    %s\nself-host: %s",
					joinOrNone(goMsgs), joinOrNone(shMsgs))
			}
		})
	}
}

func assocConstOnly(ms []string) []string {
	var out []string
	for _, m := range ms {
		if strings.Contains(m, "const") || strings.Contains(m, "variant") {
			out = append(out, m)
		}
	}
	return out
}
