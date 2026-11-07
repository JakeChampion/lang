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

func ShippedFern(t testing.TB) string {
	t.Helper()
	host := HostSelfHostTarget()
	if host == "" {
		t.Skip("no self-host target runs natively on this host")
	}
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
		shippedFernPath = CachedCLIBuild(t, CLIBuild{Target: host, Dir: RepoPath("compiler"), Entry: "fern_main.fern", Embed: stage})
	})
	if shippedFernErr != "" {
		t.Fatal(shippedFernErr)
	}
	return shippedFernPath
}
