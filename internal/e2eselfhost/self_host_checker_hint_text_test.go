package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/modload"
)

// typeErrPrefixRE strips the `type error at L:C: ` a checker error wears
// in its Error(), leaving the message body — the same thing the self-host
// driver prints, and what `error[EXXX]: ` is followed by on screen.
var typeErrPrefixRE = regexp.MustCompile(`^type error at \d+:\d+: `)

// goCheckerDiags runs the native checker over src and returns its
// diagnostics as code/message pairs.
func goCheckerDiags(t *testing.T, dir, src string) []driverDiag {
	t.Helper()
	p := filepath.Join(dir, "gocheck_input.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write gocheck input: %v", err)
	}
	prog, _, err := modload.Load(p)
	if err != nil {
		return nil
	}
	_, err = checker.Check(prog)
	if err == nil {
		return nil
	}
	errs := []error{err}
	if es, ok := err.(diag.Errors); ok {
		errs = es
	}
	var out []driverDiag
	for _, e := range errs {
		code := ""
		if cd, ok := e.(diag.Coded); ok {
			code = cd.Code()
		}
		out = append(out, driverDiag{code, typeErrPrefixRE.ReplaceAllString(e.Error(), "")})
	}
	return out
}

// byCode groups diagnostics into code → sorted unique messages. Order and
// multiplicity are dropped for the same reason the code gates drop them:
// the two checkers walk in different orders and may report a shape once or
// twice, and neither is a text difference.
func byCode(ds []driverDiag) map[string][]string {
	out := map[string][]string{}
	for _, d := range ds {
		out[d.code] = append(out[d.code], d.msg)
	}
	for code, msgs := range out {
		sort.Strings(msgs)
		out[code] = slicesCompact(msgs)
	}
	return out
}

func slicesCompact(in []string) []string {
	var out []string
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// hintTextCase is one program whose diagnostic tells the reader what to
// write. `spelling` is a fragment of that advice which must appear in the
// native message — it keeps the corpus honest about why each row is here,
// so a case cannot decay into pinning arbitrary prose.
type hintTextCase struct {
	name     string
	src      string
	code     string
	spelling string
}

var hintTextCases = []hintTextCase{
	{
		name: "E041 equality",
		src: `struct P { x: i32 }
function main(): i32 {
    var a: P = P { x: 1 };
    var b: P = P { x: 1 };
    if (a == b) { return 1; }
    return 0;
}`,
		code:     "E041",
		spelling: "`@derive(cmp.Eq)`",
	},
	{
		name: "E041 ordering",
		src: `struct P { x: i32 }
function main(): i32 {
    var a: P = P { x: 1 };
    var b: P = P { x: 1 };
    if (a < b) { return 1; }
    return 0;
}`,
		code:     "E041",
		spelling: "`@derive(cmp.Ord)`",
	},
	{
		name:     "E056 array element assignment",
		src:      `function main(): i32 { var a: i32[] = [1, 2]; a[0] = 5; return a[0]; }`,
		code:     "E056",
		spelling: "`arr = arr.with(i, value)`",
	},
	{
		name: "E026 wildcard arm placement",
		src: `enum C { R, G }
function main(): i32 {
    var c: C = R;
    match (c) { _ => { return 0; }, R => { return 1; } }
    return 0;
}`,
		code:     "E026",
		spelling: "`_` arm must be last",
	},
	{
		name: "E030 inexhaustive match",
		src: `enum C { R, G }
function main(): i32 {
    var c: C = R;
    match (c) { R => { return 1; } }
    return 0;
}`,
		code:     "E030",
		spelling: "use `_`",
	},
	{
		// The one hint that ECHOES the reader's own spelling rather than
		// hardcoding one, so it is the row that pins the echo staying an
		// echo on both sides.
		name: "E021 underivable field",
		src: `trait Ord { function cmp(self: Self, other: Self): i32; }
@derive(Ord)
struct Foo { x: i32 }
function main(): i32 { return 0; }`,
		code:     "E021",
		spelling: "`impl Ord for i32`",
	},
	{
		name: "E038 print needs Display",
		src: `struct P { x: i32 }
function main(): i32 {
    var a: P = P { x: 1 };
    print(a);
    return 0;
}`,
		code:     "E038",
		spelling: "`@derive(cmp.Display)`",
	},
	{
		name:     "E045 float map key",
		src:      `function main(): i32 { var m = Map { 1.5: 1 }; return 0; }`,
		code:     "E045",
		spelling: "`@derive(cmp.Eq, cmp.Hash)`",
	},
	{
		name: "E045 struct map key",
		src: `struct K { a: i32 }
function main(): i32 { var m = Map { K { a: 1 }: 1 }; return 0; }`,
		code:     "E045",
		spelling: "`@derive(cmp.Eq, cmp.Hash)`",
	},
	{
		// `bool` is the cross-language slip a Fern reader is likeliest to
		// write, so this row pins it being REJECTED rather than quietly taken
		// as a synonym for `boolean` — which is what the self-host's five
		// type-name resolvers did while native errored.
		name:     "E064 bool for boolean",
		src:      `function main(): i32 { var b: bool = true; return 0; }`,
		code:     "E064",
		spelling: "did you mean `boolean`?",
	},
	{
		name:     "E064 int for i32",
		src:      `function main(): i32 { var n: int = 1; return 0; }`,
		code:     "E064",
		spelling: "did you mean `i32`?",
	},
	{
		name:     "E064 String for string",
		src:      `function main(): i32 { var s: String = "x"; return 0; }`,
		code:     "E064",
		spelling: "did you mean `string`?",
	},
	{
		// The type NAME a message renders is advice to write as much as a
		// hint is: the self-host used to print its internal "bool" tag here,
		// naming a type the reader cannot spell. e042_ret_label patched
		// exactly one of the two E042 sites; this is the other one (#7251).
		name: "E042 non-Option operand names boolean",
		src: `function f(): i32 { var b: boolean = true; var c = b?; return 0; }
function main(): i32 { return 0; }`,
		code:     "E042",
		spelling: "got boolean",
	},
	{
		// The message spells out the call that DOES work, so the reader is
		// not left to guess that a receiver-less trait requirement is
		// reached through the type parameter rather than through a value.
		name: "E021 associated function called as a method",
		src: `trait Show { function show(): i32; }
struct P { v: i32 }
impl Show for P { function show(): i32 { return 5; } }
function pick[T: Show](a: T): i32 { return a.show(); }
function main(): i32 { return pick(P { v: 42 }); }`,
		code:     "E021",
		spelling: "call it as T.show(...)",
	},
	{
		// The dyn twin of the row above: a receiver-less requirement reached
		// through a value that has erased its concrete type. The advice is
		// the only thing that gets the reader out, so the two compilers
		// must give the same one.
		name: "E021 associated function called through dyn",
		src: `struct Box { v: i32 }
trait Mk { function make(own b: Box): i32; }
struct P { v: i32 }
impl Mk for P { function make(own b: Box): i32 { return b.v; } }
function main(): i32 { var d: dyn Mk = P { v: 0 }; return d.make(Box { v: 3 }); }`,
		code:     "E021",
		spelling: "call it on a concrete type",
	},
	{
		name: "E021 receiver type not a valid receiver",
		src: `function (x: bool) foo(): i32 { return 0; }
function main(): i32 { return 0; }`,
		code:     "E021",
		spelling: "must be a struct, enum, array, slice, or built-in type",
	},
}

// hintTextDivergences records the rows where the two checkers are meant to
// say different things, keyed `<case>/<code>`. Not every difference is
// drift, and forcing these into lockstep would be the #6990 bug all over
// again: a hint must not name a fix the checker printing it still refuses.
//
// E045's two rows used to be the example — the self-host accepted only
// i32/string map keys, so repeating native's derive advice would have been
// exactly that bug. #7001 taught it the derived-key rule, the texts converged,
// and this table pruned them, which is the mechanism working rather than an
// exception to it.
//
// The table is exact in BOTH directions. A listed row that converges fails
// too, because the reason it was listed has gone away and nobody would
// otherwise notice.
// A row is also listed when the self-host checker does not report the
// diagnostic at all: "says nothing" is a difference in what the reader is
// told, and leaving it as a test SKIP would hide it. Listing it makes the
// self-host port's remaining hint gaps enumerable from one place.
var hintTextDivergences = map[string]string{
	"E038 print needs Display/E038": "the self-host checker has no Display check on `print` yet, so it reports nothing here " +
		"— its E038 sites are call-shape errors",
}

// TestSelfHostCheckerHintTextDifferential is the message-TEXT half of the
// checker differential, and the gap #7018 opened on: every existing gate
// extracts `E\d{3}` and compares CODE SETS, and checkStatusLines drops
// explanation text outright, so the two checkers were free to say
// arbitrarily different things — or the same wrong thing — while agreeing
// perfectly on what they reported. #6990 is what that cost: four hints
// named a spelling that does not compile, in both compilers, for a long
// time, with every differential green.
//
// It is deliberately narrow. Comparing ALL message text would pin prose
// that is allowed to differ and would fight the partial self-host checker;
// this corpus holds only diagnostics that tell the reader what to WRITE,
// which is the text where a difference is a bug rather than a style. Each
// row states the spelling it is about, and a row whose two sides are meant
// to differ is listed above with its reason.
//
// It runs the driver under the native interpreter — no cross toolchain, so
// unlike its x86_64-suffixed neighbours it runs on every host.
func TestSelfHostCheckerHintTextDifferential(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	driver, err := filepath.Abs("../../examples/self_host/checker_codes_run.fern")
	if err != nil {
		t.Fatalf("abs driver path: %v", err)
	}

	for _, tc := range hintTextCases {
		t.Run(tc.name, func(t *testing.T) {
			goDiags := byCode(goCheckerDiags(t, t.TempDir(), tc.src))
			goMsgs := goDiags[tc.code]
			if len(goMsgs) == 0 {
				t.Fatalf("native reported no %s for this case — the corpus row no longer exercises the hint it names", tc.code)
			}
			for _, m := range goMsgs {
				if !strings.Contains(m, tc.spelling) {
					t.Fatalf("native %s no longer names %q, so this row is not pinning advice any more: %s", tc.code, tc.spelling, m)
				}
			}

			cmd := exec.Command(interpBin, "-interp", driver)
			cmd.Stdin = strings.NewReader(tc.src)
			out, _ := cmd.Output()
			shMsgs := byCode(driverDiags(string(out)))[tc.code]

			key := tc.name + "/" + tc.code
			reason, listed := hintTextDivergences[key]
			switch {
			case listed && equalStrings(goMsgs, shMsgs):
				t.Errorf("%s no longer diverges — the two checkers now say the same thing, so drop %q from hintTextDivergences.\nlisted reason: %s\ntext: %s",
					tc.code, key, reason, strings.Join(goMsgs, "\n      "))
			case !listed && len(shMsgs) == 0:
				t.Errorf("the self-host checker reports no %s here, so it tells the reader nothing where native names %q. Fix the port, or list %q in hintTextDivergences with the reason.\nnative: %s",
					tc.code, tc.spelling, key, strings.Join(goMsgs, "\n        "))
			case !listed && !equalStrings(goMsgs, shMsgs):
				t.Errorf("%s message text has drifted between the two checkers.\nnative:    %s\nself-host: %s\n\nIf the difference is deliberate, add %q to hintTextDivergences with the reason; if it is not, move the two into step.",
					tc.code, strings.Join(goMsgs, "\n           "), strings.Join(shMsgs, "\n           "), key)
			}
		})
	}
}

// The corpus is only worth its runtime if it covers the hints that carry a
// spelling. This holds it against the native checker's own set: every
// message naming `@derive(` has to be represented by a case.
func TestHintTextCorpusCoversTheDeriveHints(t *testing.T) {
	want := map[string]bool{"E038": false, "E041": false, "E045": false}
	for _, tc := range hintTextCases {
		if _, ok := want[tc.code]; ok {
			want[tc.code] = true
		}
	}
	for code, covered := range want {
		if !covered {
			t.Errorf("%s names a `@derive` spelling in internal/checker but no hintTextCases row reaches it", code)
		}
	}
}
