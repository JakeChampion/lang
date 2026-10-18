package interp

import (
	"runtime"
	"testing"
)

// timer_fd and udp_send are hosted builtins the interpreter lacked, so a
// program calling either stopped with "undefined function" (#12662).
//
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

// udp_send sends a string datagram from a socket made for it, and answers -3
// for a host that is not a dotted quad before any socket exists.
func TestUdpSendDeliversAString(t *testing.T) {
	src := `function main(): i32 {
    let lo: u8[] = [127u8, 0u8, 0u8, 1u8];
    let u: i32 = udp_bind(lo, 0);
    if (u < 0) { return 1; }
    if (udp_send("127.0.0.1", tcp_local_port(u), "hello") != 5) { return 2; }
    let buf: u8[] = __alloc_u8(16);
    let from: u8[] = __alloc_u8(19);
    if (udp_recvfrom(u, buf, from) != 5) { return 3; }
    if (buf[0] != 104u8 || buf[4] != 111u8) { return 4; }
    if (udp_send("localhost", 9, "x") != 0 - 3) { return 5; }
    return 0;
}`
	if val, _ := runCapture(t, src); val != "0" {
		t.Errorf("udp_send program = %s, want 0", val)
	}
}
