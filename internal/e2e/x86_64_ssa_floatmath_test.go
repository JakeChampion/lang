package e2e

import "testing"

// The f64 math helpers and __memcpy on `-backend ssa -target x86-64-linux`,
// against the DEFAULT x86-64 backend on stdout and exit status. The
// transcendentals share one kernel bundle between the two backends, so their
// answers are the same bits, not merely close; the rounding family is one
// SSE4.1 instruction on each side.
var x86SSAFloatMathCases = []struct {
	name string
	src  string
}{
	{
		name: "rounding_family_abs_and_sqrt",
		src: `import "std/float";
function main(): i32 {
  var xs: f64[] = [2.5, -2.5, 0.49, -0.5, 1.75, -1.75, 6.25, 0.0 - 0.0];
  var i: i32 = 0;
  while (i < xs.len()) {
    var x = xs[i];
    stdout().write(x.to_string_prec(2) + " floor=" + x.floor().to_string_prec(2)
      + " ceil=" + x.ceil().to_string_prec(2) + " trunc=" + x.trunc().to_string_prec(2)
      + " round=" + x.round().to_string_prec(2) + " abs=" + x.abs().to_string_prec(2)
      + " sqrt=" + x.abs().sqrt().to_string_prec(6) + "\n");
    i = i + 1;
  }
  return 0;
}`,
	},
	{
		name: "transcendentals_are_the_same_bits",
		src: `import "std/float";
function main(): i32 {
  var xs: f64[] = [0.0, 0.5, 1.0, 2.0, 3.75, 10.0, 100.0, 0.0 - 1.0, 1000000.5];
  var i: i32 = 0;
  while (i < xs.len()) {
    var x = xs[i];
    stdout().write(x.to_string_prec(2) + " exp=" + x.exp().to_string_prec(12)
      + " sin=" + x.sin().to_string_prec(12) + " cos=" + x.cos().to_string_prec(12)
      + " tan=" + x.tan().to_string_prec(12) + "\n");
    if (x > 0.0) {
      stdout().write("  log=" + x.log().to_string_prec(12) + " log2=" + x.log2().to_string_prec(12)
        + " pow(x,2.5)=" + x.pow(2.5).to_string_prec(12) + " cbrt=" + x.cbrt().to_string_prec(12)
        + " hypot(x,3)=" + x.hypot(3.0).to_string_prec(12) + "\n");
    }
    i = i + 1;
  }
  var neg: f64 = 0.0 - 2.0;
  stdout().write("pow(-2,3)=" + neg.pow(3.0).to_string_prec(6) + " pow(-2,0.5)_nan=" + neg.pow(0.5).is_nan().to_string() + "\n");
  var one: f64 = 1.0;
  var zero: f64 = 0.0;
  stdout().write("log(0)_inf=" + zero.log().is_inf().to_string() + " exp(1000)_inf=" + (one * 1000.0).exp().is_inf().to_string() + "\n");
  return 0;
}`,
	},
	{
		// string.bytes() is std/string's __memcpy caller: an owned copy of the
		// bytes, summed back and rebuilt into the same text.
		name: "string_bytes_copies_through_memcpy",
		src: `import "std/string";
function main(): i32 {
  var s: string = "hello, memcpy";
  var b: u8[] = s.bytes();
  var total: i32 = 0;
  var i: i32 = 0;
  while (i < b.len()) { total = total + (b[i] as i32); i = i + 1; }
  stdout().write(s + "\n");
  stdout().write(string_from_bytes_unchecked(b) + "\n");
  return total % 256;
}`,
	},
}

func TestX86_64SSAFloatMathMatchesDefaultBackend(t *testing.T) {
	runner, ok := x86Runner()
	if !ok {
		t.Skip("no way to run x86-64 binaries on this host")
	}
	fern := buildFernCLI(t)

	for _, c := range x86SSAFloatMathCases {
		t.Run(c.name, func(t *testing.T) {
			ssaOut, ssaErr, ssaCode := buildRunX86(t, fern, runner, c.src, true)
			flatOut, flatErr, flatCode := buildRunX86(t, fern, runner, c.src, false)

			if ssaCode != flatCode {
				t.Errorf("exit status: ssa=%d flat=%d", ssaCode, flatCode)
			}
			if ssaOut != flatOut {
				t.Errorf("stdout differs:\n ssa=%q\nflat=%q", ssaOut, flatOut)
			}
			if ssaErr != flatErr {
				t.Errorf("stderr differs:\n ssa=%q\nflat=%q", ssaErr, flatErr)
			}
			if ssaOut == "" {
				t.Errorf("no output at all: the program did not reach its prints")
			}
		})
	}
}
