package coreutils

import (
	"slices"
	"testing"
)

// A cross leg (docs/COREUTILS.md) compiles the utilities for
// FERN_COREUTILS_TARGET and runs both sides under FERN_COREUTILS_QEMU. The
// self-host compiler is built for that target too, so it needs the emulator
// exactly as the utilities do — without it TestSelfHostCoreutilsParity died
// with "exec format error" on every utility, which is the whole cross leg of
// that gate silently unavailable from an x86-64 desk.
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
