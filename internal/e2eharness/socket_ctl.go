package e2eharness

// SocketCtlProbe exercises `tcp_listen_with` and `tcp_socket_ctl` (#9853)
// over a loopback connection: a listener with a chosen backlog and
// SO_REUSEPORT, a second listener on the same port where the target
// load-balances one (Linux), no-delay and keep-alive on the accepted side,
// a non-blocking read that answers empty rather than waiting, a write-side
// shutdown the peer reads as end of stream, and the -EINVAL an unknown op
// draws. Exit 42 and "ok" on stdout iff every check holds, else the number
// of the first failing check; a preview-2 wasm host reports only 0 or 1, so
// the wasm legs read stdout.
func SocketCtlProbe() string {
	return `import "core/int";

function fail(n: i32): i32 {
    print(int.int_to_string(n));
    return n;
}

function main(): i32 {
    var ln: i32 = tcp_listen_with(0, 4, true);
    if (ln < 0) { return fail(1); }
    var port: i32 = tcp_local_port(ln);
    if (port <= 0) { return fail(2); }
    // Linux lets a second SO_REUSEPORT listener share the port; it is closed
    // again before the dial so the one accept below is on the first.
    if (target_os() == "linux") {
        var ln2: i32 = tcp_listen_with(port, 4, true);
        if (ln2 < 0) { return fail(3); }
        tcp_close(ln2);
    }
    // 127.0.0.1 packed in network order: 127 | 1 << 24.
    var c: i32 = tcp_connect(16777343, port);
    if (c < 0) { return fail(4); }
    var a: i32 = tcp_accept(ln);
    if (a < 0) { return fail(5); }
    if (tcp_socket_ctl(a, 2, 1) != 0) { return fail(7); }
    if (target_os() == "wasi") {
        // wasi:sockets has no no-delay and no blocking mode to switch off.
        if (tcp_socket_ctl(a, 1, 1) != 0 - 58) { return fail(6); }
        if (tcp_socket_ctl(c, 3, 1) != 0 - 58) { return fail(8); }
    } else {
        if (tcp_socket_ctl(a, 1, 1) != 0) { return fail(6); }
        if (tcp_socket_ctl(c, 3, 1) != 0) { return fail(8); }
        var nothing: u8[] = tcp_recv(c, 16);
        if (nothing.len() != 0) { return fail(9); }
        if (tcp_socket_ctl(c, 3, 0) != 0) { return fail(10); }
    }
    if (tcp_send(a, "hi") != 2) { return fail(11); }
    if (tcp_socket_ctl(a, 4, 1) != 0) { return fail(12); }
    var got: u8[] = tcp_recv(c, 16);
    if (got.len() != 2 || got[0] != 104u8 || got[1] != 105u8) { return fail(13); }
    var eof: u8[] = tcp_recv(c, 16);
    if (eof.len() != 0) { return fail(14); }
    if (tcp_socket_ctl(a, 9, 0) >= 0) { return fail(15); }
    tcp_close(c);
    tcp_close(a);
    tcp_close(ln);
    print("ok");
    return 42;
}
`
}

// NetSocketOptsProbe is SocketCtlProbe through std/net's typed faces:
// `listen_with` with options, `set_keepalive`, `set_nodelay`,
// `set_nonblocking` and `shutdown`, each answering `Result[(), NetError]`
// with the errno mapped, and the errno a refused bind and a refused dial
// report on each target. Same verdict channel: exit 42 and "ok", or the
// first failing check.
func NetSocketOptsProbe() string {
	return `import "core/int";
import "std/net";

function fail(n: i32): i32 {
    print(int.int_to_string(n));
    return n;
}

function ok(r: Result[(), net.NetError]): boolean {
    match (r) {
        Ok(u) => { return true; },
        Err(e) => { return false; },
    }
}

function unsupported(r: Result[(), net.NetError]): boolean {
    match (r) {
        Ok(u) => { return false; },
        Err(e) => { return e.errno() == 58; },
    }
}

function main(): i32 {
    var opts: net.ListenOptions = net.ListenOptions { ...net.listen_options(), backlog: 4, reuse_port: true };
    var ln: i32 = 0;
    match (net.listen_with(0, opts)) {
        Ok(fd) => { ln = fd; },
        Err(e) => { return fail(1); },
    }
    var port: i32 = tcp_local_port(ln);
    if (port <= 0) { return fail(2); }
    var c: i32 = tcp_connect(16777343, port);
    if (c < 0) { return fail(3); }
    var a: i32 = tcp_accept(ln);
    if (a < 0) { return fail(4); }
    if (!ok(net.set_keepalive(a, true))) { return fail(5); }
    if (target_os() == "wasi") {
        if (!unsupported(net.set_nodelay(a, true))) { return fail(6); }
        if (!unsupported(net.set_nonblocking(c, true))) { return fail(7); }
    } else {
        if (!ok(net.set_nodelay(a, true))) { return fail(6); }
        if (!ok(net.set_nonblocking(c, true))) { return fail(7); }
        var nothing: u8[] = tcp_recv(c, 16);
        if (nothing.len() != 0) { return fail(8); }
        if (!ok(net.set_nonblocking(c, false))) { return fail(9); }
    }
    if (tcp_send(a, "hi") != 2) { return fail(10); }
    if (!ok(net.shutdown(a, net.Write))) { return fail(11); }
    var got: u8[] = tcp_recv(c, 16);
    if (got.len() != 2 || got[0] != 104u8 || got[1] != 105u8) { return fail(12); }
    var eof: u8[] = tcp_recv(c, 16);
    if (eof.len() != 0) { return fail(13); }
    match (net.listen_with(port, net.listen_options())) {
        Ok(fd) => { return fail(14); },
        Err(e) => { if (!e.eq(net.AddrInUse)) { return fail(15); } },
    }
    tcp_close(c);
    tcp_close(a);
    tcp_close(ln);
    // Nothing listens on the port now, so a dial is refused, and the errno
    // the builtin answers names it.
    var refused: i32 = tcp_connect(16777343, port);
    if (refused >= 0) { return fail(16); }
    if (!net.error_from_errno(refused).eq(net.ConnectionRefused)) { return fail(17); }
    print("ok");
    return 42;
}
`
}
