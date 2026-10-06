package interp

import (
	"errors"
	"syscall"
	"testing"
	"time"
)

// An interrupted wait is waited again for what is left of its deadline,
// never for the whole timeout again, and an interruption past the deadline
// is a timeout (#11646).
func TestWaitRestartingKeepsTheDeadline(t *testing.T) {
	t.Run("interrupted then ready", func(t *testing.T) {
		var asked []int
		n, err := waitRestarting(1000, func(ms int) (int, error) {
			asked = append(asked, ms)
			if len(asked) == 1 {
				time.Sleep(20 * time.Millisecond)
				return 0, syscall.EINTR
			}
			return 3, nil
		})
		if n != 3 || err != nil {
			t.Fatalf("got (%d, %v), want (3, nil)", n, err)
		}
		if len(asked) != 2 || asked[0] != 1000 || asked[1] <= 0 || asked[1] > 990 {
			t.Fatalf("waits asked for %v ms, want 1000 then what was left of it", asked)
		}
	})
	t.Run("interrupted past the deadline", func(t *testing.T) {
		calls := 0
		start := time.Now()
		n, err := waitRestarting(50, func(ms int) (int, error) {
			calls++
			time.Sleep(time.Duration(ms) * time.Millisecond / 2)
			return 0, syscall.EINTR
		})
		if n != 0 || err != nil {
			t.Fatalf("got (%d, %v), want a timeout (0, nil)", n, err)
		}
		if d := time.Since(start); d > time.Second {
			t.Fatalf("a 50 ms wait interrupted %d times took %v", calls, d)
		}
	})
	t.Run("unbounded", func(t *testing.T) {
		var asked []int
		n, _ := waitRestarting(-1, func(ms int) (int, error) {
			asked = append(asked, ms)
			if len(asked) < 3 {
				return 0, syscall.EINTR
			}
			return 1, nil
		})
		if n != 1 || len(asked) != 3 || asked[1] != -1 || asked[2] != -1 {
			t.Fatalf("got %d after waits of %v ms, want 1 after three unbounded waits", n, asked)
		}
	})
	t.Run("other errors pass through", func(t *testing.T) {
		calls := 0
		_, err := waitRestarting(100, func(ms int) (int, error) {
			calls++
			return 0, syscall.EBADF
		})
		if !errors.Is(err, syscall.EBADF) || calls != 1 {
			t.Fatalf("got %v after %d calls, want EBADF after one", err, calls)
		}
	})
}
