package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

const signalDispositionSrc = `function main(): i32 {
    if (args().len() == 2) { signal_ignore(13); }
    if (args().len() == 3) { signal_ignore(13); signal_default(13); }
    let i: i32 = 0;
    while (i < 200000) {
        print("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx");
        i = i + 1;
    }
    return 7;
}`

var signalDispositionCases = []struct {
	name string
	argv []string
	want int
}{
	{"default disposition kills the writer", nil, 141},
	{"signal_ignore drops the failing writes", []string{"ignore"}, 7},
	{"signal_default restores the kill", []string{"ignore", "restore"}, 141},
}

func runWithStdoutClosed(t *testing.T, argv ...string) int {
	t.Helper()
	const script = `"$@" | head -c 1 >/dev/null; exit ${PIPESTATUS[0]}`
	cmd := exec.Command("bash", append([]string{"-c", script, "signal-disposition"}, argv...)...)
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode()
}

func TestInterpSignalDisposition(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the SIGPIPE observable needs a POSIX signal")
	}
	dir := t.TempDir()
	fern := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fern, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(signalDispositionSrc+"\n"), 0o644); err != nil {
		t.Fatalf("write prog.fern: %v", err)
	}
	for _, tc := range signalDispositionCases {
		argv := []string{fern, "-interp", src}
		if len(tc.argv) != 0 {
			argv = append(append(argv, "--"), tc.argv...)
		}
		if got := runWithStdoutClosed(t, argv...); got != tc.want {
			t.Errorf("%s: exit = %d, want %d (#8792)", tc.name, got, tc.want)
		}
	}
}

func TestWASMSignalDispositionIsANoOp(t *testing.T) {
	src := `function main(): i32 {
    let n: i32 = 40;
    signal_ignore(13);
    n = n + 1;
    signal_default(2);
    return n + 1;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("wasm signal dispositions: got %d, want 42 (both calls are no-ops that consume their argument; #8792)", got)
	}
}

func TestInterpSignalDispositionOutOfRangeDoesNotHang(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal numbering")
	}
	dir := t.TempDir()
	fern := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fern, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}
	for _, sig := range []string{"0 - 1", "0", "65", "99", "1000000"} {
		for _, fn := range []string{"signal_ignore", "signal_default"} {
			src := filepath.Join(dir, "prog.fern")
			body := "function main(): i32 { " + fn + "(" + sig + "); return 7; }\n"
			if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
				t.Fatalf("write prog.fern: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			err := exec.CommandContext(ctx, fern, "-interp", src).Run()
			timedOut := ctx.Err() != nil
			cancel()
			if timedOut {
				t.Errorf("%s(%s) HUNG — the out-of-range guard is gone (#8792)", fn, sig)
				continue
			}
			if code := exitCodeOf(err); code != 7 {
				t.Errorf("%s(%s) exit = %d, want 7", fn, sig, code)
			}
		}
	}
}
