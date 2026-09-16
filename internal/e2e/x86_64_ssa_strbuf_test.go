package e2e

import "testing"

// The global string builder on x86-64 SSA (strbuf_reset / strbuf_append /
// strbuf_take, the family the self-hosted compiler writes its output
// through): a build across many appends, a take that hands back exactly
// those bytes, an independent build after it, and a build that outgrows the
// first buffer several times over. The flat build's answer is the reference.
func TestX86_64SSAStrbufBuildsAndTakes(t *testing.T) {
	runner, ok := x86Runner()
	if !ok {
		t.Skip("no way to run x86-64 binaries on this host")
	}
	fern := buildFernCLI(t)
	src := `function main(): i32 {
  strbuf_reset();
  strbuf_append("Hello, ");
  strbuf_append("Fern");
  strbuf_append("!");
  var s: string = strbuf_take();
  if (s != "Hello, Fern!") { return 1; }
  var i: i32 = 0;
  while (i < 5) { strbuf_append("ab"); i = i + 1; }
  var t: string = strbuf_take();
  if (t != "ababababab") { return 2; }
  i = 0;
  while (i < 100000) { strbuf_append("0123456789"); i = i + 1; }
  var big: string = strbuf_take();
  if (big.len() != 1000000) { return 3; }
  if (big[0] != 48 || big[999999] != 57) { return 4; }
  strbuf_append("tail");
  var after: string = strbuf_take();
  if (after != "tail") { return 5; }
  stdout().write(s + " " + t + " " + after + "\n");
  return 0;
}
`
	ssaOut, ssaErr, ssaCode := buildRunX86(t, fern, runner, src, true)
	flatOut, flatErr, flatCode := buildRunX86(t, fern, runner, src, false)
	if ssaCode != 0 || ssaOut != flatOut || ssaCode != flatCode {
		t.Errorf("ssa exit %d %q %s\nflat exit %d %q %s", ssaCode, ssaOut, ssaErr, flatCode, flatOut, flatErr)
	}
}
