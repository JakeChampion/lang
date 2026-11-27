//go:build unix

package coreutils

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
)

// A zero-length datagram ends a read block. The next operation on stdin
// can still read a later datagram, making a redundant read observable.
func TestCmpPartialFinalBlock(t *testing.T) {
	for _, tc := range []struct {
		name, file, skip string
		packets          []string
	}{
		{"equal partial block", "a", "0", []string{"a", "", "next"}},
		{"partial block after short reads", "abc", "0", []string{"a", "bc", "", "next"}},
		{"skip ends before comparison", "b", "3", []string{"a", "", "b", "", "next"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := writeFile(t, t.TempDir(), "other", tc.file)
			pair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
			if err != nil {
				t.Fatal(err)
			}
			in := os.NewFile(uintptr(pair[0]), "stdin")
			defer in.Close()
			defer syscall.Close(pair[1])
			for _, packet := range tc.packets {
				if err := syscall.Sendto(pair[1], []byte(packet), 0, nil); err != nil {
					t.Fatal(err)
				}
			}
			if err := syscall.SetNonblock(pair[0], true); err != nil {
				t.Fatal(err)
			}
			argv := crossArgv(fernBin(t, "cmp"), "-", other, tc.skip, "0")
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Stdin = in
			if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
				t.Fatalf("cmp: %v, %s", err, out)
			}
			var buf [16]byte
			n, _, err := syscall.Recvfrom(pair[0], buf[:], 0)
			if err != nil {
				t.Fatal(err)
			}
			if string(buf[:n]) != "next" {
				t.Fatalf("next datagram = %q", buf[:n])
			}
		})
	}
}
