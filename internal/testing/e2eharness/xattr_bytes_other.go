//go:build !linux && !darwin

package e2eharness

import "testing"

func seedXattrBytesValue(t *testing.T, _, _, _ string) {
	t.Helper()
	t.Skip("extended attribute host fixtures require Linux or Darwin")
}

func readXattrBytesValue(t *testing.T, _, _ string) (string, bool) {
	t.Helper()
	t.Skip("extended attribute host fixtures require Linux or Darwin")
	return "", false
}
