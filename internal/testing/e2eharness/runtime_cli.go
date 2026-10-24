package e2eharness

import "testing"

func RuntimeCLI(t testing.TB) string {
	t.Helper()
	target := HostSelfHostTarget()
	if target == "" {
		t.Skip("no self-host target runs natively on this host")
	}
	return CachedDriverBinFor(t, RepoPath("compiler"), "drivers/runtime_run.fern", target)
}
