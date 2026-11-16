//go:build linux

package coreutils

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// noAtime opens a file without a change to its access time.
const noAtime = syscall.O_NOATIME

// A read of the tree keeps the access time of each file. Under relatime a
// plain read moves an access time that is older than the modification
// time, and the next run of `find -printf %A` then gives another time.
func TestReadTreeKeepsAccessTimes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	modified := time.Now().Add(-time.Minute)
	accessed := modified.Add(-time.Hour)
	if err := os.Chtimes(p, accessed, modified); err != nil {
		t.Fatal(err)
	}
	readTreeInto(t, dir, "", map[[2]uint64]int{}, treeOpts{})
	var st syscall.Stat_t
	if err := syscall.Stat(p, &st); err != nil {
		t.Fatal(err)
	}
	if got := time.Unix(st.Atim.Unix()); !got.Equal(accessed) {
		t.Errorf("the access time moved from %v to %v", accessed, got)
	}
}
