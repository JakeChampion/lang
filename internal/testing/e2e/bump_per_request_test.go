package e2e

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestBumpPerRequest(t *testing.T) {
	compiler := buildFernCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			rounds := e2eharness.BumpPerRequestRounds
			if target == "arm64-linux" && arm64QemuOrEmpty(t) != "" {
				rounds /= 10
			}
			bin := buildNativeServer(t, compiler, target, e2eharness.BumpPerRequestServerSource())
			addr, _ := e2eharness.StartInheritedServer(t, nativeServerRunner(t, target)(bin))
			e2eharness.CheckBumpPerRequest(t, addr, rounds)
		})
	}
}

func TestServeListenFailure(t *testing.T) {
	compiler := buildFernCLI(t)
	for _, supervised := range []bool{false, true} {
		t.Run(fmt.Sprintf("supervised=%v", supervised), func(t *testing.T) {
			held := heldLoopbackPort(t)
			bin := buildNativeServer(t, compiler, "x86-64-linux", e2eharness.ListenFailureServerSource(held, supervised))
			e2eharness.CheckListenFailure(t, nativeServerRunner(t, "x86-64-linux")(bin), held)
		})
	}
}

func heldLoopbackPort(t *testing.T) int {
	t.Helper()
	held, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { held.Close() })
	return held.Addr().(*net.TCPAddr).Port
}

func buildNativeServer(t *testing.T, compiler, target, src string) string {
	t.Helper()
	dir := t.TempDir()
	path, bin := filepath.Join(dir, "server.fern"), filepath.Join(dir, "server")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(compiler, "-target", target, "-o", bin, path).CombinedOutput(); err != nil {
		t.Fatalf("build for %s: %v\n%s", target, err, out)
	}
	return bin
}
