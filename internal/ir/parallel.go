package ir

import (
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

// lowerJobs is the worker count for LowerWith's per-function loop:
// FERN_LOWER_JOBS when set (1 = sequential), else one worker per available
// CPU.
func lowerJobs() int {
	if s := os.Getenv("FERN_LOWER_JOBS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return runtime.GOMAXPROCS(0)
}

// forEach runs f(i) for every i in [0, n) on jobs workers and returns the
// error of the lowest i that failed, so a failure is reported the same
// whichever worker reached it first. Indices are handed out in order, and
// f must write only to its own index's output and to state it guards
// itself: the program-wide maps LowerWith threads into lowerFunc are read
// only, and the memo in typeMemo takes typeMemoMu.
func forEach(n, jobs int, f func(i int) error) error {
	if jobs <= 1 || n < 2 {
		for i := 0; i < n; i++ {
			if err := f(i); err != nil {
				return err
			}
		}
		return nil
	}
	if jobs > n {
		jobs = n
	}
	errs := make([]error, n)
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < jobs; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= n {
					return
				}
				errs[i] = f(i)
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
