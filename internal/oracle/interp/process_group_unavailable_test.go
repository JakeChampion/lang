//go:build !unix

package interp

import (
	"errors"
	"syscall"
	"testing"
)

func TestSetProcessGroupUnavailable(t *testing.T) {
	if err := hostSetProcessGroup(0, 0); !errors.Is(err, syscall.ENOSYS) {
		t.Fatalf("set process group = %v, want ENOSYS", err)
	}
}
