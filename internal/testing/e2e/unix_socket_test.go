package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The Unix-domain sockets (#9853) on the native backends and the
// interpreter, raw and through std/net: one probe each over a socket file
// under /tmp, exit 42 iff every check holds. There is no wasm leg: the
// unix capability refuses the builtins there, which TestWASMUnixSocketRefused
// pins.
var unixProbes = []struct {
	name string
	src  func() string
}{
	{"raw", e2eharness.UnixSocketProbe},
	{"std_net", e2eharness.NetUnixProbe},
}

func TestUnixSocketInterp(t *testing.T) {
	for _, p := range unixProbes {
		t.Run(p.name, func(t *testing.T) {
			if out, got := runInterpExitCode(t, p.src()); got != 42 {
				t.Fatalf("interp got %d, want 42; first failing check: %s", got, out)
			}
		})
	}
}

func TestUnixSocketX86_64(t *testing.T) {
	for _, p := range unixProbes {
		t.Run(p.name, func(t *testing.T) {
			if out, got := compileAndRunX86_64(t, p.src()); got != 42 {
				t.Fatalf("x86-64 got %d, want 42; first failing check: %s", got, out)
			}
		})
	}
}

func TestUnixSocketArm64(t *testing.T) {
	for _, p := range unixProbes {
		t.Run(p.name, func(t *testing.T) {
			if out, got := compileAndRunArm64(t, p.src()); got != 42 {
				t.Fatalf("arm64 got %d, want 42; first failing check: %s", got, out)
			}
		})
	}
}

// The Darwin leg: sun_len leads the address and the path limit is 104.
func TestArm64DarwinUnixSocket(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires the Apple Silicon execution lane")
	}
	fern := buildFernCLI(t)
	for _, p := range unixProbes {
		t.Run(p.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "probe.fern")
			if err := os.WriteFile(src, []byte(p.src()), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "probe")
			if out, err := exec.Command(fern, "-target", "arm64-darwin", "-o", bin, src).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			out, err := exec.Command(bin).CombinedOutput()
			if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 42 {
				t.Fatalf("arm64-darwin: %v, want exit 42; first failing check: %s", err, out)
			}
		})
	}
}

// Neither WASI world has a filesystem namespace for sockets, so no wasi
// profile grants `unix` and E066 refuses the builtin with its name and the
// call site's position.
func TestWASMUnixSocketRefused(t *testing.T) {
	fern := buildFernCLI(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "u.fern")
	src := `function main(): i32 {
    return unix_listen("/tmp/x", 4);
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	emit := exec.Command(fern, "-target", "wasm32-wasi", "-o", filepath.Join(dir, "u.wasm"), srcPath)
	var eb bytes.Buffer
	emit.Stderr = &eb
	if err := emit.Run(); err == nil {
		t.Fatalf("expected a refusal for unix_listen on wasm32-wasi, got success")
	}
	for _, want := range []string{"E066", "unix_listen", srcPath} {
		if !bytes.Contains(eb.Bytes(), []byte(want)) {
			t.Errorf("refusal missing %q:\n%s", want, eb.String())
		}
	}
}
