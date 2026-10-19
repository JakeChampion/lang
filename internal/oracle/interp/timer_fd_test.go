package interp

import (
	"runtime"
	"testing"
)

// timer_fd is a handle poll waits on and tcp_close releases; it becomes ready
// once its milliseconds have passed.
func TestTimerFdBecomesReady(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("timerfd is Linux's; elsewhere timer_fd answers -1")
	}
	src := `function main(): i32 {
    let t: i32 = timer_fd(5);
    if (t < 0) { return 1; }
    if (poll([t], 2000) != 0) { return 2; }
    if (tcp_close(t) != 0) { return 3; }
    if (tcp_close(t) != 0 - 1) { return 4; }
    return 0;
}`
	if val, _ := runCapture(t, src); val != "0" {
		t.Errorf("timer_fd program = %s, want 0 (1 = no timer, 2 = poll did not see it fire, 3 = close failed, 4 = a closed handle closed again)", val)
	}
}
