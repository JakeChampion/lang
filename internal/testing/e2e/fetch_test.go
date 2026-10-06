package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// std/fetch's client against a scripted loopback origin (#9855): every
// response shape the parser reads (a length, chunked with trailers, an
// interim 1xx stepped over, close-delimited, bodiless 204 and HEAD, bytes
// that are not UTF-8, a body larger than one recv), the request as it
// goes out (method, target, Host, the caller's header, the body and its
// length), the host bag's `plat.http`, and each FetchError the client
// produces. Both native backends (the arm64 binary under qemu connects to
// the host origin), and the interpreter.
func TestFetchClient(t *testing.T) {
	bin := buildFernCLI(t)
	up := e2eharness.StartFetchUpstream(t)
	e2eharness.SetFetchProxy(t, up)
	closed := e2eharness.ClosedLoopbackPort(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "fetch_client.fern")
	if err := os.WriteFile(srcPath, []byte(e2eharness.FetchClientSource(up.Port, closed)), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	for _, be := range nativeBackends() {
		be := be
		t.Run(be.target, func(t *testing.T) {
			qemu := be.qemu(t)
			out := filepath.Join(dir, be.target+"_fetch_client.bin")
			if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			cmd := be.run(qemu, out)
			var stdout bytes.Buffer
			cmd.Stdout = &stdout
			_ = cmd.Run()
			e2eharness.CheckFetchClient(t, up, stdout.String(), cmd.ProcessState.ExitCode())
		})
	}
}

func TestFetchClientInterp(t *testing.T) {
	up := e2eharness.StartFetchUpstream(t)
	e2eharness.SetFetchProxy(t, up)
	closed := e2eharness.ClosedLoopbackPort(t)
	out, code := runInterpExitCode(t, e2eharness.FetchClientSource(up.Port, closed))
	e2eharness.CheckFetchClient(t, up, out, code)
}

type nativeBackend struct {
	target string
	qemu   func(*testing.T) string
	run    func(qemu, bin string, args ...string) *exec.Cmd
}

func nativeBackends() []nativeBackend {
	return []nativeBackend{
		{"x86-64-linux", x86QemuOrEmpty, runX86Bin},
		{"arm64-linux", arm64QemuOrEmpty, runArm64Bin},
	}
}

// An accumulator with a second reference copies the whole response so
// far on every append, which the rc==1 cliff counters make observable.
// Pin them against a 1 MiB body: the blocking client appends each `recv`
// onto a uniquely owned buffer and must never cross the cliff at all,
// and the drain's chunk list is captured by `resume` so it does cross,
// but what it copies is a pointer array, kept three orders of magnitude
// below the ~137 MB a byte-wise accumulator would have copied for the
// same body. The counters see only shared-array pushes: a fresh buffer
// rebuilt from a unique one on every recv is just as quadratic and
// invisible here, so the client's in-place growth is pinned by reading
// `__append_bytes`, not by this test.
func TestFetchAccumulatorStaysLinear(t *testing.T) {
	bin := buildFernCLI(t)
	const bodyLen = 1 << 20
	port := e2eharness.StartBodyUpstream(t, e2eharness.AlphabetBody(bodyLen))
	src := e2eharness.FetchAccumulatorSource(port, bodyLen)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "fetch_linear.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	for _, be := range nativeBackends() {
		be := be
		t.Run(be.target, func(t *testing.T) {
			qemu := be.qemu(t)
			out := filepath.Join(dir, be.target+"_fetch_linear.bin")
			if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			cmd := be.run(qemu, out)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 42 {
				t.Errorf("%s: accumulator shape exit = %d, want 42", be.target, code)
			}
		})
	}
}
