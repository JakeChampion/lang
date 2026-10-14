package e2eharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var (
	shippedFernOnce sync.Once
	shippedFernPath string
	shippedFernErr  string
)

// ShippedFern is the `fern` a release ships: compiler/fern_main.fern built by
// the current self-host compiler for this host with the tree
// scripts/fern-embed-stage lays out embedded, so it carries its own stdlib,
// explanations and VERSION. Built once per process.
func ShippedFern(t testing.TB) string {
	t.Helper()
	host := hostSelfHostTarget()
	if host == "" {
		t.Skip("no self-host target runs natively on this host")
	}
	compiler := SelfHostCLI(t)
	shippedFernOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fern-shipped-")
		if err != nil {
			shippedFernErr = err.Error()
			return
		}
		stage := filepath.Join(dir, "embed")
		if out, err := exec.Command(RepoPath("scripts", "fern-embed-stage"), stage).CombinedOutput(); err != nil {
			shippedFernErr = "fern-embed-stage: " + err.Error() + "\n" + string(out)
			return
		}
		bin := filepath.Join(dir, "fern")
		cmd := exec.Command(compiler, "-target", host, "-embed", stage, "-o", bin,
			RepoPath("compiler", "fern_main.fern"), SelfHostStdlibRoot(t))
		cmd.Env = ChildEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			shippedFernErr = "build fern_main.fern: " + err.Error() + "\n" + string(out)
			return
		}
		shippedFernPath = bin
	})
	if shippedFernErr != "" {
		t.Fatal(shippedFernErr)
	}
	return shippedFernPath
}
