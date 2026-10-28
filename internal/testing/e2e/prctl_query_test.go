//go:build linux

package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// PR_GET_DUMPABLE answers 1 for an ordinary process, and an option the
// kernel does not know answers EINVAL as -22. The two lines must agree
// between the native build and the Go oracle on the host.
const prctlQuerySrc = `function main(): i32 {
  print(prctl_query(3).to_string());
  print(prctl_query(999999).to_string());
  return 0;
}
`

func TestSelfHostPrctlQuery(t *testing.T) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("prctl_query runs on x86-64 and arm64 Linux hosts")
	}
	const want = "1\n-22\n"
	targets := []string{e2eharness.HostSelfHostTarget()}
	if runtime.GOARCH == "amd64" {
		targets = append(targets, "arm64-linux")
	}
	run := func(cmd *exec.Cmd) (string, string, int) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); cmd.ProcessState == nil {
			t.Fatalf("run: %v\n%s", err, stderr.String())
		}
		return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			var runner []string
			if target == "arm64-linux" {
				if qemu := e2eharness.Arm64Runner(t); qemu != "" {
					runner = []string{qemu}
				}
			}
			bin := e2eharness.CompileSelfHostSource(t, target, prctlQuerySrc, nil)
			argv := append(append([]string{}, runner...), bin)
			if out, errout, code := run(exec.Command(argv[0], argv[1:]...)); code != 0 || out != want || errout != "" {
				t.Fatalf("exit=%d stdout=%q stderr=%q; want %q", code, out, errout, want)
			}
		})
	}
	t.Run("oracle", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "main.fern")
		if err := os.WriteFile(src, []byte(prctlQuerySrc), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, errout, code := run(exec.Command(buildLangBinForInterp(t), "-interp", src)); code != 0 || out != want || errout != "" {
			t.Fatalf("oracle: exit=%d stdout=%q stderr=%q; want %q", code, out, errout, want)
		}
	})
}

func TestPrctlQueryRefusedOffNativeHosts(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 { return prctl_query(3) as i32; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http", "arm64-freestanding", "x86-64-freestanding"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 || vs[0].Builtin != "prctl_query" || vs[0].Capability != "sysinfo" {
			t.Errorf("%s: violations %v, want prctl_query refused on the sysinfo capability", target, vs)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-android", "arm64-darwin"} {
		if vs := platforms.Enforce(prog, target); len(vs) != 0 {
			t.Errorf("%s refused prctl_query: %v", target, vs)
		}
	}
}
