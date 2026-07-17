package e2eharness

import (
	"os"
	"strconv"
	"strings"
	"sync"
)

// Building a self-host DRIVER is memory-heavy: the Go x86-64 emit of a
// multi-thousand-function driver peaks at ~8 GB RSS, and GNU `as` on the
// emitted `.s` peaks at several GB more. A single driver build fits a
// 16 GB host, but the test suite builds several DISTINCT drivers, and Go
// test parallelism (`t.Parallel`, and `go test` running packages
// concurrently) can put two cold driver builds in their memory-peak phase
// at once — two ~8 GB peaks stacked crosses the host's RAM and trips the
// OOM killer (the exit-137 / "signal: killed" the project notes used to
// paper over with an 8 GB swap file).
//
// buildMemLimiter is a weighted counting semaphore over an estimated-RSS
// budget: each cold driver build acquires its estimated peak before it
// starts emitting and releases it after the link, so the harness never
// runs more heavy builds at once than the host's RAM can hold. On a
// 16 GB host that serialises the heavy builds (correct — you cannot build
// two 8 GB things at once in 16 GB, and serialising beats OOM+swap
// thrash); on a big host it still parallelises up to the budget. Disk-
// cache hits and the small self-host PROGRAM links never acquire — only
// the cold DRIVER emit+link, the sole multi-GB step.
type buildMemLimiter struct {
	mu     sync.Mutex
	cond   *sync.Cond
	budget int // MB
	used   int // MB
}

func newBuildMemLimiter(budgetMB int) *buildMemLimiter {
	l := &buildMemLimiter{budget: budgetMB}
	l.cond = sync.NewCond(&l.mu)
	return l
}

// acquire blocks until weightMB fits within the remaining budget, reserves
// it, and returns a release func (idempotent). A lone request always
// proceeds — even one whose weight alone exceeds the whole budget — so a
// single heavy build on a tiny host can never deadlock; it just runs
// without a concurrent partner.
func (l *buildMemLimiter) acquire(weightMB int) func() {
	if weightMB < 1 {
		weightMB = 1
	}
	l.mu.Lock()
	for l.used > 0 && l.used+weightMB > l.budget {
		l.cond.Wait()
	}
	l.used += weightMB
	l.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			l.used -= weightMB
			l.cond.Broadcast()
			l.mu.Unlock()
		})
	}
}

var (
	buildMemOnce sync.Once
	buildMemInst *buildMemLimiter
)

// buildMem returns the process-wide driver-build memory limiter, sized from
// the host's RAM (or FERN_BUILD_MEM_BUDGET_MB).
func buildMem() *buildMemLimiter {
	buildMemOnce.Do(func() {
		buildMemInst = newBuildMemLimiter(buildMemBudgetMB())
	})
	return buildMemInst
}

// withBuildMemory runs fn while holding a reservation of weightMB against
// the process-wide budget.
func withBuildMemory(weightMB int, fn func() error) error {
	release := buildMem().acquire(weightMB)
	defer release()
	return fn()
}

// buildMemBudgetMB is the total estimated-RSS budget for concurrent heavy
// builds. FERN_BUILD_MEM_BUDGET_MB overrides it; otherwise it's ~85% of
// MemTotal (leaving headroom for the Go test process, gcc's own compile,
// and page cache). Falls back to a conservative fixed value when
// /proc/meminfo is unreadable (e.g. a non-Linux CI runner).
func buildMemBudgetMB() int {
	if n, ok := envPositiveInt("FERN_BUILD_MEM_BUDGET_MB"); ok {
		return n
	}
	total := memTotalMB()
	if total <= 0 {
		return 12000
	}
	b := total * 85 / 100
	if b < 1 {
		b = total
	}
	return b
}

// heavyBuildWeightMB is the per-cold-driver-build reservation.
// FERN_BUILD_HEAVY_MB overrides it. The default (~8 GB) matches the
// measured per-driver peak (the Go emit ~8 GB; the assembler a few GB less
// after the rc-op call-form fall-back shrank the driver `.s`), so on a
// 16 GB host at most one cold driver build runs at a time.
func heavyBuildWeightMB() int {
	if n, ok := envPositiveInt("FERN_BUILD_HEAVY_MB"); ok {
		return n
	}
	return 8000
}

func envPositiveInt(name string) (int, bool) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// memTotalMB reads MemTotal from /proc/meminfo in MB, or 0 if unavailable.
func memTotalMB() int {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			if kb, err := strconv.Atoi(fields[1]); err == nil {
				return kb / 1024
			}
		}
		break
	}
	return 0
}
