package e2ecompiler

import "testing"

func TestSelfHostNestedWorkerPrivateHeapsRun(t *testing.T) {
	ownedResultRuntimeCase(t, "nested_workers_keep_private_heaps")
}
