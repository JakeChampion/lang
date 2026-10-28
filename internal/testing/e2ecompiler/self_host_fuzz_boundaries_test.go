package e2ecompiler

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The targets here feed hostile bytes to the boundaries the compiler
// crosses after the front end: the build manifest reader, which reads a
// native image, the IR verifiers, which read an op stream, and the two
// in-process assemblers, which read assembly text. Each input runs in a
// child process under a time budget and a resident-memory budget. The
// accepted outcomes are the ones each boundary documents; a signal, a hang
// or a budget breach fails the input.

const (
	fuzzInputLimit   = 64 << 10
	fuzzTimeBudget   = 20 * time.Second
	fuzzMemoryBudget = 512 << 20
)

type fuzzRun struct {
	exited   bool
	code     int
	state    string
	timedOut bool
	maxRSS   int64
	stdout   string
	stderr   string
}

func runUnderBudget(cmd *exec.Cmd) fuzzRun {
	ctx, cancel := context.WithTimeout(context.Background(), fuzzTimeBudget)
	defer cancel()
	bounded := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	bounded.Env = cmd.Env
	bounded.Dir = cmd.Dir
	var so, se bytes.Buffer
	bounded.Stdout, bounded.Stderr = &so, &se
	err := bounded.Run()
	r := fuzzRun{stdout: so.String(), stderr: se.String(), code: -1, timedOut: ctx.Err() == context.DeadlineExceeded}
	if ps := bounded.ProcessState; ps != nil {
		r.exited, r.code, r.state = ps.Exited(), ps.ExitCode(), ps.String()
		if ru, ok := ps.SysUsage().(*syscall.Rusage); ok {
			r.maxRSS = ru.Maxrss << 10
		}
	} else if err != nil {
		r.state = err.Error()
	}
	return r
}

// failOutsideBudget reports an input that hung, died by signal or exceeded
// the memory budget, whatever its exit status.
func failOutsideBudget(t *testing.T, what string, r fuzzRun, input []byte) bool {
	t.Helper()
	switch {
	case r.timedOut:
		t.Errorf("%s hung past %s on %d bytes", what, fuzzTimeBudget, len(input))
	case !r.exited:
		t.Errorf("%s died by signal (%s) on %d bytes\nstderr: %s", what, r.state, len(input), r.stderr)
	case r.maxRSS > fuzzMemoryBudget:
		t.Errorf("%s used %d bytes of resident memory on %d bytes of input, budget %d", what, r.maxRSS, len(input), fuzzMemoryBudget)
	default:
		return false
	}
	return true
}

// fuzzBuildCache gives the fuzz workers one disk cache for the driver
// binaries. The coordinator builds a driver once in its setup and publishes
// it there, and each worker process, which runs the same setup, copies it
// instead of compiling the compiler again. Without a shared cache every
// worker spent the whole budget building the CLI, so the run ended before
// its first execution and still passed.
func fuzzBuildCache(f *testing.F) {
	if os.Getenv("FERN_SELFHOST_BUILD_CACHE") != "" {
		return
	}
	dir := filepath.Join(os.TempDir(), "fern-fuzz-build-cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.Fatal(err)
	}
	f.Setenv("FERN_SELFHOST_BUILD_CACHE", dir)
}

func fuzzCLI(f *testing.F) (bin, stdlib string) {
	fuzzBuildCache(f)
	gcc, runner := e2eharness.X86_64Tooling(f)
	if len(runner) != 0 {
		f.Skip("the fuzz drivers run natively only")
	}
	dir := e2eharness.WriteSelfHostAsmProject(f)
	e2eharness.CopySelfHostDriver(f, dir, "fern.fern")
	bin = e2eharness.BuildSelfHostBin(f, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs("../../stdlib")
	if err != nil {
		f.Fatal(err)
	}
	return bin, stdlib
}

func fuzzDriver(f *testing.F, driver string) string {
	fuzzBuildCache(f)
	gcc, runner := e2eharness.X86_64Tooling(f)
	if len(runner) != 0 {
		f.Skip("the fuzz drivers run natively only")
	}
	dir := f.TempDir()
	e2eharness.CopySelfHostDriver(f, dir, "drivers/"+driver+".fern")
	return e2eharness.BuildSelfHostBin(f, gcc, dir, "drivers/"+driver+".fern", driver)
}

func fuzzCompile(f *testing.F, bin, stdlib, src string, flags ...string) {
	dir := f.TempDir()
	main := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(main, []byte(src), 0o644); err != nil {
		f.Fatal(err)
	}
	args := append(append([]string{}, flags...), main, stdlib)
	cmd := exec.Command(bin, args...)
	cmd.Env = e2eharness.SelfHostChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		f.Fatalf("compile %v: %v\n%s", flags, err, out)
	}
}

const fuzzManifestMagic = "FERNBUILD1 target="

// FuzzSelfHostManifest feeds hostile bytes to `fern -manifest`. The reader
// documents two outcomes: the fields on stdout with exit 0, or a refusal
// naming the file with exit 1.
func FuzzSelfHostManifest(f *testing.F) {
	bin, stdlib := fuzzCLI(f)
	image := filepath.Join(f.TempDir(), "image")
	fuzzCompile(f, bin, stdlib, "function main(): i32 { return 0; }\n", "-target", "x86-64-linux", "-o", image)
	raw, err := os.ReadFile(image)
	if err != nil {
		f.Fatal(err)
	}
	at := bytes.Index(raw, []byte(fuzzManifestMagic))
	if at < 0 {
		f.Fatalf("the compiled image carries no manifest")
	}
	lo, hi := at-64, at+320
	if lo < 0 {
		lo = 0
	}
	if hi > len(raw) {
		hi = len(raw)
	}
	line := "FERNBUILD1 target=x86-64-linux profile=safe harden=0 pie=0 sanitize=0 backtrace=0 size=0 shared=0 heap_limit=0\n"
	for _, seed := range [][]byte{
		raw[lo:hi],
		[]byte(line),
		[]byte(strings.TrimSuffix(line, "\n")),
		[]byte(strings.Repeat(fuzzManifestMagic, 100)),
		[]byte(fuzzManifestMagic + strings.Repeat("x", 600) + "\n"),
		[]byte(fuzzManifestMagic + " profile= heap_limit=\n"),
		[]byte("FERNBUILD1 target=\x00 profile=safe heap_limit=0\n"),
		{},
		{0x7f, 'E', 'L', 'F'},
	} {
		f.Add(seed)
	}
	work := f.TempDir()
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > fuzzInputLimit {
			t.Skip("over the input limit")
		}
		path := filepath.Join(work, "input")
		if err := os.WriteFile(path, input, 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bin, "-manifest", path)
		cmd.Env = e2eharness.SelfHostChildEnv()
		r := runUnderBudget(cmd)
		if failOutsideBudget(t, "fern -manifest", r, input) {
			return
		}
		switch r.code {
		case 0:
			if !strings.HasPrefix(r.stdout, "target: ") {
				t.Errorf("exit 0 without the target field on %d bytes\nstdout: %q", len(input), r.stdout)
			}
		case 1:
			if !strings.HasPrefix(r.stderr, "fern: no build manifest in ") {
				t.Errorf("exit 1 without the documented refusal on %d bytes\nstderr: %q", len(input), r.stderr)
			}
		default:
			t.Errorf("exit %d on %d bytes, want 0 or 1\nstdout: %q\nstderr: %q", r.code, len(input), r.stdout, r.stderr)
		}
	})
}

// FuzzSelfHostIRVerify decodes bytes into an op stream and runs the
// structural, stack and reference-count verifiers over it through
// drivers/irverify_fuzz_run.fern. Every stream must come back as a problem
// count: the verifiers refuse, they never trap.
func FuzzSelfHostIRVerify(f *testing.F) {
	driver := fuzzDriver(f, "irverify_fuzz_run")
	for _, seed := range [][]byte{
		{},
		{1, 3},
		{1, 3, 0, 0, 0, 0},
		{2, 4, 13, 0, 1, 0, 14, 2, 1, 0, 15, 2, 1, 0},
		{0, 0, 255, 255, 255, 255},
		{7, 15, 64, 128, 3, 7, 65, 130, 4, 11, 66, 132, 8, 15},
		bytes.Repeat([]byte{3, 1, 9, 0}, 200),
	} {
		f.Add(seed)
	}
	work := f.TempDir()
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > fuzzInputLimit {
			t.Skip("over the input limit")
		}
		path := filepath.Join(work, "ops")
		if err := os.WriteFile(path, input, 0o644); err != nil {
			t.Fatal(err)
		}
		r := runUnderBudget(exec.Command(driver, path))
		if failOutsideBudget(t, "irverify_fuzz_run", r, input) {
			return
		}
		if r.code != 0 || !strings.HasPrefix(r.stdout, "ops ") {
			t.Errorf("exit %d on %d bytes, want 0 with the counts\nstdout: %q\nstderr: %q", r.code, len(input), r.stdout, r.stderr)
			return
		}
		want := 0
		if len(input) >= 2 {
			want = (len(input) - 2) / 4
		}
		fields := strings.Fields(r.stdout)
		if n, err := strconv.Atoi(fields[1]); err != nil || n != want {
			t.Errorf("the driver decoded %q ops from %d bytes, want %d", fields[1], len(input), want)
		}
	})
}

// fuzzAssembler shares the body of the two assembler targets: the text goes
// to one in-process assembler through drivers/asm_fuzz_run.fern. The
// assemblers consume the compiler's own output, so on foreign text an exit
// 0 summary and an abort with a cause line are both documented outcomes; a
// signal, a hang or a budget breach is not.
func fuzzAssembler(f *testing.F, which, target string) {
	bin, stdlib := fuzzCLI(f)
	driver := fuzzDriver(f, "asm_fuzz_run")
	emitted := filepath.Join(f.TempDir(), "out.s")
	fuzzCompile(f, bin, stdlib, "function main(): i32 { let xs: i32[] = [1, 2, 3]; return xs[1] + xs.len(); }\n", "-target", target, "-emit", "asm", "-o", emitted)
	text, err := os.ReadFile(emitted)
	if err != nil {
		f.Fatal(err)
	}
	if len(text) > 16<<10 {
		text = text[:16<<10]
	}
	for _, seed := range [][]byte{
		text,
		[]byte(".text\n_start:\n ret\n"),
		[]byte(".text\n_start:\n b _start\n"),
		[]byte(".text\n134217728:\n b 134217728b\n"),
		[]byte(".data\nx: .quad y\n.text\ny: .cfi_startproc\n.cfi_endproc\n"),
		[]byte(strings.Repeat(".section .rodata\n", 50)),
		[]byte("\x00\xff .text \n\n\n"),
		{},
	} {
		f.Add(seed)
	}
	work := f.TempDir()
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > fuzzInputLimit {
			t.Skip("over the input limit")
		}
		path := filepath.Join(work, "text.s")
		if err := os.WriteFile(path, input, 0o644); err != nil {
			t.Fatal(err)
		}
		r := runUnderBudget(exec.Command(driver, which, path))
		if failOutsideBudget(t, "asm_fuzz_run "+which, r, input) {
			return
		}
		switch {
		case r.code == 0 && strings.HasPrefix(r.stdout, which+" "):
		case r.code == 134 && strings.HasPrefix(r.stderr, "fern: "):
		default:
			t.Errorf("exit %d on %d bytes, want 0 with a summary or 134 with a cause\nstdout: %q\nstderr: %q", r.code, len(input), r.stdout, r.stderr)
		}
	})
}

func FuzzSelfHostX86Assembler(f *testing.F)   { fuzzAssembler(f, "x86", "x86-64-linux") }
func FuzzSelfHostArm64Assembler(f *testing.F) { fuzzAssembler(f, "arm64", "arm64-linux") }
