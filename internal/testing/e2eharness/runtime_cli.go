package e2eharness

import "testing"

// RuntimeCLI builds the compiler entry for internal runtime probes.
// User-source admission stays on SelfHostCLI and ShippedFern.
func RuntimeCLI(t testing.TB) string {
	t.Helper()
	target := HostSelfHostTarget()
	if target == "" {
		t.Skip("no self-host target runs natively on this host")
	}
	return CachedDriverBinFor(t, RepoPath("compiler"), "drivers/runtime_run.fern", target)
}
