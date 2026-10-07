package e2ecompiler

import (
	"bytes"
	goelf "debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/tables/symname"
)

// A fatal abort names its cause on stderr and walks the frame-pointer chain
// under it (#11405): the cause line, exit 134, `backtrace:` and one
// `0x<16 hex>` line per frame. FERN_BACKTRACE=0 on the compiler, or its
// -backtrace=false flag, keeps the cause line and the exit code and drops the
// walk at compile time.

var abortReportCases = []struct{ name, src, cause string }{
	{"array_oob", `function main(): i32 { let xs: i32[] = [10, 20, 30]; return xs[7]; }`, "fern: array index out of range"},
	{"string_slice_oob", `function main(): i32 { let s: string = "hi"; let t: str = slice_unchecked(s, 1, 9); return t.len(); }`, "fern: string index out of range"},
	{"slice_range_oob", `function main(): i32 { let xs: i32[] = [1, 2, 3]; let ys: [i32] = xs[1:9]; return ys.len(); }`, "fern: slice range out of bounds"},
	// A byte view's read is one unsigned compare, so both ends must still trap.
	{"byte_view_negative_oob", byteViewReadSrc("args().len() - 2"), "fern: array index out of range"},
	{"byte_view_past_end_oob", byteViewReadSrc("args().len() + 2"), "fern: array index out of range"},
}

const abortInBoundsSrc = `function main(): i32 { let xs: i32[] = [10, 20, 30]; return xs[1]; }`

// byteViewReadSrc reads a three-byte view at `index`; args() keeps the index
// from folding.
func byteViewReadSrc(index string) string {
	return `function at(bs: [u8], i: i32): u8 { return bs[i]; }
function main(): i32 { let xs: u8[] = [10, 20, 30]; return at(xs, ` + index + `) as i32; }`
}

var abortFrameRe = regexp.MustCompile(`(?m)^  0x[0-9a-f]{16}$`)

func checkAbortReports(t *testing.T, cli *selfHostCLI, target string) {
	t.Helper()
	for _, tc := range abortReportCases {
		t.Run(tc.name, func(t *testing.T) {
			stderr, code := cli.exitOf(t, tc.src, target)
			if code != 134 {
				t.Errorf("exit %d, want 134\nstderr:\n%s", code, stderr)
			}
			if !strings.HasPrefix(stderr, tc.cause+"\nbacktrace:\n") {
				t.Errorf("stderr does not open with the cause line and the backtrace header:\n%s", stderr)
			}
			if len(abortFrameRe.FindAllString(stderr, -1)) == 0 {
				t.Errorf("no frame under the backtrace header:\n%s", stderr)
			}
		})
	}
	t.Run("in_bounds", func(t *testing.T) {
		stderr, code := cli.exitOf(t, abortInBoundsSrc, target)
		if code != 20 || stderr != "" {
			t.Errorf("exit %d, stderr %q; want 20 and nothing on stderr", code, stderr)
		}
	})
	t.Run("byte_view_last_in_bounds", func(t *testing.T) {
		stderr, code := cli.exitOf(t, byteViewReadSrc("args().len() + 1"), target)
		if code != 30 || stderr != "" {
			t.Errorf("exit %d, stderr %q; want 30 and nothing on stderr", code, stderr)
		}
	})
}

func TestSelfHostAbortReportsX86_64(t *testing.T) {
	checkAbortReports(t, buildSelfHostCLI(t), "x86-64-linux")
}

func TestSelfHostAbortReportsArm64(t *testing.T) {
	arm64Tooling(t)
	checkAbortReports(t, buildSelfHostCLI(t), "arm64-linux")
}

// deepAbortSrc traps three frames deep. @noinline keeps the chain, since the
// subject is the walk: inlined into main there would be one frame to report.
// inner is a leaf the x86-64 emitter runs without a frame, which is the case
// the walk has to recover the immediate caller for.
const deepAbortSrc = `@noinline function inner(xs: i32[]): i32 { return xs[7]; }
@noinline function mid(xs: i32[]): i32 { return inner(xs); }
function main(): i32 { let xs: i32[] = [1, 2, 3]; return mid(xs); }
`

// abortFrames runs bin (through runner, if any) and returns the functions its
// backtrace's return addresses fall in, in the order printed, resolved against
// the ELF symbol table, plus the whole of stderr.
func abortFrames(t *testing.T, bin string, run *exec.Cmd) ([]string, string) {
	t.Helper()
	var stderr bytes.Buffer
	run.Stderr = &stderr
	_ = run.Run()
	if run.ProcessState == nil || run.ProcessState.ExitCode() != 134 {
		t.Fatalf("exit %v, want 134\nstderr:\n%s", run.ProcessState, stderr.String())
	}
	f, err := goelf.Open(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	syms, err := f.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	var fns []goelf.Symbol
	for _, s := range syms {
		if goelf.ST_TYPE(s.Info) == goelf.STT_FUNC || s.Name == "_start" || strings.HasPrefix(s.Name, "__fn_") {
			if !strings.Contains(s.Name, ".") {
				fns = append(fns, s)
			}
		}
	}
	sort.Slice(fns, func(i, j int) bool { return fns[i].Value < fns[j].Value })
	resolve := func(addr uint64) string {
		name := "?"
		for _, s := range fns {
			if s.Value <= addr {
				name = s.Name
			}
		}
		if src, ok := symname.Source(name); ok {
			return src
		}
		return name
	}
	var frames []string
	for _, line := range abortFrameRe.FindAllString(stderr.String(), -1) {
		var addr uint64
		for _, c := range strings.TrimSpace(line)[2:] {
			addr <<= 4
			switch {
			case c >= '0' && c <= '9':
				addr |= uint64(c - '0')
			default:
				addr |= uint64(c-'a') + 10
			}
		}
		frames = append(frames, resolve(addr))
	}
	return frames, stderr.String()
}

func checkAbortBacktrace(t *testing.T, frames []string, stderr string) {
	t.Helper()
	want := []string{"mid", "main", "_start"}
	if len(frames) < len(want) {
		t.Fatalf("got %d frames %v, want at least %d\nstderr:\n%s", len(frames), frames, len(want), stderr)
	}
	for i, w := range want {
		if frames[i] != w {
			t.Errorf("frame %d resolves to %q, want %q (frames %v)\nstderr:\n%s", i, frames[i], w, frames, stderr)
		}
	}
}

// compileDeep builds deepAbortSrc for target through the CLI's own
// assembler and linker, with extra flags, and env added to the compiler's
// environment, returning the binary.
func compileDeep(t *testing.T, cli *selfHostCLI, target string, flags []string, env ...string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "deep.fern")
	if err := os.WriteFile(src, []byte(deepAbortSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "deep")
	args := append(append([]string{}, flags...), "-g", "-target", target, "-o", bin, src, cli.stdlib)
	cmd := runX86_64Bin(cli.runner, cli.bin, args...)
	cmd.Env = childEnv(env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host CLI %v: %v\n%s", args, err, out)
	}
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestSelfHostAbortBacktraceX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	bin := compileDeep(t, cli, "x86-64-linux", nil)
	frames, stderr := abortFrames(t, bin, runX86_64Bin(cli.runner, bin))
	checkAbortBacktrace(t, frames, stderr)
}

func TestSelfHostAbortBacktraceArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	bin := compileDeep(t, cli, "arm64-linux", nil)
	frames, stderr := abortFrames(t, bin, runArm64Bin(qemu, bin))
	checkAbortBacktrace(t, frames, stderr)
}

// The opt-out, by both routes: the cause line and the status stay, the header
// and every address go.
func checkAbortBacktraceOff(t *testing.T, cli *selfHostCLI, target string, run func(bin string) *exec.Cmd) {
	t.Helper()
	for _, tc := range []struct {
		name  string
		flags []string
		env   []string
	}{
		{"env", nil, []string{"FERN_BACKTRACE=0"}},
		{"flag", []string{"-backtrace=false"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := compileDeep(t, cli, target, tc.flags, tc.env...)
			cmd := run(bin)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 134 {
				t.Errorf("exit %d, want 134", code)
			}
			if stderr.String() != "fern: array index out of range\n" {
				t.Errorf("stderr with the walk suppressed:\n%s\nwant the cause line alone", stderr.String())
			}
		})
	}
}

func TestSelfHostAbortBacktraceOffX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	checkAbortBacktraceOff(t, cli, "x86-64-linux", func(bin string) *exec.Cmd { return runX86_64Bin(cli.runner, bin) })
}

func TestSelfHostAbortBacktraceOffArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	checkAbortBacktraceOff(t, cli, "arm64-linux", func(bin string) *exec.Cmd { return runArm64Bin(qemu, bin) })
}
