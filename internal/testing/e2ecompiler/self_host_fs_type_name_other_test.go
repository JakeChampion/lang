//go:build !darwin

package e2ecompiler

import "testing"

func hostFsTypeName(t *testing.T, path string) string {
	t.Helper()
	return ""
}
