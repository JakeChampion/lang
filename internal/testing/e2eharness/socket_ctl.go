package e2eharness

func SocketCtlProbe() string {
	return `import "core/int";

function fail(n: i32): i32 {
    print(n.to_string());
    return n;
}

// lo4_at reads the sender record tcp_addr_into writes: family 4, then
// 127.0.0.1 and twelve zero bytes, then the port.
function lo4_at(rec: u8[], port: i32): boolean {
    if (rec.len() != 19 || rec[0] != 4u8 || rec[1] != 127u8 || rec[4] != 1u8) { return false; }
    let i: i32 = 2;
    while (i < 17) {
        if (i != 4 && rec[i] != 0u8) { return false; }
        i = i + 1;
    }
    return ((rec[17] as i32) << 8 | rec[18] as i32) == port;
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
    let c: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], port, false);
    if (c < 0) { return fail(4); }
    let a: i32 = tcp_accept(ln);
    if (a < 0) { return fail(5); }
    if (tcp_socket_ctl(a, 2, 1) != 0) { return fail(7); }
    if (target_os() == "wasi") {
        // wasi:sockets has no no-delay switch.
        if (tcp_socket_ctl(a, 1, 1) != 0 - 58) { return fail(6); }
    } else {
        if (tcp_socket_ctl(a, 1, 1) != 0) { return fail(6); }
    }
    if (tcp_socket_ctl(c, 3, 1) != 0) { return fail(8); }
    let nothing: u8[] = [0u8, 0u8];
    if (tcp_recv_into(c, nothing) >= 0) { return fail(9); }
    if (tcp_socket_ctl(c, 3, 0) != 0) { return fail(10); }
    if (tcp_send_bytes(a, [104u8, 105u8]) != 2) { return fail(11); }
    if (tcp_socket_ctl(a, 4, 1) != 0) { return fail(12); }
    let got: u8[] = [0u8, 0u8, 0u8, 0u8];
    if (tcp_recv_into(c, got) != 2 || got[0] != 104u8 || got[1] != 105u8) { return fail(13); }
    if (tcp_recv_into(c, got) != 0) { return fail(14); }
    if (tcp_socket_ctl(a, 11, 0) >= 0) { return fail(15); }
    // tcp_addr_into answers the local address, or the peer's, as the
    // 19-byte sender record: the accepted side is 127.0.0.1 at the
    // listener's port, its peer 127.0.0.1 at the dialler's port. A
    // listener has no peer, and a record shorter than 19 bytes is refused.
    let rec: u8[] = [0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8];
    if (tcp_addr_into(a, false, rec) != 19 || !lo4_at(rec, port)) { return fail(29); }
    if (tcp_addr_into(a, true, rec) != 19 || !lo4_at(rec, tcp_local_port(c))) { return fail(30); }
    if (tcp_addr_into(ln, true, rec) >= 0) { return fail(31); }
    if (tcp_addr_into(a, false, [0u8, 0u8]) >= 0) { return fail(32); }
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

func NetSocketOptsProbe() string {
	return `import "core/int";
import "std/net";
import "std/errno";
import "std/async";
import "std/time";

function fail(n: i32): i32 {
    print(n.to_string());
    return n;
}

function ok(r: Result[void, net.NetError]): boolean {
    match (r) {
        Ok(u) => { return true; },
        Err(e) => { return false; },
    }
}

function unsupported(r: Result[void, net.NetError]): boolean {
    match (r) {
        Ok(u) => { return false; },
        Err(e) => { return e.errno() == errno.ENOTSUP; },
    }
}

// read_ready reads once into a fresh 16-byte buffer once sock is readable:
// the bytes read, or -1 for an error.
function read_ready(sock: i32): u8[] {
    let set: i32[] = [sock, 1];
    let woke: i32 = async.wait_any(set, Some(time.duration_millis(5000)));
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
    let c: i32 = 0;
    match (net.connect(net.socket_addr(net.ipv4_loopback(), port))) {
        Ok(fd) => { c = fd; },
        Err(e) => { return fail(3); },
    }
    let a: i32 = tcp_accept(ln);
    if (a < 0) { return fail(4); }
    if (!ok(net.set_keepalive(a, true))) { return fail(5); }
    if (target_os() == "wasi") {
        if (!unsupported(net.set_nodelay(a, true))) { return fail(6); }
    } else {
        if (!ok(net.set_nodelay(a, true))) { return fail(6); }
    }
    if (!ok(net.set_nonblocking(c, true))) { return fail(7); }
    let probe: u8[] = [0u8, 0u8];
    match (net.read(c, probe)) {
        Ok(n) => { return fail(8); },
        Err(e) => { if (!e.eq(net.WouldBlock)) { return fail(8); } },
    }
    if (!ok(net.set_nonblocking(c, false))) { return fail(9); }
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
    // Nothing listens on the port now, so a dial is refused, and the error
    // names it.
    match (net.connect(net.socket_addr(net.ipv4_loopback(), port))) {
        Ok(fd) => { return fail(16); },
        Err(e) => { if (!e.eq(net.ConnectionRefused)) { return fail(17); } },
    }
    print("ok");
    return 42;
}
`
}
