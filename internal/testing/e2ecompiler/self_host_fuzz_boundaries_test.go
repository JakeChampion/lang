package e2ecompiler

import (
	"bytes"
	"context"
	"fmt"
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
	fault := budgetFault(what, r, input)
	if fault != "" {
		t.Error(fault)
	}
	return fault != ""
}

// budgetFault returns the budget fault of one run, or "".
func budgetFault(what string, r fuzzRun, input []byte) string {
	switch {
	case r.timedOut:
		return fmt.Sprintf("%s hung past %s on %d bytes", what, fuzzTimeBudget, len(input))
	case !r.exited:
		return fmt.Sprintf("%s died by signal (%s) on %d bytes\nstderr: %s", what, r.state, len(input), r.stderr)
	case r.maxRSS > fuzzMemoryBudget:
		return fmt.Sprintf("%s used %d bytes of resident memory on %d bytes of input, budget %d", what, r.maxRSS, len(input), fuzzMemoryBudget)
	}
	return ""
}

// fuzzBuildCache gives the fuzz workers one disk cache for the driver
// binaries. The coordinator builds a driver once in its setup and publishes
// it there, and each worker process, which runs the same setup, copies it
// instead of compiling the compiler again. Without a shared cache every
// worker spent the whole budget building the CLI, so the run ended before
// its first execution and still passed.
func fuzzBuildCache(f testing.TB) {
	if os.Getenv("FERN_SELFHOST_BUILD_CACHE") != "" {
		return
	}
	dir := filepath.Join(os.TempDir(), "fern-fuzz-build-cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.Fatal(err)
	}
	f.Setenv("FERN_SELFHOST_BUILD_CACHE", dir)
}

func fuzzCLI(f testing.TB) (bin, stdlib string) {
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

func fuzzDriver(f testing.TB, driver string) string {
	fuzzBuildCache(f)
	gcc, runner := e2eharness.X86_64Tooling(f)
	if len(runner) != 0 {
		f.Skip("the fuzz drivers run natively only")
	}
	dir := f.TempDir()
	e2eharness.CopySelfHostDriver(f, dir, "drivers/"+driver+".fern")
	return e2eharness.BuildSelfHostBin(f, gcc, dir, "drivers/"+driver+".fern", driver)
}

func fuzzCompile(f testing.TB, bin, stdlib, src string, flags ...string) {
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

// FuzzSelfHostManifest feeds hostile bytes to the build manifest reader of
// `fern -manifest`, through drivers/manifest_fuzz_run.fern. The reader finds
// the fields or finds none, and the fields are printable ASCII, whatever the
// file holds.
func FuzzSelfHostManifest(f *testing.F) { fuzzReaderTarget(f) }

func manifestSeeds(tb testing.TB) [][]byte {
	bin, stdlib := fuzzCLI(tb)
	image := filepath.Join(tb.TempDir(), "image")
	fuzzCompile(tb, bin, stdlib, "function main(): i32 { return 0; }\n", "-target", "x86-64-linux", "-o", image)
	raw, err := os.ReadFile(image)
	if err != nil {
		tb.Fatal(err)
	}
	at := bytes.Index(raw, []byte(fuzzManifestMagic))
	if at < 0 {
		tb.Fatalf("the compiled image carries no manifest")
	}
	lo, hi := at-64, at+320
	if lo < 0 {
		lo = 0
	}
	if hi > len(raw) {
		hi = len(raw)
	}
	line := "FERNBUILD1 target=x86-64-linux profile=safe harden=0 pie=0 sanitize=0 backtrace=0 size=0 shared=0 heap_limit=0\n"
	return [][]byte{
		raw[lo:hi],
		[]byte(line),
		[]byte(strings.TrimSuffix(line, "\n")),
		[]byte(strings.Repeat(fuzzManifestMagic, 100)),
		[]byte(fuzzManifestMagic + strings.Repeat("x", 600) + "\n"),
		[]byte(fuzzManifestMagic + " profile= heap_limit=\n"),
		[]byte("FERNBUILD1 target=x86-64-linux  profile=safe heap_limit=0  \n"),
		[]byte("FERNBUILD1 target=\x00 profile=safe heap_limit=0\n"),
		[]byte("FERNBUILD1 target=\x1b]0;owned\x07 profile=safe heap_limit=0\n"),
		[]byte("FERNBUILD1 target=x86-64-linux profile=\xff\xfe heap_limit=0\n"),
		[]byte("FERNBUILD1 target=\x1b[2J profile=safe heap_limit=0\n" + line),
		{},
		{0x7f, 'E', 'L', 'F'},
	}
}

// manifestCheck reads `manifest N` and the N fields after it. The first field
// names the target, and no field holds a control byte.
func manifestCheck(r readerRun) string {
	lines := strings.Split(r.summary, "\n")
	n, err := strconv.Atoi(strings.TrimPrefix(lines[0], "manifest "))
	if err != nil || n != len(lines)-1 {
		return fmt.Sprintf("the driver printed %q for %d bytes", r.summary, len(r.input))
	}
	if n > 0 && !strings.HasPrefix(lines[1], "target: ") {
		return fmt.Sprintf("the first field is not the target on %d bytes\nfields: %q", len(r.input), r.summary)
	}
	for _, c := range []byte(r.summary) {
		if (c < 0x20 || c > 0x7e) && c != '\n' {
			return fmt.Sprintf("the fields carry byte %#x on %d bytes\nfields: %q", c, len(r.input), r.summary)
		}
	}
	return ""
}

// TestSelfHostManifestRefusal pins the refusal of `fern -manifest` for a file
// with no manifest: exit 1 and a message that names the file.
func TestSelfHostManifestRefusal(t *testing.T) {
	bin, _ := fuzzCLI(t)
	path := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(path, []byte(fuzzManifestMagic+"x86-64-linux\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-manifest", path)
	cmd.Env = e2eharness.SelfHostChildEnv()
	r := runUnderBudget(cmd)
	if r.code != 1 || !strings.HasPrefix(r.stderr, "fern: no build manifest in "+path+": ") || r.stdout != "" {
		t.Fatalf("exit %d, want 1 with the refusal\nstdout: %q\nstderr: %q", r.code, r.stdout, r.stderr)
	}
}

// FuzzSelfHostIRVerify decodes bytes into an op stream and runs the
// structural, stack and reference-count verifiers over it through
// drivers/irverify_fuzz_run.fern. Every stream must come back as a problem
// count: the verifiers refuse, they never trap.
func FuzzSelfHostIRVerify(f *testing.F) {
	fuzzReaderTarget(f)
}

func irSeeds(testing.TB) [][]byte {
	return [][]byte{
		{},
		{1, 3},
		{1, 3, 0, 0, 0, 0},
		{2, 4, 13, 0, 1, 0, 14, 2, 1, 0, 15, 2, 1, 0},
		{0, 0, 255, 255, 255, 255},
		{7, 15, 64, 128, 3, 7, 65, 130, 4, 11, 66, 132, 8, 15},
		bytes.Repeat([]byte{3, 1, 9, 0}, 200),
	}
}

// irOpCount checks that the driver decoded one op from each four bytes after
// the two that choose the counts.
func irOpCount(r readerRun) string {
	want := 0
	if len(r.input) >= 2 {
		want = (len(r.input) - 2) / 4
	}
	fields := strings.Fields(r.summary)
	if len(fields) < 3 || fields[2] != strconv.Itoa(want) {
		return fmt.Sprintf("the driver gave %q for %d bytes, want %d ops", r.summary, len(r.input), want)
	}
	return ""
}

// FuzzSelfHostX86Assembler feeds hostile text to the x86-64 assembler through
// drivers/asm_fuzz_run.fern.
func FuzzSelfHostX86Assembler(f *testing.F) { fuzzReaderTarget(f) }

// FuzzSelfHostArm64Assembler feeds hostile text to the arm64 assembler.
func FuzzSelfHostArm64Assembler(f *testing.F) { fuzzReaderTarget(f) }

// assemblerSeeds gives the seeds of an assembler for target: the assembly of
// a compiled program and short texts that probe labels, sections and
// alignment.
func assemblerSeeds(target string) func(testing.TB) [][]byte {
	return func(f testing.TB) [][]byte {
		bin, stdlib := fuzzCLI(f)
		return assemblerSeedsOf(compiledAssembly(f, bin, stdlib, target))
	}
}

func assemblerSeedsOf(text []byte) [][]byte {
	if len(text) > 16<<10 {
		text = text[:16<<10]
	}
	return [][]byte{
		text,
		[]byte(".text\n_start:\n ret\n"),
		[]byte(".text\n_start:\n b _start\n"),
		[]byte(".text\n134217728:\n b 134217728b\n"),
		[]byte(".data\nx: .quad y\n.text\ny: .cfi_startproc\n.cfi_endproc\n"),
		[]byte(strings.Repeat(".section .rodata\n", 50)),
		[]byte(".text\nret\n.p2align 26\nret\n"),
		[]byte(".text\nret\n.align 67108864\nret\n"),
		[]byte(".data\nx:\n.zero 2000000000\n"),
		[]byte("\x00\xff .text \n\n\n"),
		{},
	}
}

// compiledAssembly is the assembly that the compiler emits for a small program
// with flags.
func compiledAssembly(f testing.TB, bin, stdlib, target string, flags ...string) []byte {
	emitted := filepath.Join(f.TempDir(), "out.s")
	args := append([]string{"-target", target, "-emit", "asm", "-o", emitted}, flags...)
	fuzzCompile(f, bin, stdlib, "function main(): i32 { let xs: i32[] = [1, 2, 3]; return xs[1] + xs.len(); }\n", args...)
	text, err := os.ReadFile(emitted)
	if err != nil {
		f.Fatal(err)
	}
	return text
}
