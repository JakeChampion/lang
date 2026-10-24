package monomorph

import (
	"errors"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

func TestUserErrorsFromMonomorphAreCodedDiagnostics(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantCode string
		wantMsg  string
	}{
		{
			name: "trait bound violated generic-into-generic",
			src: `trait Named { function name(self: Self): string; }
struct B { v: i32 }
function inner[U: Named](x: U): string { return x.name(); }
function outer[T](x: T): string { return inner(x); }
function main(): i32 { print(outer(B { v: 2 })); return 0; }`,
			wantCode: "E021",
			wantMsg:  "does not implement trait Named required by inner",
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
function main(): i32 { let s = id(S { v: 3 }); return s.v; }`},
	}
	for _, s := range sources {
		t.Run(s.name, func(t *testing.T) {
			if err := runMonomorph(t, s.src); err != nil {
				t.Errorf("rejected a well-typed program: %v", err)
			}
		})
	}
}

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
	if !strings.Contains(msg, "U = C does not implement") {
		t.Errorf("the C violation is missing:\n%s", msg)
	}
}

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
