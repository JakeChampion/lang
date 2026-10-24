package coreutils

import (
	"slices"
	"testing"
)

func TestCrossArgvCarriesTheEmulator(t *testing.T) {
	for _, tc := range []struct {
		name string
		qemu string
		want []string
	}{
		{"native", "", []string{"/bin/fern", "-o", "out"}},
		{"emulated", "qemu-aarch64", []string{"qemu-aarch64", "/bin/fern", "-o", "out"}},
		{"emulated with a sysroot", "qemu-aarch64 -L /usr/aarch64-linux-gnu",
			[]string{"qemu-aarch64", "-L", "/usr/aarch64-linux-gnu", "/bin/fern", "-o", "out"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FERN_COREUTILS_QEMU", tc.qemu)
			if got := crossArgv("/bin/fern", "-o", "out"); !slices.Equal(got, tc.want) {
				t.Errorf("crossArgv = %q, want %q", got, tc.want)
			}
		})
	}
}
