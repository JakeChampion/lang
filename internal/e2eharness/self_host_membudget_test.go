package e2eharness

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A lone acquire whose weight exceeds the whole budget still proceeds
// (never deadlocks) — a single heavy build on a tiny host must run.
func TestBuildMemLimiterLoneOverBudgetProceeds(t *testing.T) {
	l := newBuildMemLimiter(1000)
	done := make(chan struct{})
	go func() {
		release := l.acquire(9999) // > budget
		release()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("lone over-budget acquire deadlocked")
	}
}

// Two acquisitions that together exceed the budget do not run
// concurrently: the second blocks until the first releases.
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
			release := l.acquire(6000) // 6000+6000 > 10000 → must serialise
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

// Two acquisitions that both fit run concurrently — the limiter only
// blocks when the budget would be exceeded.
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
			release := l.acquire(4000) // 4000+4000 <= 10000 → both fit
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

// Release is idempotent — calling it twice must not double-credit the
// budget (which would let the limiter over-admit).
func TestBuildMemLimiterReleaseIdempotent(t *testing.T) {
	l := newBuildMemLimiter(1000)
	release := l.acquire(600)
	release()
	release() // second call is a no-op
	l.mu.Lock()
	used := l.used
	l.mu.Unlock()
	if used != 0 {
		t.Fatalf("used = %d after balanced release; want 0", used)
	}
}

// The budget derivation prefers the env override, else a positive value.
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

func TestHeavyBuildWeightEnvOverride(t *testing.T) {
	t.Setenv("FERN_BUILD_HEAVY_MB", "1234")
	if got := heavyBuildWeightMB(); got != 1234 {
		t.Fatalf("heavy weight with env override = %d; want 1234", got)
	}
	t.Setenv("FERN_BUILD_HEAVY_MB", "")
	if got := heavyBuildWeightMB(); got <= 0 {
		t.Fatalf("default heavy weight = %d; want positive", got)
	}
}
