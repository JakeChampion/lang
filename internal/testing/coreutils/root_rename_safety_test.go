package coreutils

import (
	"strings"
	"testing"
)

func TestRootRenameSafetyRejectsCrossDeviceFixtures(t *testing.T) {
	if err := rootRenameSafety(1, 1); err != nil {
		t.Fatalf("same-device fixture refused: %v", err)
	}
	err := rootRenameSafety(1, 2)
	if err == nil || !strings.Contains(err.Error(), "TMPDIR") || !strings.Contains(err.Error(), "GNU's EXDEV") {
		t.Fatalf("cross-device fixture needs an actionable refusal, got %v", err)
	}
}

func TestMvRootOperandRequiresSafeDestination(t *testing.T) {
	seen := false
	for _, inv := range mvCases(t) {
		for _, arg := range inv.args {
			if strings.Trim(arg, "/") == "" && arg != "" && strings.Contains(arg, "//") {
				seen = true
				if !inv.rootRename || inv.seedTree == nil {
					t.Fatalf("root operand in %q lacks the pre-execution fixture guard", inv.name)
				}
			}
		}
	}
	if !seen {
		t.Fatal("missing root-operand regression case")
	}
}
