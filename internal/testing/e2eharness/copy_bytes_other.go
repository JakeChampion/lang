//go:build !linux

package e2eharness

import "testing"

func copyByteOtherDevice(t *testing.T, source string) string {
	t.Helper()
	t.Skip("cross-device fixture requires Linux /dev/shm")
	return ""
}
