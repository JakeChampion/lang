package e2ecompiler

import "testing"

func TestSelfHostOwnedValueCellRun(t *testing.T) {
	ownedResultRuntimeCase(t, "cell_constructor_result_ownership")
}
