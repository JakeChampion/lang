package e2eselfhost

import (
	"os/exec"
	"testing"
)

// TestSelfHostTomlCRLF pins the manifest reader's line handling on CRLF input
// (examples/self_host/fern_toml.fern via util.trim — SH-020,
// docs/SELF-HOST-AUDIT.md T1).
//
// fern_toml splits on '\n' alone, so on a CRLF file every line arrives with a
// trailing '\r' and trim is what has to remove it. It did not: fern_toml kept a
// private trim stripping only space and tab, where the tree's other two copies
// also stripped '\r'. parse_lock compares a line for exact equality against
// "[[package]]", so on CRLF that header never matched, `have` never went true,
// and every entry was dropped — a CRLF fern.lock parsed to ZERO packages and the
// loader saw an empty lock instead of an error.
//
// Only the lock half was affected, which is why this went unnoticed: the
// manifest half reads its values through quoted_value, which scans for the
// closing quote and so steps over a trailing '\r'. Both halves are asserted here
// anyway — the manifest rows are what catch a future trim change that strips too
// MUCH (a '\r' inside a quoted value is data, not framing).
//
// The driver parses the same two documents twice, LF and CRLF, and prints what
// each yields; the two halves must agree row for row.
func TestSelfHostTomlCRLF(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("toml_crlf_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "toml_crlf_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "toml_crlf_run.fern", "toml_crlf_run")

	const want = "LF   name=acme\n" +
		"LF   deps=2\n" +
		"LF   dep dbl kind=path version= path=../dbl\n" +
		"LF   dep shout kind=version version=1.2.3 path=\n" +
		"LF   member a\n" +
		"LF   member b/c\n" +
		"CRLF name=acme\n" +
		"CRLF deps=2\n" +
		"CRLF dep dbl kind=path version= path=../dbl\n" +
		"CRLF dep shout kind=version version=1.2.3 path=\n" +
		"CRLF member a\n" +
		"CRLF member b/c\n" +
		"LF   entries=2\n" +
		"LF   entry dbl version=1.0.0 path=../dbl hash=\n" +
		"LF   entry shout version=1.2.3 path= hash=sha256:abc123\n" +
		"CRLF entries=2\n" +
		"CRLF entry dbl version=1.0.0 path=../dbl hash=\n" +
		"CRLF entry shout version=1.2.3 path= hash=sha256:abc123\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("toml_crlf_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("CRLF manifest/lock parse mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
