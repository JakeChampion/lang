package checker

import (
	"fmt"
	"strings"
	"testing"
)

func TestNoRCAcceptsScalarCode(t *testing.T) {
	src := `const UART: usize = 4096;
@norc
function put(b: i32): void {
  unsafe {
    while ((__mmio_load32(UART + 24) & 32) != 0) {
    }
    __mmio_store8(UART, b);
  }
}
@norc
function sum(n: i32): i32 {
  let t = 0;
  let i = 0;
  while (i < n) {
    t += i;
    i += 1;
  }
  put(t);
  return t as i32;
}
@norc
function after_shadow(): i32 {
  if (true) { let sum = 1; }
  return sum(2);
}
function main(): i32 { return sum(5) - 10; }
`
	if errs := checkErrors(t, src); len(errs) != 0 {
		t.Fatalf("scalar @norc code refused: %v", errs)
	}
}

func TestNoRCRefusesEachBreach(t *testing.T) {
	src := `function helper(x: i32): i32 {
  return x + 1;
}
@norc
function f(s: string, n: i32): i32 {
  let xs: i32[] = [1, 2];
  let t = "hi";
  let u = helper(n);
  for i in 0..n {
    u += i;
  }
  return u + xs.len();
}
function main(): i32 { return f("a", 1); }
`
	want := []struct{ pos, msg string }{
		{"5:12", `takes "s" of type string, which is not a scalar`},
		{"6:3", `declares "xs" of type i32[], which is not a scalar`},
		{"6:19", "may not use an array literal"},
		{"7:11", "may not use a string"},
		{"8:17", "may call only `@norc` functions and the raw memory intrinsics, not \"helper\""},
		{"9:3", "may not use a `for` loop, which iterates a value; use `while`"},
		{"12:20", "may not call a method or a function value"},
	}
	errs := checkErrors(t, src)
	got := map[string]string{}
	for _, e := range errs {
		if e.ErrCode == "E102" {
			got[e.Pos.String()] = e.Msg
		}
	}
	for _, w := range want {
		msg, ok := got[w.pos]
		if !ok || !strings.HasSuffix(msg, w.msg) || !strings.HasPrefix(msg, "`@norc` function \"f\" ") {
			t.Errorf("at %s: got %q, want E102 ending %q (all: %v)", w.pos, msg, w.msg, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d E102 diagnostics, want %d: %v", len(got), len(want), got)
	}
}

func TestNoRCRefusesAMethod(t *testing.T) {
	src := `struct Box { v: i32 }
@norc
function (b: Box) get(): i32 {
  return 1;
}
function main(): i32 { return 0; }
`
	for _, e := range checkErrors(t, src) {
		if e.ErrCode == "E102" && strings.HasSuffix(e.Msg, "must be a plain function, not a method or a generic function") {
			return
		}
	}
	t.Error("a @norc method was accepted")
}

func TestNoRCRefusesFunctionValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"shadowed callee", "let allowed = allocating; return allowed();", "may not call a method or a function value"},
		{"inferred function value", "let value = allowed; return 0;", `uses "allowed" of type () => i32, which is not a scalar`},
		{"bare function value", "allowed; return 0;", `uses "allowed" of type () => i32, which is not a scalar`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "@norc\nfunction allowed(): i32 { return 1; }\n" +
				"function allocating(): i32 { let xs = [1]; return xs.len(); }\n" +
				"@norc\nfunction bad(): i32 { " + tc.body + " }\n" +
				"function main(): i32 { return 0; }\n"
			errs := checkErrors(t, src)
			for _, err := range errs {
				if err.ErrCode == "E102" && strings.HasSuffix(err.Msg, tc.want) {
					return
				}
			}
			t.Fatalf("want E102 ending %q, got %v", tc.want, errs)
		})
	}
}

func TestNoRCRefusesNonScalarReads(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prelude string
		body    string
		want    string
	}{
		{"enum", "enum Kind { One, Two }", "One;", `uses "One" of type Kind, which is not a scalar`},
		{"constant after local scope", `const text: string = "heap";`, "if (true) { let text = 1; } text;", `uses "text" of type string, which is not a scalar`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.prelude + "\n@norc\nfunction bad(): i32 { " + tc.body +
				" return 0; }\nfunction main(): i32 { return 0; }\n"
			errs := checkErrors(t, src)
			if len(errs) != 1 || errs[0].ErrCode != "E102" || !strings.HasSuffix(errs[0].Msg, tc.want) {
				t.Fatalf("want one E102 ending %q, got %v", tc.want, errs)
			}
		})
	}
}

func TestEarlyHookRules(t *testing.T) {
	const main = "\nfunction main(): i32 { return 0; }\n"
	ok := "@early\nfunction setup(): void {\n  unsafe { __sysreg_write(\"tpidr_el1\", 1i64); }\n}" + main
	if errs := checkErrors(t, ok); len(errs) != 0 {
		t.Fatalf("a well-formed @early hook refused: %v", errs)
	}
	for name, c := range map[string]struct{ src, want string }{
		"a parameter":   {"@early\nfunction h(n: i32): void {}" + main, "must take no parameters and return void"},
		"a result":      {"@early\nfunction h(): i32 { return 1; }" + main, "must take no parameters and return void"},
		"two hooks":     {"@early\nfunction a(): void {}\n@early\nfunction b(): void {}" + main, "is a second hook"},
		"implies @norc": {"@early\nfunction h(): void { let s = \"x\"; }" + main, "`@early` function \"h\""},
	} {
		errs := checkErrors(t, c.src)
		if len(errs) == 0 || !strings.Contains(fmt.Sprint(errs), c.want) {
			t.Errorf("%s: errors %v, want %q", name, errs, c.want)
		}
	}
}

func TestSecondaryEntryRules(t *testing.T) {
	const main = "\nfunction main(): i32 { return 0; }\n"
	if errs := checkErrors(t, "@secondary\nfunction run(context: usize): void {}"+main); len(errs) != 0 {
		t.Fatalf("a well-formed @secondary entry refused: %v", errs)
	}
	for name, c := range map[string]struct{ src, want string }{
		"no parameter":  {"@secondary\nfunction s(): void {}" + main, "must take one usize, the context id, and return void"},
		"an i32":        {"@secondary\nfunction s(n: i32): void {}" + main, "must take one usize, the context id, and return void"},
		"a result":      {"@secondary\nfunction s(c: usize): i32 { return 1; }" + main, "must take one usize, the context id, and return void"},
		"two entries":   {"@secondary\nfunction a(c: usize): void {}\n@secondary\nfunction b(c: usize): void {}" + main, "is a second entry"},
		"implies @norc": {"@secondary\nfunction s(c: usize): void { let t = \"x\"; }" + main, "`@secondary` function \"s\""},
	} {
		errs := checkErrors(t, c.src)
		if len(errs) == 0 || !strings.Contains(fmt.Sprint(errs), c.want) {
			t.Errorf("%s: errors %v, want %q", name, errs, c.want)
		}
	}
}
