//go:build unix

package e2eharness

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// hostBuildSlots bounds the cold driver builds that run at once on one host.
// Each build is a subprocess at 4 to 6 GB, so two fill a 16 GB runner.
const hostBuildSlots = 2

// acquireHostBuildSlot holds one of the host's build slots until the returned
// function runs. The processes that share a driver cache
// (FERN_SELFHOST_BUILD_CACHE) share the host's RAM too, so the slots are
// flock-held files in that directory. Without a shared cache there is no
// sibling process to wait for, and the in-process budget alone applies.
func acquireHostBuildSlot() func() {
	dir := diskCacheWriteDir()
	if dir == "" {
		return func() {}
	}
	var slots []*os.File
	for i := 0; i < hostBuildSlots; i++ {
		f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf(".build-slot-%d", i)), os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			continue
		}
		slots = append(slots, f)
	}
	if len(slots) == 0 {
		return func() {}
	}
	release := func() {
		for _, f := range slots {
			f.Close()
		}
	}
	// flock cannot wait on several files at once, so a waiter polls every
	// slot and takes whichever is released first.
	for {
		for _, f := range slots {
			if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
				return release
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}
