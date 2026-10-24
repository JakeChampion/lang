package e2eharness

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuildMemLimiterLoneOverBudgetProceeds(t *testing.T) {
	l := newBuildMemLimiter(1000)
	done := make(chan struct{})
	go func() {
		release := l.acquire(9999)
		release()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("lone over-budget acquire deadlocked")
	}
}

func TestBuildMemLimiterSerialisesOverBudgetPair(t *testing.T) {
	l := newBuildMemLimiter(10000)
	var concurrent int32
	var maxConcurrent int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release := l.acquire(6000)
			cur := atomic.AddInt32(&concurrent, 1)
			for {
				m := atomic.LoadInt32(&maxConcurrent)
				if cur <= m || atomic.CompareAndSwapInt32(&maxConcurrent, m, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&concurrent, -1)
			release()
		}()
	}
	close(start)
	wg.Wait()
	if maxConcurrent > 1 {
		t.Fatalf("two over-budget builds ran concurrently (max=%d); expected serialisation", maxConcurrent)
	}
}

func TestBuildMemLimiterAllowsFittingPair(t *testing.T) {
	l := newBuildMemLimiter(10000)
	var maxConcurrent int32
	var concurrent int32
	var wg sync.WaitGroup
	reached := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release := l.acquire(4000)
			cur := atomic.AddInt32(&concurrent, 1)
			for {
				m := atomic.LoadInt32(&maxConcurrent)
				if cur <= m || atomic.CompareAndSwapInt32(&maxConcurrent, m, cur) {
					break
				}
			}
			reached <- struct{}{}
			time.Sleep(30 * time.Millisecond)
			atomic.AddInt32(&concurrent, -1)
			release()
		}()
	}
	wg.Wait()
	if maxConcurrent < 2 {
		t.Fatalf("two fitting builds did not run concurrently (max=%d)", maxConcurrent)
	}
}

func TestBuildMemLimiterReleaseIdempotent(t *testing.T) {
	l := newBuildMemLimiter(1000)
	release := l.acquire(600)
	release()
	release()
	l.mu.Lock()
	used := l.used
	l.mu.Unlock()
	if used != 0 {
		t.Fatalf("used = %d after balanced release; want 0", used)
	}
}

func TestBuildMemBudgetEnvOverride(t *testing.T) {
	t.Setenv("FERN_BUILD_MEM_BUDGET_MB", "4242")
	if got := buildMemBudgetMB(); got != 4242 {
		t.Fatalf("budget with env override = %d; want 4242", got)
	}
	t.Setenv("FERN_BUILD_MEM_BUDGET_MB", "")
	if got := buildMemBudgetMB(); got <= 0 {
		t.Fatalf("derived budget = %d; want positive", got)
	}
}

func TestGccBigLinkWeightScales(t *testing.T) {
	small := gccBigLinkWeightMB(1 << 20)
	x86 := gccBigLinkWeightMB(100 << 20)
	arm64 := gccBigLinkWeightMB(102 << 20)
	if small < 100 {
		t.Fatalf("weight for 1MB asm = %dMB; want a real floor", small)
	}
	if x86 <= small || x86 < 392 {
		t.Fatalf("weight for the 100MB x86-64 listing = %dMB; want at least its measured 392MB peak", x86)
	}
	if arm64 < 590 {
		t.Fatalf("weight for the 102MB arm64 listing = %dMB; want at least its measured 590MB peak", arm64)
	}
}
