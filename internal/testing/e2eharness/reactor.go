package e2eharness

// ReactorProbe exercises the reactor floor (#9853): a readiness set from
// reactor_new, a listener and a connection watched through reactor_ctl,
// waits that report the accept, the bytes, the writable side and the
// peer's close, two descriptors ready at once reported by one wait, the
// owned-buffer read of tcp_recv_into with its -EAGAIN when nothing is
// queued, the quiet timeout, and the -EINVAL of an op or an event buffer
// the floor refuses. A host may report readiness
// spuriously, so a wait is checked for the pair it must contain rather
// than for exactly one. Exit 42 and "ok" on stdout iff every check holds,
// else the number of the first failing check.
func ReactorProbe() string {
	return `import "core/int";

function fail(n: i32): i32 {
    print(int.int_to_string(n));
    return n;
}

// EINVAL as the target numbers it: wasi's own table on wasm.
function einval(): i32 {
    if (target_os() == "wasi") { return 28; }
    return 22;
}

function eagain(): i32 {
    if (target_os() == "darwin") { return 35; }
    if (target_os() == "wasi") { return 6; }
    return 11;
}

// The floor exposes -errno, including a kernel wait a signal interrupts
// where nothing restarts it. Keep the original deadline when retrying
// EINTR.
function wait_until(r: i32, events: i32[], timeout_ms: i32): i32 {
    let interrupted: i32 = 0 - 4;
    if (target_os() == "wasi") { interrupted = 0 - 27; }
    let deadline: i64 = monotonic_ns() + timeout_ms as i64 * (1000000 as i64);
    let remaining: i32 = timeout_ms;
    while (true) {
        let n: i32 = reactor_wait(r, events, remaining);
        if (n != interrupted) { return n; }
        let ns: i64 = deadline - monotonic_ns();
        if (ns <= (0 as i64)) { return 0; }
        remaining = ((ns + (999999 as i64)) / (1000000 as i64)) as i32;
    }
    return 0;
}

// One wait that must report want with the readiness bits in ready
// among its pairs; a host may report a socket ready spuriously, so other
// pairs are not a failure. Anything else is the failing check.
function expect_ready(r: i32, want: i32, ready: i32, timeout_ms: i32, check: i32): i32 {
    let events: i32[] = [0, 0, 0, 0, 0, 0, 0, 0];
    let n: i32 = wait_until(r, events, timeout_ms);
    if (n < 1 || n > 4) { return fail(check); }
    let i: i32 = 0;
    while (i < n) {
        if (events[i * 2] == want && (events[i * 2 + 1] & ready) == ready) { return 0; }
        i = i + 1;
    }
    return fail(check);
}

// One wait that must report both pairs: want_a with the bits in ready_a
// and want_b with the bits in ready_b. Two descriptors ready before the
// wait come back in one batch, which is what shows each of the kernel's
// events turned into its pair in the caller's array without treading on
// the next.
function expect_both(r: i32, want_a: i32, ready_a: i32, want_b: i32, ready_b: i32, timeout_ms: i32, check: i32): i32 {
    let events: i32[] = [0, 0, 0, 0, 0, 0, 0, 0];
    let n: i32 = wait_until(r, events, timeout_ms);
    if (n < 2 || n > 4) { return fail(check); }
    let seen_a: boolean = false;
    let seen_b: boolean = false;
    let i: i32 = 0;
    while (i < n) {
        if (events[i * 2] == want_a && (events[i * 2 + 1] & ready_a) == ready_a) { seen_a = true; }
        if (events[i * 2] == want_b && (events[i * 2 + 1] & ready_b) == ready_b) { seen_b = true; }
        i = i + 1;
    }
    if (seen_a && seen_b) { return 0; }
    return fail(check);
}

function main(): i32 {
    let r: i32 = reactor_new();
    if (r < 0) { return fail(1); }
    if (reactor_ctl(r, 9, 0, 0) != 0 - einval()) { return fail(2); }
    let one: i32[] = [0];
    if (reactor_wait(r, one, 0) != 0 - einval()) { return fail(3); }
    let events: i32[] = [0, 0, 0, 0];
    if (wait_until(r, events, 20) != 0) { return fail(4); }
    let any: u8[] = [0u8, 0u8, 0u8, 0u8];
    let ln: i32 = tcp_listen_with(any, 0, 4, false);
    if (ln < 0) { return fail(5); }
    let port: i32 = tcp_local_port(ln);
    if (reactor_ctl(r, 1, ln, 1) != 0) { return fail(6); }
    if (wait_until(r, events, 20) != 0) { return fail(7); }
    // Interest 4 is EPOLLEXCLUSIVE, which the kernel refuses on a
    // modification: asking it of a descriptor already in the set proves
    // the bit reached epoll_ctl rather than being masked off.
    if (target_os() == "linux" && reactor_ctl(r, 1, ln, 5) != 0 - einval()) { return fail(31); }
    let c: i32 = tcp_connect(16777343, port);
    if (c < 0) { return fail(8); }
    if (expect_ready(r, ln, 1, 2000, 9) != 0) { return 9; }
    let a: i32 = tcp_accept(ln);
    if (a < 0) { return fail(10); }
    if (reactor_ctl(r, 1, a, 1) != 0) { return fail(11); }
    if (reactor_ctl(r, 2, ln, 0) != 0) { return fail(12); }
    if (wait_until(r, events, 20) != 0) { return fail(13); }
    if (tcp_send(c, "hi") != 2) { return fail(14); }
    if (expect_ready(r, a, 1, 2000, 15) != 0) { return 15; }
    let buf: u8[] = [0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8];
    if (tcp_recv_into(a, buf) != 2) { return fail(16); }
    if (buf[0] != 104u8 || buf[1] != 105u8) { return fail(17); }
    if (reactor_ctl(r, 1, c, 2) != 0) { return fail(18); }
    if (expect_ready(r, c, 2, 2000, 19) != 0) { return 19; }
    if (reactor_ctl(r, 1, c, 1) != 0) { return fail(20); }
    // wasm has no non-blocking mode to switch on: its reads never block.
    if (target_os() != "wasi") {
        if (tcp_socket_ctl(a, 3, 1) != 0) { return fail(21); }
    }
    // Reading until -EAGAIN is what clears a readiness the host reported
    // spuriously, so the idle wait after it must be quiet. A serve loop
    // drains every readable event this way, so the empty read must also
    // leave nothing behind: the wasm leg runs under the leak census, which
    // is what catches the empty list the host materialises through
    // cabi_realloc (#10608).
    if (tcp_recv_into(a, buf) != 0 - eagain()) { return fail(22); }
    if (wait_until(r, events, 20) != 0) { return fail(23); }
    if (tcp_send(a, "yo") != 2) { return fail(29); }
    if (expect_ready(r, c, 1, 2000, 24) != 0) { return 24; }
    let got: u8[] = tcp_recv(c, 8);
    if (got.len() != 2 || got[0] != 121u8) { return fail(25); }
    // A socket is unwatched before it is closed: on wasm the pollables a
    // watch holds are children of the socket's streams.
    if (reactor_ctl(r, 2, c, 0) != 0) { return fail(26); }
    tcp_close(c);
    if (expect_ready(r, a, 1, 2000, 27) != 0) { return 27; }
    if (tcp_recv_into(a, buf) != 0) { return fail(28); }
    // a at its end and a second connection pending at the listener,
    // watched again: two descriptors ready before one wait, reported as
    // two pairs. Both stay ready, so no host timing is in the way.
    if (reactor_ctl(r, 1, ln, 1) != 0) { return fail(32); }
    let c2: i32 = tcp_connect(16777343, port);
    if (c2 < 0) { return fail(33); }
    if (expect_both(r, ln, 1, a, 1, 2000, 34) != 0) { return 34; }
    tcp_close(c2);
    if (reactor_ctl(r, 2, ln, 0) != 0) { return fail(35); }
    if (reactor_ctl(r, 2, a, 0) != 0) { return fail(29); }
    tcp_close(a);
    tcp_close(ln);
    if (reactor_ctl(r, 3, 0, 0) != 0) { return fail(30); }
    print("ok");
    return 42;
}
`
}
