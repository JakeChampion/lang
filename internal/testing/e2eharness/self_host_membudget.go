package e2eharness

import (
	"os"
	"strconv"
	"strings"
	"sync"
)

type buildMemLimiter struct {
	mu     sync.Mutex
	cond   *sync.Cond
	budget int
	used   int
}

func newBuildMemLimiter(budgetMB int) *buildMemLimiter {
	l := &buildMemLimiter{budget: budgetMB}
	l.cond = sync.NewCond(&l.mu)
	return l
}

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

func buildMem() *buildMemLimiter {
	buildMemOnce.Do(func() {
		buildMemInst = newBuildMemLimiter(buildMemBudgetMB())
	})
	return buildMemInst
}

func withBuildMemory(weightMB int, fn func() error) error {
	release := buildMem().acquire(weightMB)
	defer release()
	return fn()
}

func WithBuildMemoryMB(weightMB int, fn func() error) error {
	return withBuildMemory(weightMB, fn)
}

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
