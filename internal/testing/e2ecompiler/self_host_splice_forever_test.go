package e2ecompiler

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// `w.splice_forever(block)` on the host targets. A short block that divides
// neither 4 KiB nor the runtime's 128 KiB mapping checks the stream stays
// whole across every wrap, and a 70000-byte one checks a block that fills the
// mapping alone. Stdout is a pipe (the vmsplice path), then a regular file
// (through the runtime's own pipe). A pipe whose reader goes away, with
// SIGPIPE ignored, must answer the writer's own EPIPE (exit 4); /dev/full
// refuses splice before a byte moves, so it must answer Unsupported (exit 3)
// and leave the write to the caller. A build that answered Unsupported
// everywhere would exit at once and leave the pipe empty, so the byte checks
// also prove the kernel path ran.
const spliceForeverProg = `import "std/string";

function main(): i32 {
    let block: string = "abcdefghij\n";
    if (args().len() > 1) {
        block = "x".repeat(69999) + "\n";
    }
    match (stdout().splice_forever(block)) {
        Err(e) => {
            match (e) {
                Unsupported => { return 3; },
                _ => { return 4; }
            }
        },
        Ok(_) => { return 5; }
    }
}
`

// spliceForeverWant is the first n bytes the program must write.
func spliceForeverWant(long bool, n int) []byte {
	block := "abcdefghij\n"
	if long {
		block = strings.Repeat("x", 69999) + "\n"
	}
	return []byte(strings.Repeat(block, n/len(block)+1)[:n])
}

func TestSelfHostSpliceForever(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("vmsplice is Linux's; Darwin answers Unsupported (TestArm64DarwinSpliceForeverUnsupported)")
	}
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "sf.fern")
	if err := os.WriteFile(src, []byte(spliceForeverProg), 0o644); err != nil {
		t.Fatal(err)
	}
	const n = 3 << 20
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		if combined, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		argvOf := func(args ...string) []string {
			argv := append([]string{bin}, args...)
			if len(tg.runner) > 0 {
				argv = append(append([]string{}, tg.runner...), argv...)
			}
			return argv
		}
		command := func(args ...string) *exec.Cmd {
			argv := argvOf(args...)
			return exec.Command(argv[0], argv[1:]...)
		}
		for _, long := range []bool{false, true} {
			var args []string
			if long {
				args = []string{"long"}
			}
			want := spliceForeverWant(long, n)

			cmd := command(args...)
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, n)
			_, rerr := io.ReadFull(out, got)
			out.Close()
			_ = cmd.Wait()
			if rerr != nil {
				t.Errorf("%s long=%v: pipe: read %v, exit %d", tg.target, long, rerr, cmd.ProcessState.ExitCode())
			} else if !bytes.Equal(got, want) {
				t.Errorf("%s long=%v: pipe: the first %d bytes are not the block repeated", tg.target, long, n)
			}

			path := filepath.Join(dir, "out")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			cmd = command(args...)
			cmd.Stdout = f
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
				if fi, err := f.Stat(); err == nil && fi.Size() >= n {
					break
				}
			}
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			f.Close()
			fileBytes, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(fileBytes) < n || !bytes.Equal(fileBytes[:n], want) {
				t.Errorf("%s long=%v: file: %d bytes, not the block repeated", tg.target, long, len(fileBytes))
			}

			// `trap "" PIPE` sets SIGPIPE to ignored, which survives exec.
			cmd = exec.Command("sh", append([]string{"-c", `trap "" PIPE; exec "$@"`, "sh"}, argvOf(args...)...)...)
			out, err = cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadFull(out, make([]byte, 1<<16)); err != nil {
				t.Errorf("%s long=%v: closed pipe: read %v", tg.target, long, err)
			}
			out.Close()
			_ = cmd.Wait()
			if got := cmd.ProcessState.ExitCode(); got != 4 {
				t.Errorf("%s long=%v: closed pipe: exit %d, want 4 (EPIPE)", tg.target, long, got)
			}

			full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
			if err != nil {
				t.Skipf("no /dev/full: %v", err)
			}
			cmd = command(args...)
			cmd.Stdout = full
			_ = cmd.Run()
			full.Close()
			if got := cmd.ProcessState.ExitCode(); got != 3 {
				t.Errorf("%s long=%v: /dev/full: exit %d, want 3 (Unsupported)", tg.target, long, got)
			}
		}
	}
}
