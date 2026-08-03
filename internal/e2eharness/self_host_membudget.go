package e2eharness

import (
	"math"
	"os"
	"runtime/debug"
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

// WithBuildMemoryMB is withBuildMemory for tests outside this package
// that run a heavy build of their own — a `fern` subprocess emitting a
// multi-hundred-MB image, say — and must not stack their peak on top of
// a concurrent driver build's.
func WithBuildMemoryMB(weightMB int, fn func() error) error {
	return withBuildMemory(weightMB, fn)
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
// FERN_BUILD_HEAVY_MB overrides it. The default matches the measured
// per-driver peak under the soft emit memory limit (withEmitMemLimit),
// the OpExt side-table Op shrink, and the backends' per-function IR
// release: the Go emit peaks ~3.7 GB RSS (down from ~9 GB uncapped),
// and the in-process native assemble that follows peaks ~2.6 GB, so
// ~4.3 GB covers the build's worst phase with margin. Two cold builds
// can now run concurrently within a 16 GB host's budget.
func heavyBuildWeightMB() int {
	if n, ok := envPositiveInt("FERN_BUILD_HEAVY_MB"); ok {
		return n
	}
	return 4300
}

// The Go x86-64 emit of a self-host driver allocates hard: its LIVE heap
// peaks ~2.6 GB (see emitMemLimitMB), but at the default GOGC the runtime
// lets the heap double between collections, so the process peaked ~9 GB
// RSS — over half the emit's footprint was garbage awaiting collection.
// Capping the runtime's soft memory limit (GOMEMLIMIT semantics) during a
// heavy build makes the GC keep the heap near the cap instead, and the
// backends' per-function IR release keeps shrinking the live set as
// emission proceeds: measured on the asm_ir_run driver emit, cap + Op
// shrink + release run at ~3.7 GB peak RSS in ~40 s vs the original
// 9.0 GB / 134 s (4-core host — fewer huge-heap GC pauses and less page
// pressure), with byte-identical output.
//
// The limit is process-wide, so it is REFCOUNTED and scaled: while n
// heavy builds are active the limit is n * per-build-cap, and when the
// last one releases it goes back to unlimited. Everything else in a test
// process lives in a few hundred MB, far under any cap, so bystander
// tests never feel it.
var (
	emitMemLimitMu     sync.Mutex
	emitMemLimitActive int
)

// emitMemLimitMB is the per-heavy-build soft heap cap in MB.
// FERN_EMIT_MEMLIMIT_MB overrides it; <= 0 disables the cap entirely.
// The emit's live heap peaks ~2.6 GB (AST + checker info + the full IR,
// right as emission starts — ir.Op's OpExt side-table shrank the IR to
// ~96 B/op, and the per-function IR release in the backends then shrinks
// the live set further as the output grows). The default leaves ~1 GB of
// headroom above that live peak; a cap below the live set would make the
// GC thrash, not save memory (it is a soft limit; the process would
// still finish). Measured on the asm_ir_run driver: this cap + the Op
// shrink + the IR release run the emit at ~40 s / 3.7 GB RSS vs the
// original 134 s / 9.0 GB, output byte-identical.
// CI-DARK: FERN_EMIT_MEMLIMIT_MB — a tuning override with a default, not a
// gate. CI exercises the default (3600), which is the configuration that
// matters; the override exists to lower the cap on a smaller host.
func emitMemLimitMB() int {
	if v := strings.TrimSpace(os.Getenv("FERN_EMIT_MEMLIMIT_MB")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 3600
		}
		return n // <= 0 disables
	}
	return 3600
}

// withEmitMemLimit runs fn with the Go runtime's soft memory limit capped
// at (active heavy builds) * emitMemLimitMB. Nested/concurrent holders
// stack the limit; the last release restores unlimited.
func withEmitMemLimit(fn func() error) error {
	per := emitMemLimitMB()
	if per <= 0 {
		return fn()
	}
	adjust := func(delta int) {
		emitMemLimitMu.Lock()
		defer emitMemLimitMu.Unlock()
		emitMemLimitActive += delta
		if emitMemLimitActive > 0 {
			debug.SetMemoryLimit(int64(emitMemLimitActive) * int64(per) << 20)
		} else {
			debug.SetMemoryLimit(math.MaxInt64)
		}
	}
	adjust(1)
	defer adjust(-1)
	return fn()
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
