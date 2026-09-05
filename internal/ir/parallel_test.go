package ir

import (
	"errors"
	"sync"
	"testing"
)

func TestForEachVisitsEveryIndexOnce(t *testing.T) {
	for _, jobs := range []int{1, 3, 8, 64} {
		const n = 37
		var mu sync.Mutex
		seen := make([]int, n)
		if err := forEach(n, jobs, func(i int) error {
			mu.Lock()
			seen[i]++
			mu.Unlock()
			return nil
		}); err != nil {
			t.Fatalf("jobs=%d: unexpected error %v", jobs, err)
		}
		for i, c := range seen {
			if c != 1 {
				t.Fatalf("jobs=%d: index %d visited %d times", jobs, i, c)
			}
		}
	}
}

// The error reported is the lowest failing index's, whichever worker got
// there first, so a failing program names the same function every run.
func TestForEachReportsLowestIndexError(t *testing.T) {
	errAt := func(i int) error { return errors.New("fail") }
	for _, jobs := range []int{1, 4} {
		e5, e9 := errAt(5), errAt(9)
		err := forEach(20, jobs, func(i int) error {
			switch i {
			case 5:
				return e5
			case 9:
				return e9
			}
			return nil
		})
		if err != e5 {
			t.Fatalf("jobs=%d: got %v, want the index-5 error", jobs, err)
		}
	}
}

func TestLowerJobsEnv(t *testing.T) {
	t.Setenv("FERN_LOWER_JOBS", "1")
	if got := lowerJobs(); got != 1 {
		t.Fatalf("FERN_LOWER_JOBS=1: got %d", got)
	}
	t.Setenv("FERN_LOWER_JOBS", "3")
	if got := lowerJobs(); got != 3 {
		t.Fatalf("FERN_LOWER_JOBS=3: got %d", got)
	}
	t.Setenv("FERN_LOWER_JOBS", "nonsense")
	if got := lowerJobs(); got < 1 {
		t.Fatalf("unparseable FERN_LOWER_JOBS: got %d, want the CPU count", got)
	}
}
