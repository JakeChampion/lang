package e2eharness

// RelistenProbe binds a port again right after the server closed a
// connection on it first (#12237). The server's half of that connection
// lingers in FIN_WAIT or TIME_WAIT, so both listeners must set
// SO_REUSEADDR before bind or the second bind is EADDRINUSE: through
// `tcp_listen_with`, then through `tcp_listen`. Exit 42 and "ok" on stdout
// iff every check holds, else the number of the first failing check.
func RelistenProbe() string {
	return `import "core/int";

function fail(n: i32): i32 {
    print(int.int_to_string(n));
    return n;
}

function main(): i32 {
    let any: u8[] = [0u8, 0u8, 0u8, 0u8];
    let ln: i32 = tcp_listen_with(any, 0, 4, false);
    if (ln < 0) { return fail(1); }
    let port: i32 = tcp_local_port(ln);
    if (port <= 0) { return fail(2); }
    // 127.0.0.1 packed in network order: 127 | 1 << 24.
    let c: i32 = tcp_connect(16777343, port);
    if (c < 0) { return fail(3); }
    let a: i32 = tcp_accept(ln);
    if (a < 0) { return fail(4); }
    if (tcp_send(a, "x") != 1) { return fail(5); }
    tcp_close(a);
    let got: u8[] = tcp_recv(c, 16);
    if (got.len() != 1 || got[0] != 120u8) { return fail(6); }
    let eof: u8[] = tcp_recv(c, 16);
    if (eof.len() != 0) { return fail(7); }
    tcp_close(c);
    tcp_close(ln);
    let again: i32 = tcp_listen_with(any, port, 4, false);
    if (again < 0) { return fail(8); }
    tcp_close(again);
    let plain: i32 = tcp_listen(port);
    if (plain < 0) { return fail(9); }
    tcp_close(plain);
    print("ok");
    return 42;
}
`
}
