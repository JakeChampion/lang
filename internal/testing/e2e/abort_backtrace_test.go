package e2e

import (
	"bytes"
	goelf "debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/tables/symname"
)

var backtraceHexRe = regexp.MustCompile(`0x[0-9a-f]{16}`)

const deepAbortSrc = `@noinline function step(n: i64): i64 { return n + 1; }
@noinline function inner(xs: i32[]): i32 {
  let a: i64 = step(xs.len() as i64);
  let b: i64 = step(a);
  let c: i64 = step(b);
  let d: i64 = step(c);
  let e: i64 = step(d);
  let f: i64 = step(e);
  let g: i64 = step(f);
  let h: i64 = step(g);
  let i: i64 = step(h);
  let j: i64 = step(i);
  let k: i64 = step(j);
  let l: i64 = step(k);
  let m: i64 = step(l);
  return ((xs[7]) as i64 + a + b + c + d + e + f + g + h + i + j + k + l + m) as i32;
}
@noinline function mid(xs: i32[]): i32 {
  let a: i64 = step(xs.len() as i64);
  let b: i64 = step(a);
  let c: i64 = step(b);
  let d: i64 = step(c);
  let e: i64 = step(d);
  let f: i64 = step(e);
  let g: i64 = step(f);
  let h: i64 = step(g);
  let i: i64 = step(h);
  let j: i64 = step(i);
  let k: i64 = step(j);
  let l: i64 = step(k);
  let m: i64 = step(l);
  return ((inner(xs)) as i64 + a + b + c + d + e + f + g + h + i + j + k + l + m) as i32;
}
function main(): i32 {
  let a: i64 = step(args().len() as i64);
  let b: i64 = step(a);
  let c: i64 = step(b);
  let d: i64 = step(c);
  let e: i64 = step(d);
  let f: i64 = step(e);
  let g: i64 = step(f);
  let h: i64 = step(g);
  let i: i64 = step(h);
  let j: i64 = step(i);
  let k: i64 = step(j);
  let l: i64 = step(k);
  let m: i64 = step(l);
  let xs: i32[] = [1, 2, 3];
  return ((mid(xs)) as i64 + a + b + c + d + e + f + g + h + i + j + k + l + m) as i32;
}
`

func buildAndAbort(t *testing.T, target string, run func(bin string) *exec.Cmd, extraArgs, extraEnv []string) (string, string) {
	t.Helper()
	bin := buildFernCLI(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "deep.fern")
	if err := os.WriteFile(p, []byte(deepAbortSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "deep.bin")
	args := append([]string{"-g", "-target", target, "-o", out}, extraArgs...)
	build := exec.Command(bin, append(args, p)...)
	build.Env = append(os.Environ(), extraEnv...)
	if o, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, o)
	}

	cmd := run(out)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 134 {
		t.Errorf("exit = %d, want 134", code)
	}
	errOut := stderr.String()
	if !strings.Contains(errOut, "fern: array index out of range") {
		t.Errorf("stderr missing cause line:\n%s", errOut)
	}
	return out, errOut
}

func runBacktraceCase(t *testing.T, target string, run func(bin string) *exec.Cmd) {
	t.Helper()
	out, errOut := buildAndAbort(t, target, run, nil, nil)
	if !strings.Contains(errOut, "backtrace:") {
		t.Fatalf("stderr missing backtrace header:\n%s", errOut)
	}

	f, err := goelf.Open(out)
	if err != nil {
		t.Fatalf("open ELF: %v", err)
	}
	defer f.Close()
	syms, err := f.Symbols()
	if err != nil {
		t.Fatalf("Symbols() (expected .symtab under -g): %v", err)
	}
	sort.Slice(syms, func(i, j int) bool { return syms[i].Value < syms[j].Value })
	resolve := func(a uint64) string {
		for _, s := range syms {
			if s.Value <= a && a < s.Value+s.Size {
				if src, ok := symname.Source(s.Name); ok {
					return src
				}
				return s.Name
			}
		}
		return ""
	}

	var frames []string
	for _, m := range backtraceHexRe.FindAllString(errOut, -1) {
		a, err := strconv.ParseUint(m[2:], 16, 64)
		if err != nil {
			continue
		}
		frames = append(frames, resolve(a))
	}
	if len(frames) < 3 {
		t.Fatalf("got %d backtrace frames, want >= 3\nstderr:\n%s", len(frames), errOut)
	}
	for _, want := range []string{"mid", "main", "_start"} {
		found := false
		for _, got := range frames {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("backtrace frames %v missing %q\nstderr:\n%s", frames, want, errOut)
		}
	}
}

func TestX86_64AbortBacktrace(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	runBacktraceCase(t, "x86-64-linux", func(bin string) *exec.Cmd { return runX86Bin(qemu, bin) })
}

func TestArm64AbortBacktrace(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	runBacktraceCase(t, "arm64-linux", func(bin string) *exec.Cmd {
		if qemu == "" {
			return exec.Command(bin)
		}
		return exec.Command(qemu, bin)
	})
}

func TestArm64DebugPreservesTailCalls(t *testing.T) {
	bin := buildFernCLI(t)
	for _, tc := range []struct {
		name, source string
		register     bool
	}{
		{"array_parameter", `@noinline function inner(xs: i32[]): i32 { return xs[7]; }
@noinline function mid(xs: i32[]): i32 { return inner(xs); }
function main(): i32 { let xs: i32[] = [1, 2, 3]; return mid(xs); }
`, true},
		{"no_parameters", `@noinline function inner(): i32 { return 7; }
@noinline function mid(): i32 { return inner(); }
function main(): i32 { return mid(); }
`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "chain.fern")
			if err := os.WriteFile(src, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, debug := range []bool{false, true} {
				out := filepath.Join(dir, strconv.FormatBool(debug)+".s")
				args := []string{"-target", "arm64-linux", "-emit", "asm", "-o", out}
				if debug {
					args = append(args, "-g")
				}
				if o, err := exec.Command(bin, append(args, src)...).CombinedOutput(); err != nil {
					t.Fatalf("debug=%v: build: %v\n%s", debug, err, o)
				}
				data, err := os.ReadFile(out)
				if err != nil {
					t.Fatal(err)
				}
				for _, callee := range []string{"inner", "mid"} {
					symbol := symname.Fn(callee)
					if tc.register {
						symbol += ".r"
					}
					wantTail := tc.register
					tail := "    b " + symbol + "\n"
					call := "    bl " + symbol + "\n"
					if strings.Contains(string(data), tail) != wantTail || strings.Contains(string(data), call) == wantTail {
						t.Errorf("debug=%v: wrong call form for %s\n%s", debug, callee, data)
					}
				}
			}
		})
	}
}

func runBacktraceOffCase(t *testing.T, target string, run func(bin string) *exec.Cmd) {
	t.Helper()
	for _, tc := range []struct {
		name string
		args []string
		env  []string
	}{
		{"env", nil, []string{"FERN_BACKTRACE=0"}},
		{"flag", []string{"-backtrace=false"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errOut := buildAndAbort(t, target, run, tc.args, tc.env)
			if strings.Contains(errOut, "backtrace:") {
				t.Errorf("backtrace header still printed with the walk suppressed:\n%s", errOut)
			}
			if got := backtraceHexRe.FindAllString(errOut, -1); len(got) != 0 {
				t.Errorf("frame addresses still printed with the walk suppressed: %v\n%s", got, errOut)
			}
		})
	}
}

func TestX86_64AbortBacktraceOff(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	runBacktraceOffCase(t, "x86-64-linux", func(bin string) *exec.Cmd { return runX86Bin(qemu, bin) })
}

func TestArm64AbortBacktraceOff(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	runBacktraceOffCase(t, "arm64-linux", func(bin string) *exec.Cmd {
		if qemu == "" {
			return exec.Command(bin)
		}
		return exec.Command(qemu, bin)
	})
}
