//go:build unix

package e2eharness

import (
	"testing"
	"time"
)

func TestHostBuildSlotsBoundConcurrentBuilds(t *testing.T) {
	t.Setenv("FERN_SELFHOST_BUILD_CACHE", t.TempDir())
	first := acquireHostBuildSlot()
	second := acquireHostBuildSlot()
	third := make(chan func(), 1)
	go func() { third <- acquireHostBuildSlot() }()
	select {
	case <-third:
		t.Fatal("a third build ran while both slots were held")
	case <-time.After(300 * time.Millisecond):
	}
	first()
	select {
	case release := <-third:
		release()
	case <-time.After(5 * time.Second):
		t.Fatal("the third build did not get the released slot")
	}
	second()
}

func TestHostBuildSlotsWithoutSharedCacheDoNotWait(t *testing.T) {
	t.Setenv("FERN_SELFHOST_BUILD_CACHE", "")
	done := make(chan struct{})
	go func() {
		for i := 0; i < hostBuildSlots+1; i++ {
			defer acquireHostBuildSlot()()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("builds waited on a slot with no shared cache")
	}
}
