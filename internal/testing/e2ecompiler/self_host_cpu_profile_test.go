package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const cpuProfileHot = `import "std/i64";
@noinline function hot(seed: i64): i64 {
  let x: i64 = seed;
  let i: i32 = 0;
  while (i < 600000000) {
    x = (x * 1664525 + 1013904223) ^ (i as i64);
    i = i + 1;
  }
  return x;
}
function main(): i32 { print(hot(args().len() as i64).to_string()); return 7; }
`

func compileCPUProfile(t *testing.T, cli *selfHostCLI, src, target, mode string, flags ...string) string {
	t.Helper()
	return compileProfileWithEnv(t, cli, src, target, mode, nil, flags...)
}

func compileProfileWithEnv(t *testing.T, cli *selfHostCLI, src, target, mode string, env []string, flags ...string) string {
	t.Helper()
	dir := t.TempDir()
	input, bin := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main")
	if err := os.WriteFile(input, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append(append([]string{}, flags...), "-target", target, "-o", bin, input, cli.stdlib)
	cmd := runX86_64Bin(cli.runner, cli.bin, args...)
	cmd.Env = childEnv(append([]string{"FERN_PROFILE=" + mode}, env...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	return bin
}

func runCPUProfile(t *testing.T, cmd *exec.Cmd, wantExit int) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	timer := time.AfterFunc(45*time.Second, func() { _ = cmd.Process.Kill() })
	_ = cmd.Wait()
	timer.Stop()
	if cmd.ProcessState.ExitCode() != wantExit {
		t.Fatalf("exit %v, want %d\n%s", cmd.ProcessState, wantExit, &stderr)
	}
	return stdout.String(), stderr.String()
}

func TestSelfHostCPUProfile(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			var qemu string
			if target == "arm64-linux" {
				_, qemu = arm64Tooling(t)
				t.Setenv("QEMU_CPU", "max")
			}
			run := func(bin string) *exec.Cmd {
				if target == "arm64-linux" {
					return runArm64Bin(qemu, bin)
				}
				return runX86_64Bin(cli.runner, bin)
			}
			for _, harden := range []bool{false, true} {
				if harden && target != "arm64-linux" {
					continue
				}
				flags := []string{}
				if harden {
					flags = append(flags, "-harden")
				}
				bin := compileCPUProfile(t, cli, cpuProfileHot, target, "cpu", flags...)
				stdout, stderr := runCPUProfile(t, run(bin), 7)
				if stdout != "-1611139248354046463\n" {
					t.Fatalf("stdout %q", stdout)
				}
				checkCPUProfileHot(t, stderr)
			}
			for _, tc := range []struct {
				src  string
				code int
			}{
				{`function main(): i32 { exit(9); return 0; }`, 9},
				{`function main(): i32 { let xs: i32[] = [1, 2]; return xs[args().len()+4]; }`, 134},
			} {
				bin := compileCPUProfile(t, cli, tc.src, target, "cpu")
				_, stderr := runCPUProfile(t, run(bin), tc.code)
				if strings.Count(stderr, "# fern CPU profile samples=") != 1 {
					t.Fatalf("missing or repeated exit profile:\n%s", stderr)
				}
			}
		})
	}
}

func checkCPUProfileHot(t *testing.T, output string) {
	t.Helper()
	header := regexp.MustCompile(`^# fern CPU profile samples=([0-9]+) dropped=([0-9]+)\n`).FindStringSubmatch(output)
	if header == nil {
		t.Fatalf("missing profile header: %s", output)
	}
	n, _ := strconv.Atoi(header[1])
	if n < 3 {
		t.Fatalf("only %d samples; expected the long CPU loop to be sampled", n)
	}
	rows := strings.Split(strings.TrimSpace(output[len(header[0]):]), "\n")
	if len(rows) != n {
		t.Fatalf("header counts %d samples, got %d rows", n, len(rows))
	}
	hot := 0
	for _, row := range rows {
		if !strings.HasSuffix(row, " 1") {
			t.Errorf("invalid collapsed row %q", row)
		}
		if strings.Contains(row, "_start;__fn_main;__fn_hot 1") {
			hot++
		}
		if strings.Contains(row, "_start;_start;") {
			t.Errorf("duplicate root frame: %s", row)
		}
	}
	if hot*4 < n*3 {
		t.Fatalf("hot function is only %d/%d samples:\n%s", hot, n, output)
	}
}

func TestSelfHostCPUProfileOff(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		const src = `function main(): i32 { let xs: i32[] = [1, 2, 3]; print("off"); return xs[args().len()]; }`
		a := compileCPUProfile(t, cli, src, target, "")
		b := compileCPUProfile(t, cli, src, target, "0")
		x, err := os.ReadFile(a)
		if err != nil {
			t.Fatal(err)
		}
		y, err := os.ReadFile(b)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(x, y) {
			t.Errorf("%s: explicitly disabled profiling changes executable", target)
		}
		if bytes.Contains(x, []byte("fern CPU profile")) {
			t.Errorf("%s: off binary contains profiler runtime", target)
		}
	}
}

func TestSelfHostCPUProfileRefusals(t *testing.T) {
	cli := buildSelfHostCLI(t)
	t.Setenv("FERN_PROFILE_FILE", "")
	input := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(input, []byte(`function main(): i32 { return 0; }`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		target, mode, want string
		flags              []string
	}{
		{"wasm32-wasi", "cpu", "native Linux executable", nil},
		{"arm64-darwin", "cpu", "native Linux executable", nil},
		{"arm64-android", "cpu", "native Linux executable", nil},
		{"x86-64-linux", "cpu", "native Linux executable", []string{"-shared"}},
		{"x86-64-linux", "other", "FERN_PROFILE", nil},
		{"x86-64-linux", "cpu-pprof", "FERN_PROFILE_FILE", nil},
		{"x86-64-linux", "alloc-pprof", "FERN_PROFILE_FILE", nil},
	} {
		args := append(append([]string{}, tc.flags...), "-target", tc.target, "-o", filepath.Join(t.TempDir(), "out"), input, cli.stdlib)
		cmd := runX86_64Bin(cli.runner, cli.bin, args...)
		cmd.Env = childEnv("FERN_PROFILE=" + tc.mode)
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), tc.want) {
			t.Errorf("%s/%s: %v %s", tc.target, tc.mode, err, out)
		}
	}
}

func TestSelfHostCPUProfileCapacity(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			getpid, gettid, tgkill := "39", "186", "234"
			var qemu string
			if target == "arm64-linux" {
				_, qemu = arm64Tooling(t)
				getpid, gettid, tgkill = "172", "178", "131"
			}
			// Deliver synchronously to this thread: the test reaches capacity
			// without depending on the machine's speed or waiting 82 seconds.
			src := `function main(): i32 {
  let pid: i64 = __syscall3(` + getpid + `, 0, 0, 0);
  let tid: i64 = __syscall3(` + gettid + `, 0, 0, 0);
  let i: i32 = 0;
  while (i < 9000) {
    if (__syscall3(` + tgkill + `, pid, tid, 27) != (0 as i64)) { return 11; }
    i = i + 1;
  }
  return 7;
}`
			bin := compileCPUProfile(t, cli, src, target, "cpu")
			cmd := runX86_64Bin(cli.runner, bin)
			if target == "arm64-linux" {
				cmd = runArm64Bin(qemu, bin)
			}
			_, stderr := runCPUProfile(t, cmd, 7)
			header := regexp.MustCompile(`^# fern CPU profile samples=8192 dropped=([0-9]+)\n`).FindStringSubmatch(stderr)
			if header == nil {
				t.Fatalf("wrong capacity header: %.200s", stderr)
			}
			dropped, _ := strconv.Atoi(header[1])
			if dropped < 808 {
				t.Errorf("only %d drops for 9000 signals and 8192 slots", dropped)
			}
			if rows := strings.Count(stderr[len(header[0]):], "\n"); rows != 8192 {
				t.Errorf("%d rows, want 8192", rows)
			}
		})
	}
}

func TestSelfHostCPUProfileSamplerHasNoCalls(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		asm := compileCPUProfile(t, cli, `function main(): i32 { return 0; }`, target, "cpu", "-emit", "asm")
		data, err := os.ReadFile(asm)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		label := "__fn___fern_profile_sample.r:\n"
		start := strings.Index(text, label)
		if start < 0 {
			t.Fatalf("%s: no sample routine", target)
		}
		body := text[start+len(label):]
		if end := strings.Index(body, "\n__fn_"); end >= 0 {
			body = body[:end]
		}
		// The entire capture routine must be raw loads/stores/arithmetic:
		// no allocator, I/O, locks supplied by libraries, or hidden helpers.
		for _, line := range strings.Split(body, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			switch fields[0] {
			case "call", "callq", "bl", "blr", "syscall", "svc":
				t.Errorf("%s: sampler invokes another routine: %s", target, line)
			}
		}
	}
}

func TestSelfHostCPUProfileRetainsLateSamples(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		getpid, gettid, tgkill := "39", "186", "234"
		var qemu string
		if target == "arm64-linux" {
			_, qemu = arm64Tooling(t)
			getpid, gettid, tgkill = "172", "178", "131"
		}
		body := `(pid: i64, tid: i64): void {
  let i: i32 = 0;
  while (i < 10000) { __syscall3(` + tgkill + `, pid, tid, 27); i = i + 1; }
}
`
		src := "@noinline function early" + body + "@noinline function late" + body + `
function main(): i32 {
  let pid: i64 = __syscall3(` + getpid + `, 0, 0, 0);
  let tid: i64 = __syscall3(` + gettid + `, 0, 0, 0);
  early(pid, tid);
  late(pid, tid);
  return 7;
}`
		bin := compileCPUProfile(t, cli, src, target, "cpu")
		cmd := runX86_64Bin(cli.runner, bin)
		if target == "arm64-linux" {
			cmd = runArm64Bin(qemu, bin)
		}
		_, stderr := runCPUProfile(t, cmd, 7)
		// A first-full buffer contains no late samples. A reservoir should
		// keep roughly equal populations from the two equal event streams.
		for _, name := range []string{"__fn_early", "__fn_late"} {
			if n := strings.Count(stderr, name); n < 2500 || n > 5700 {
				t.Errorf("%s: %s has %d/8192 retained samples", target, name, n)
			}
		}
	}
}
