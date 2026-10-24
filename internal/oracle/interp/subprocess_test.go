package interp

import "testing"

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
