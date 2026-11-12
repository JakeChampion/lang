package e2ecompiler

import "testing"

func TestSelfHostGenericClosureDispatchInstancesRun(t *testing.T) {
	ownedResultRuntimeCase(t, "generic_closure_dispatch_instances")
}
