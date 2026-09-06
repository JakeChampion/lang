package monomorph

import (
	"errors"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/modload"
)

// Two ordinary user type errors used to be reported as
//
//	monomorph: re-check failed (compiler bug): …
//
// with no diagnostic code — a banner accusing the compiler for a mistake in
// the author's own program, and nothing to look up (#8452). Both are checks
// that cannot run before instantiation:
//
//   - `Cell[T]` over a composite, where T is only known once substituted;
//   - a trait bound violated through a generic-into-generic call, which the
//     call-site check skips because the argument is still a type parameter
//     and leaves "for the eventual monomorphic call" — nothing was that call,
//     because monomorph clears the clone's TypeParams and the bound goes with
//     them, so the body failed on whatever the missing impl was needed for.
//
// spec/diagnostics.md's premise is that every user-facing error has a stable
// code, so each row asserts the code AND that the banner is gone.
func TestUserErrorsFromMonomorphAreCodedDiagnostics(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantCode string
		wantMsg  string
	}{
		{
			name: "Cell over a composite reached through a generic",
			src: `struct Point { x: i32, y: i32 }
function mk[T](v: T): Cell[T] { return cell_new(v); }
function main(): i32 {
  var c = mk(Point { x: 1, y: 2 });
  return c.get().x;
}`,
			wantCode: "E057",
			wantMsg:  "a cell's element type must be a scalar",
		},
		{
			name: "trait bound violated generic-into-generic",
			src: `trait Named { function name(self: Self): string; }
struct B { v: i32 }
function inner[U: Named](x: U): string { return x.name(); }
function outer[T](x: T): string { return inner(x); }
function main(): i32 { print(outer(B { v: 2 })); return 0; }`,
			wantCode: "E021",
			// The bound, named as a bound. Reporting the missing FIELD the
			// method lookup went looking for leaked how bounds are implemented.
			wantMsg: "does not implement trait Named required by inner",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := runMonomorph(t, c.src)
			if err == nil {
				t.Fatal("accepted; want a diagnostic")
			}
			msg := err.Error()
			if strings.Contains(msg, "compiler bug") {
				t.Errorf("still blames the compiler for a user error:\n%s", msg)
			}
			if strings.Contains(msg, "monomorph:") {
				t.Errorf("a pass name reached the user:\n%s", msg)
			}
			if !hasCode(err, c.wantCode) {
				t.Errorf("want code %s, got:\n%s", c.wantCode, msg)
			}
			if !strings.Contains(msg, c.wantMsg) {
				t.Errorf("want message containing %q, got:\n%s", c.wantMsg, msg)
			}
		})
	}
}

// The bound check monomorph now runs must not fire on a program that
// satisfies its bounds, and must not disturb the call-site check that already
// worked. The last row is the one that would regress if the deferred check
// were made unconditional rather than skipping a still-parametric argument.
func TestMonomorphBoundCheckAcceptsSatisfiedBounds(t *testing.T) {
	sources := []struct{ name, src string }{
		{"direct call with the impl present", `trait Named { function name(self: Self): string; }
struct A { v: i32 }
impl Named for A { function name(self: Self): string { return "a"; } }
function inner[U: Named](x: U): string { return x.name(); }
function main(): i32 { print(inner(A { v: 1 })); return 0; }`},
		{"generic into generic with the impl present", `trait Named { function name(self: Self): string; }
struct A { v: i32 }
impl Named for A { function name(self: Self): string { return "a"; } }
function inner[U: Named](x: U): string { return x.name(); }
function outer[T](x: T): string { return inner(x); }
function main(): i32 { print(outer(A { v: 1 })); return 0; }`},
		{"an unbounded generic is not asked to satisfy anything", `function id[T](x: T): T { return x; }
struct S { v: i32 }
function main(): i32 { var s = id(S { v: 3 }); return s.v; }`},
	}
	for _, s := range sources {
		t.Run(s.name, func(t *testing.T) {
			if err := runMonomorph(t, s.src); err != nil {
				t.Errorf("rejected a well-typed program: %v", err)
			}
		})
	}
}

// One unsatisfied bound is one diagnostic, however many times the offending
// call is instantiated: a generic calling a bounded generic reaches the
// collection once per clone of the caller.
func TestMonomorphBoundViolationReportedOnce(t *testing.T) {
	src := `trait Named { function name(self: Self): string; }
struct B { v: i32 }
struct C { v: i32 }
function inner[U: Named](x: U): string { return x.name(); }
function outer[T](x: T): string { return inner(x); }
function main(): i32 { print(outer(B { v: 1 })); print(outer(B { v: 2 })); print(outer(C { v: 3 })); return 0; }`
	err := runMonomorph(t, src)
	if err == nil {
		t.Fatal("accepted; want diagnostics")
	}
	msg := err.Error()
	if n := strings.Count(msg, "U = B does not implement"); n != 1 {
		t.Errorf("the B violation is reported %d times, want 1:\n%s", n, msg)
	}
	// C is a different violation and keeps its own diagnostic — dedupe must
	// not collapse distinct errors.
	if !strings.Contains(msg, "U = C does not implement") {
		t.Errorf("the C violation is missing:\n%s", msg)
	}
}

// hasDiagnosticCode is what decides whether a re-check failure is the author's
// problem or the compiler's, so its traversal is pinned directly: diag.Errors
// is the shape the checker actually returns, and its own As delegates to
// entries implementing As — which *checker.Error does not — so a traversal
// resting on errors.As alone answers no for every real failure.
func TestHasDiagnosticCodeSeesThroughTheCheckersErrorShape(t *testing.T) {
	coded := &checker.Error{Msg: "bad", ErrCode: "E057"}
	bare := &checker.Error{Msg: "internal"}
	plain := errors.New("invariant broken")
	for _, c := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"a coded checker error", coded, true},
		{"a checker error with no code", bare, false},
		{"a plain error", plain, false},
		{"diag.Errors carrying a coded one", diag.Errors{bare, coded}, true},
		{"diag.Errors carrying none", diag.Errors{bare, plain}, false},
		{"a wrapped coded error", errWrap{coded}, true},
	} {
		if got := hasDiagnosticCode(c.err); got != c.want {
			t.Errorf("%s: hasDiagnosticCode = %v, want %v", c.name, got, c.want)
		}
	}
}

type errWrap struct{ inner error }

func (e errWrap) Error() string { return "wrapped: " + e.inner.Error() }
func (e errWrap) Unwrap() error { return e.inner }

// runMonomorph loads, checks and monomorphises src, returning the first error
// any of the three produced — which is what a user sees.
func runMonomorph(t *testing.T, src string) error {
	t.Helper()
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		return err
	}
	info, err := checker.Check(prog)
	if err != nil {
		return err
	}
	return Run(prog, info)
}

// codesOf collects the diagnostic codes err carries. The code is a FIELD on
// checker.Error, not part of its Error() text — the `error[E057]` header is
// the diag formatter's doing — so a string search would pass for a message
// that merely mentioned a code and fail for every real one.
func codesOf(err error) []string {
	switch e := err.(type) {
	case nil:
		return nil
	case *checker.Error:
		if e.ErrCode == "" {
			return nil
		}
		return []string{e.ErrCode}
	case diag.Errors:
		var out []string
		for _, one := range e {
			out = append(out, codesOf(one)...)
		}
		return out
	}
	var one *checker.Error
	if errors.As(err, &one) && one.ErrCode != "" {
		return []string{one.ErrCode}
	}
	return codesOf(errors.Unwrap(err))
}

func hasCode(err error, code string) bool {
	for _, c := range codesOf(err) {
		if c == code {
			return true
		}
	}
	return false
}
