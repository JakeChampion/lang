package interp

import "testing"

// A command that cannot run exits 127 with `cmd: strerror(errno)` on stderr,
// as a shell reports it and as the compiled runtime's child does (#11855).
func TestSubprocessSpawnFailureReportsErrno(t *testing.T) {
	_, out := runCapture(t, `function main(): i32 {
    let r: ProcessResult = subprocess("./fern-no-such-binary", ["x"], "");
    print(r.stderr);
    return r.exit_code;
}`)
	if want := "./fern-no-such-binary: No such file or directory\n\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}
