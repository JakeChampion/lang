package e2eharness

// SendQueueProbe exercises `tcp_socket_ctl` op 6, the bytes queued to
// send that the peer has not acknowledged, over a loopback connection: a
// fresh connection has nothing queued; once non-blocking sends have
// filled the socket, with the peer reading nothing, the count is
// positive; and once the peer has read everything it falls back to zero.
// On wasm the op answers -ENOTSUP (58). Exit 42 and "ok" on stdout iff
// every check holds, else the number of the first failing check.
func SendQueueProbe() string {
	return `import "core/int";
import "std/string";

function fail(n: i32): i32 {
    print(int.int_to_string(n));
    return n;
}

function main(): i32 {
    var any: u8[] = [0u8, 0u8, 0u8, 0u8];
    var ln: i32 = tcp_listen_with(any, 0, 4, false);
    if (ln < 0) { return fail(1); }
    var port: i32 = tcp_local_port(ln);
    if (port <= 0) { return fail(2); }
    var c: i32 = tcp_connect(16777343, port);
    if (c < 0) { return fail(3); }
    var a: i32 = tcp_accept(ln);
    if (a < 0) { return fail(4); }
    if (target_os() == "wasi") {
        if (tcp_socket_ctl(a, 6, 0) != 0 - 58) { return fail(5); }
    } else {
        if (tcp_socket_ctl(a, 6, 0) != 0) { return fail(6); }
        if (tcp_socket_ctl(a, 3, 1) != 0) { return fail(7); }
        var chunk: string = "x".repeat(65536);
        var sent: i32 = 0;
        var refused: boolean = false;
        var rounds: i32 = 0;
        while (rounds < 1024) {
            var n: i32 = tcp_send(a, chunk);
            if (n <= 0) { refused = true; break; }
            sent = sent + n;
            rounds = rounds + 1;
        }
        // The kernel refused a send, so the queue holds unacknowledged bytes.
        if (!refused || sent < 65536) { return fail(8); }
        if (tcp_socket_ctl(a, 6, 0) <= 0) { return fail(9); }
        var total: i32 = 0;
        while (total < sent) {
            var got: u8[] = tcp_recv(c, 65536);
            if (got.len() == 0) { return fail(10); }
            total = total + got.len();
        }
        var waited: i32 = 0;
        while (waited < 200 && tcp_socket_ctl(a, 6, 0) > 0) {
            sleep_ms(10 as i64);
            waited = waited + 1;
        }
        if (tcp_socket_ctl(a, 6, 0) != 0) { return fail(11); }
    }
    tcp_close(c);
    tcp_close(a);
    tcp_close(ln);
    print("ok");
    return 42;
}
`
}
