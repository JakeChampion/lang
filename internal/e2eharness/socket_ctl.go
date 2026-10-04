package e2eharness

// SocketCtlProbe exercises `tcp_listen_with` and `tcp_socket_ctl` (#9853)
// over a loopback connection: a listener with a chosen backlog and
// SO_REUSEPORT, a second listener on the same port where the target
// load-balances one (Linux), no-delay and keep-alive on the accepted side,
// a non-blocking read that answers empty rather than waiting, a write-side
// shutdown the peer reads as end of stream, the -EINVAL an unknown op
// draws, the CPU steering of a SO_REUSEPORT group (op 8) where the
// host has it, and the local address (op 9) and the peer's (op 10) as
// 16-bit groups and a port. Exit 42 and "ok" on stdout iff every check holds, else the number
// of the first failing check; a preview-2 wasm host reports only 0 or 1, so
// the wasm legs read stdout.
func SocketCtlProbe() string {
	return `import "core/int";

function fail(n: i32): i32 {
    print(int.int_to_string(n));
    return n;
}

function main(): i32 {
    let any: u8[] = [0u8, 0u8, 0u8, 0u8];
    let ln: i32 = tcp_listen_with(any, 0, 4, true);
    if (ln < 0) { return fail(1); }
    let port: i32 = tcp_local_port(ln);
    if (port <= 0) { return fail(2); }
    // Linux lets a second SO_REUSEPORT listener share the port; it is closed
    // again before the dial so the one accept below is on the first.
    if (target_os() == "linux") {
        let ln2: i32 = tcp_listen_with(any, port, 4, true);
        if (ln2 < 0) { return fail(3); }
        tcp_close(ln2);
    }
    // 127.0.0.1 packed in network order: 127 | 1 << 24.
    let c: i32 = tcp_connect(16777343, port);
    if (c < 0) { return fail(4); }
    let a: i32 = tcp_accept(ln);
    if (a < 0) { return fail(5); }
    if (tcp_socket_ctl(a, 2, 1) != 0) { return fail(7); }
    if (target_os() == "wasi") {
        // wasi:sockets has no no-delay and no blocking mode to switch off.
        if (tcp_socket_ctl(a, 1, 1) != 0 - 58) { return fail(6); }
        if (tcp_socket_ctl(c, 3, 1) != 0 - 58) { return fail(8); }
    } else {
        if (tcp_socket_ctl(a, 1, 1) != 0) { return fail(6); }
        if (tcp_socket_ctl(c, 3, 1) != 0) { return fail(8); }
        let nothing: u8[] = tcp_recv(c, 16);
        if (nothing.len() != 0) { return fail(9); }
        if (tcp_socket_ctl(c, 3, 0) != 0) { return fail(10); }
    }
    if (tcp_send(a, "hi") != 2) { return fail(11); }
    if (tcp_socket_ctl(a, 4, 1) != 0) { return fail(12); }
    let got: u8[] = tcp_recv(c, 16);
    if (got.len() != 2 || got[0] != 104u8 || got[1] != 105u8) { return fail(13); }
    let eof: u8[] = tcp_recv(c, 16);
    if (eof.len() != 0) { return fail(14); }
    if (tcp_socket_ctl(a, 11, 0) >= 0) { return fail(15); }
    // Op 9 is the local address as 16-bit groups and the port: the
    // accepted side is 127.0.0.1, so family 4, 127 << 8 then 1, nothing
    // past group 1, and the listener's port.
    if (tcp_socket_ctl(a, 9, 8) != 4) { return fail(17); }
    if (tcp_socket_ctl(a, 9, 0) != 32512) { return fail(18); }
    if (tcp_socket_ctl(a, 9, 1) != 1) { return fail(19); }
    if (tcp_socket_ctl(a, 9, 7) != 0) { return fail(20); }
    if (tcp_socket_ctl(a, 9, 9) != port) { return fail(21); }
    if (tcp_socket_ctl(a, 9, 10) >= 0) { return fail(22); }
    // Op 10 is the peer's, the same way: the dialling side, 127.0.0.1 at
    // its own port; a listener has no peer.
    if (tcp_socket_ctl(a, 10, 8) != 4) { return fail(23); }
    if (tcp_socket_ctl(a, 10, 0) != 32512) { return fail(24); }
    if (tcp_socket_ctl(a, 10, 1) != 1) { return fail(25); }
    if (tcp_socket_ctl(a, 10, 7) != 0) { return fail(26); }
    if (tcp_socket_ctl(a, 10, 9) != tcp_local_port(c)) { return fail(27); }
    if (tcp_socket_ctl(ln, 10, 8) >= 0) { return fail(28); }
    // Op 8 steers the listener's SO_REUSEPORT group by CPU: attached on
    // Linux (-ENOPROTOOPT where the host lacks the option, as qemu-user
    // does), -ENOTSUP on Darwin and on wasm.
    if (target_os() == "linux") {
        let steered: i32 = tcp_socket_ctl(ln, 8, 0);
        if (steered != 0 && steered != 0 - 92) { return fail(16); }
    } else if (target_os() == "wasi") {
        if (tcp_socket_ctl(ln, 8, 0) != 0 - 58) { return fail(16); }
    } else {
        if (tcp_socket_ctl(ln, 8, 0) != 0 - 45) { return fail(16); }
    }
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
// `set_nonblocking`, `read`, `write`, `local_addr`, `peer_addr` and
// `shutdown`, each answering a `Result` with the errno mapped, and the
// errno a refused bind and a refused dial report on each target. A read
// waits for readability first, since a wasi:sockets read never blocks.
// Same verdict channel: exit 42 and "ok", or the first failing check.
func NetSocketOptsProbe() string {
	return `import "core/int";
import "std/net";
import "std/async";

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

// read_ready reads once into a fresh 16-byte buffer once sock is readable:
// the bytes read, or -1 for an error.
function read_ready(sock: i32): u8[] {
    let set: i32[] = [sock, 1];
    let woke: i32 = async.wait_any(set, 5000);
    let buf: u8[] = [0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8];
    match (net.read(sock, buf)) {
        Ok(n) => {
            let out: u8[] = [];
            let i: i32 = 0;
            while (i < n) {
                out = out.append(buf[i]);
                i = i + 1;
            }
            return out;
        },
        Err(e) => { return [255u8]; },
    }
}

function main(): i32 {
    let opts: net.ListenOptions = net.ListenOptions { ...net.listen_options(), backlog: 4, reuse_port: true };
    let ln: i32 = 0;
    match (net.listen_with(0, opts)) {
        Ok(fd) => { ln = fd; },
        Err(e) => { return fail(1); },
    }
    let port: i32 = tcp_local_port(ln);
    if (port <= 0) { return fail(2); }
    let c: i32 = tcp_connect(16777343, port);
    if (c < 0) { return fail(3); }
    let a: i32 = tcp_accept(ln);
    if (a < 0) { return fail(4); }
    if (!ok(net.set_keepalive(a, true))) { return fail(5); }
    if (target_os() == "wasi") {
        if (!unsupported(net.set_nodelay(a, true))) { return fail(6); }
        if (!unsupported(net.set_nonblocking(c, true))) { return fail(7); }
    } else {
        if (!ok(net.set_nodelay(a, true))) { return fail(6); }
        if (!ok(net.set_nonblocking(c, true))) { return fail(7); }
        let probe: u8[] = [0u8, 0u8];
        match (net.read(c, probe)) {
            Ok(n) => { return fail(8); },
            Err(e) => { if (!e.eq(net.WouldBlock)) { return fail(8); } },
        }
        if (!ok(net.set_nonblocking(c, false))) { return fail(9); }
    }
    match (net.write(a, [104u8, 105u8])) {
        Ok(n) => { if (n != 2) { return fail(10); } },
        Err(e) => { return fail(10); },
    }
    match (net.local_addr(a)) {
        Ok(la) => { if (!la.ip.eq(net.ipv4_loopback()) || la.port != port) { return fail(16); } },
        Err(e) => { return fail(17); },
    }
    match (net.peer_addr(a)) {
        Ok(pa) => { if (!pa.ip.eq(net.ipv4_loopback()) || pa.port != tcp_local_port(c)) { return fail(18); } },
        Err(e) => { return fail(19); },
    }
    if (!ok(net.shutdown(a, net.Write))) { return fail(11); }
    let got: u8[] = read_ready(c);
    if (got.len() != 2 || got[0] != 104u8 || got[1] != 105u8) { return fail(12); }
    let eof: u8[] = read_ready(c);
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
    let refused: i32 = tcp_connect(16777343, port);
    if (refused >= 0) { return fail(16); }
    if (!net.error_from_errno(refused).eq(net.ConnectionRefused)) { return fail(17); }
    print("ok");
    return 42;
}
`
}
