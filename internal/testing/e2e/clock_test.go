package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const clockResolutionSource = `import "std/i64";

function main(): i32 {
    print(clock_resolution().to_string());
    return 0;
}
`

const clockSetSource = `function report(r: Result[void, IoError]): void {
    match (r) {
        Ok(_) => { print("ok"); },
        Err(e) => {
            match (e) {
                Other(_, msg, _) => { print(msg); },
                Unsupported => { print("Operation not supported"); },
                _ => { print("another IoError variant"); }
            }
        }
    }
}

function main(): i32 {
    let billion: i64 = 1000000000;
    let now: i64 = now_ns();
    report(clock_set(now / billion, now % billion));
    report(clock_set(now / billion, billion));
    report(clock_set(now / billion, 0 - 1));
    return 0;
}
`

const clockSetWant = "Operation not permitted\nInvalid argument\nInvalid argument\n"

func clockResolutionWant(t *testing.T) string {
	t.Helper()
	return strconv.FormatInt(e2eharness.HostClockResolution(t), 10) + "\n"
}

func runClockProbe(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	e2eharness.WithoutClockPrivilege(t, cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("probe: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func TestX86_64ClockResolution(t *testing.T) {
	_, runner := x86_64Tooling(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetX86_64Linux, clockResolutionSource, nil)
	if got, want := runClockProbe(t, runX86_64Bin(runner, bin)), clockResolutionWant(t); got != want {
		t.Fatalf("clock_resolution() printed %q, want the host's clock_getres %q", got, want)
	}
}

func TestX86_64ClockSet(t *testing.T) {
	_, runner := x86_64Tooling(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetX86_64Linux, clockSetSource, nil)
	if got := runClockProbe(t, runX86_64Bin(runner, bin)); got != clockSetWant {
		t.Fatalf("clock_set probe printed %q, want %q", got, clockSetWant)
	}
}

func TestArm64ClockResolution(t *testing.T) {
	bin, qemu := compileArm64Bin(t, clockResolutionSource)
	if got, want := runClockProbe(t, runArm64Bin(qemu, bin)), clockResolutionWant(t); got != want {
		t.Fatalf("clock_resolution() printed %q, want the host's clock_getres %q", got, want)
	}
}

func TestArm64ClockSet(t *testing.T) {
	bin, qemu := compileArm64Bin(t, clockSetSource)
	if got := runClockProbe(t, runArm64Bin(qemu, bin)); got != clockSetWant {
		t.Fatalf("clock_set probe printed %q, want %q", got, clockSetWant)
	}
}

func interpClockProbe(t *testing.T, src string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return runClockProbe(t, exec.Command(buildLangBinForInterp(t), "-interp", p))
}

func TestInterpClockResolution(t *testing.T) {
	if got, want := interpClockProbe(t, clockResolutionSource), clockResolutionWant(t); got != want {
		t.Fatalf("clock_resolution() printed %q, want the host's clock_getres %q", got, want)
	}
}

func TestInterpClockSet(t *testing.T) {
	if got := interpClockProbe(t, clockSetSource); got != clockSetWant {
		t.Fatalf("clock_set probe printed %q, want %q", got, clockSetWant)
	}
}

func TestWASMClockResolution(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	want := clockResolutionWant(t)
	core := e2eharness.CompileSelfHostSource(t, e2eharness.TargetWasm32Wasi, clockResolutionSource, nil)
	if got := runClockProbe(t, exec.Command("wasmtime", "run", core)); got != want {
		t.Errorf("preview1 core: clock_resolution() printed %q, want the host's clock_getres %q", got, want)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(clockResolutionSource), 0o644); err != nil {
		t.Fatal(err)
	}
	comp := filepath.Join(dir, "prog.wasm")
	build := e2eharness.SelfHostCompileCmd(t, e2eharness.TargetWasm32Wasi, src, comp)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("component build: %v\n%s", err, out)
	}
	if got := runClockProbe(t, exec.Command("wasmtime", "run", comp)); got != want {
		t.Errorf("component: clock_resolution() printed %q, want the host's clock_getres %q", got, want)
	}
}

func TestWASMClockSet(t *testing.T) {
	prog, err := parser.Parse(clockSetSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
		if vs := platforms.Enforce(prog, target); len(vs) != 0 {
			t.Errorf("%s refused %q on %q; it has a clock to answer for", target, vs[0].Builtin, vs[0].Capability)
		}
	}
	refused := false
	for _, v := range platforms.Enforce(prog, "arm64-freestanding") {
		refused = refused || v.Builtin == "clock_set" && v.Capability == "now"
	}
	if !refused {
		t.Errorf("arm64-freestanding did not refuse clock_set on now")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	const want = "Operation not supported\nOperation not supported\nOperation not supported\n"
	core := e2eharness.CompileSelfHostSource(t, e2eharness.TargetWasm32Wasi, clockSetSource, nil)
	if got := runClockProbe(t, exec.Command("wasmtime", "run", core)); got != want {
		t.Errorf("preview1 core: clock_set probe printed %q, want %q", got, want)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(clockSetSource), 0o644); err != nil {
		t.Fatal(err)
	}
	comp := filepath.Join(dir, "prog.wasm")
	if out, err := e2eharness.SelfHostCompileCmd(t, e2eharness.TargetWasm32Wasi, src, comp).CombinedOutput(); err != nil {
		t.Fatalf("component build: %v\n%s", err, out)
	}
	if got := runClockProbe(t, exec.Command("wasmtime", "run", comp)); got != want {
		t.Errorf("component: clock_set probe printed %q, want %q", got, want)
	}
}

func TestArm64DarwinClock(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	for _, tc := range []struct{ name, src, want string }{
		{"resolution", clockResolutionSource, "1000\n"},
		{"set", clockSetSource, clockSetWant},
	} {
		src := filepath.Join(dir, tc.name+".fern")
		if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, tc.name)
		if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
			t.Fatalf("arm64-darwin build of the %s probe: %v\n%s", tc.name, err, o)
		}
		if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
			continue
		}
		if got := runClockProbe(t, exec.Command(out)); got != tc.want {
			t.Errorf("%s probe printed %q, want %q", tc.name, got, tc.want)
		}
	}
}
