package e2eharness

// SocketV6Probe exercises the socket primitives over IPv6 loopback (#9853):
// a listener bound to ::1, a dial to it and the reply read back, then two
// datagram sockets on ::1 exchanging datagrams, the sender reported with
// the family byte 6 and its sixteen address bytes, and a connected reply;
// then a `::` listener taking a 127.0.0.1 peer, reported v4-mapped by
// op 10 (not on wasi:sockets, whose IPv6 socket stays IPv6-only).
// Exit 42 with "ok" on stdout when every check holds; when the host has no
// IPv6 the first bind answers -EAFNOSUPPORT (wasmtime reports the missing
// family as not-supported, -ENOTSUP) and the probe prints "nov6" and
// exits 43, which the test accepts only where Go cannot listen on ::1
// either.
func SocketV6Probe() string {
	return `import "core/int";

function fail(n: i32): i32 {
    print(int.int_to_string(n));
    return n;
}

function zeros(n: i32): u8[] {
    var b: u8[] = [];
    var i: i32 = 0;
    while (i < n) {
        b = b.append(0u8);
        i = i + 1;
    }
    return b;
}

function port_of(from: u8[]): i32 {
    return ((from[17] as i32) << 8) | (from[18] as i32);
}

// The sender record names ::1 as family 6.
function is_lo6(from: u8[]): boolean {
    if (from[0] != 6u8 || from[16] != 1u8) { return false; }
    var i: i32 = 1;
    while (i < 16) {
        if (from[i] != 0u8) { return false; }
        i = i + 1;
    }
    return true;
}

// The host has no IPv6: EAFNOSUPPORT, or wasmtime's not-supported.
function no_v6(rc: i32): boolean {
    if (target_os() == "darwin") { return rc == 0 - 47; }
    if (target_os() == "wasi") { return rc == 0 - 5 || rc == 0 - 58; }
    return rc == 0 - 97;
}

function main(): i32 {
    var lo6: u8[] = zeros(15).append(1u8);
    var peer: u8[] = [];
    var ln: i32 = tcp_listen_with(lo6, 0, 4, false);
    if (no_v6(ln)) {
        print("nov6");
        return 43;
    }
    if (ln < 0) { return fail(1); }
    var port: i32 = tcp_local_port(ln);
    if (port <= 0) { return fail(2); }
    var c: i32 = tcp_connect_with(lo6, port, false);
    if (c < 0) { return fail(3); }
    var a: i32 = tcp_accept(ln);
    if (a < 0) { return fail(4); }
    if (tcp_send(c, "hi") != 2) { return fail(5); }
    var got: u8[] = tcp_recv(a, 16);
    if (got.len() != 2 || got[0] != 104u8 || got[1] != 105u8) { return fail(6); }
    if (tcp_send(a, "yo") != 2) { return fail(7); }
    var back: u8[] = tcp_recv(c, 16);
    if (back.len() != 2 || back[0] != 121u8) { return fail(8); }
    tcp_close(a);
    tcp_close(c);
    tcp_close(ln);
    var s: i32 = udp_bind(lo6, 0);
    if (s < 0) { return fail(9); }
    var ps: i32 = tcp_local_port(s);
    if (ps <= 0) { return fail(10); }
    var t: i32 = udp_bind(lo6, 0);
    if (t < 0) { return fail(11); }
    var pt: i32 = tcp_local_port(t);
    if (pt <= 0 || pt == ps) { return fail(12); }
    if (udp_sendto(s, lo6, pt, "ping") != 4) { return fail(13); }
    var buf: u8[] = zeros(16);
    var from: u8[] = zeros(19);
    if (udp_recvfrom(t, buf, from) != 4 || buf[0] != 112u8 || buf[3] != 103u8) { return fail(14); }
    if (!is_lo6(from) || port_of(from) != ps) { return fail(15); }
    if (udp_connect(t, lo6, ps) != 0) { return fail(16); }
    if (udp_sendto(t, peer, 0, "pong") != 4) { return fail(17); }
    if (udp_recvfrom(s, buf, from) != 4 || buf[0] != 112u8 || buf[3] != 103u8) { return fail(18); }
    if (!is_lo6(from) || port_of(from) != pt) { return fail(19); }
    if (tcp_close(s) != 0 || tcp_close(t) != 0) { return fail(20); }
    // A :: listener takes an IPv4 peer too, which op 10 reports as the
    // v4-mapped address: family 6, groups 5 to 7 ffff:7f00:1, at the
    // dialler's port. Not on wasi:sockets, whose IPv6 socket stays
    // IPv6-only.
    if (target_os() != "wasi") {
        var dl: i32 = tcp_listen_with(zeros(16), 0, 4, false);
        if (dl < 0) { return fail(30); }
        var dc: i32 = tcp_connect(16777343, tcp_local_port(dl));
        if (dc < 0) { return fail(31); }
        var da: i32 = tcp_accept(dl);
        if (da < 0) { return fail(32); }
        if (tcp_socket_ctl(da, 10, 8) != 6) { return fail(33); }
        if (tcp_socket_ctl(da, 10, 4) != 0) { return fail(34); }
        if (tcp_socket_ctl(da, 10, 5) != 65535) { return fail(35); }
        if (tcp_socket_ctl(da, 10, 6) != 32512) { return fail(36); }
        if (tcp_socket_ctl(da, 10, 7) != 1) { return fail(37); }
        if (tcp_socket_ctl(da, 10, 9) != tcp_local_port(dc)) { return fail(38); }
        tcp_close(da);
        tcp_close(dc);
        tcp_close(dl);
    }
    print("ok");
    return 42;
}
`
}

// NetV6Probe is SocketV6Probe through std/net: `listen_at` on an IPv6
// `SocketAddr`, `connect` to it, `udp_socket`, `send_to` and `recv_from`
// with the sender read back as the `::1` address, and `set_peer` with
// `send`, then the `::` listener's IPv4 peer as `peer_addr` and
// `peer_key` report it. The same "nov6" answer where the host has no IPv6.
func NetV6Probe() string {
	return `import "core/int";
import "std/net";

function fail(n: i32): i32 {
    print(int.int_to_string(n));
    return n;
}

function zeros(n: i32): u8[] {
    var b: u8[] = [];
    var i: i32 = 0;
    while (i < n) {
        b = b.append(0u8);
        i = i + 1;
    }
    return b;
}

function lo6(port: i32): net.SocketAddr {
    return net.socket_addr(net.ipv6_loopback(), port);
}

// The host has no IPv6: EAFNOSUPPORT, or wasmtime's not-supported.
function no_v6(errno: i32): boolean {
    if (target_os() == "darwin") { return errno == 47; }
    if (target_os() == "wasi") { return errno == 5 || errno == 58; }
    return errno == 97;
}

function main(): i32 {
    var ln: i32 = 0;
    match (net.listen_at(lo6(0), net.listen_options())) {
        Ok(fd) => { ln = fd; },
        Err(e) => {
            if (no_v6(e.errno())) {
                print("nov6");
                return 43;
            }
            return fail(1);
        },
    }
    var port: i32 = 0;
    match (net.local_port(ln)) {
        Ok(p) => { port = p; },
        Err(e) => { return fail(2); },
    }
    var c: i32 = 0;
    match (net.connect(lo6(port))) {
        Ok(fd) => { c = fd; },
        Err(e) => { return fail(3); },
    }
    var a: i32 = tcp_accept(ln);
    if (a < 0) { return fail(4); }
    if (tcp_send(c, "hi") != 2) { return fail(5); }
    var got: u8[] = tcp_recv(a, 16);
    if (got.len() != 2 || got[0] != 104u8) { return fail(6); }
    tcp_close(a);
    tcp_close(c);
    tcp_close(ln);
    var s: i32 = 0;
    match (net.udp_socket(lo6(0))) {
        Ok(fd) => { s = fd; },
        Err(e) => { return fail(7); },
    }
    var ps: i32 = 0;
    match (net.local_port(s)) {
        Ok(p) => { ps = p; },
        Err(e) => { return fail(8); },
    }
    var t: i32 = 0;
    match (net.udp_socket(lo6(0))) {
        Ok(fd) => { t = fd; },
        Err(e) => { return fail(9); },
    }
    var pt: i32 = 0;
    match (net.local_port(t)) {
        Ok(p) => { pt = p; },
        Err(e) => { return fail(10); },
    }
    match (net.send_to(s, [112u8, 105u8, 110u8, 103u8], lo6(pt))) {
        Ok(n) => { if (n != 4) { return fail(11); } },
        Err(e) => { return fail(12); },
    }
    var buf: u8[] = zeros(16);
    match (net.recv_from(t, buf)) {
        Ok(got2) => {
            if (got2.0 != 4 || buf[0] != 112u8) { return fail(13); }
            if (!got2.1.eq(lo6(ps))) { return fail(14); }
        },
        Err(e) => { return fail(15); },
    }
    match (net.set_peer(t, lo6(ps))) {
        Ok(u) => { },
        Err(e) => { return fail(16); },
    }
    match (net.send(t, [112u8, 111u8, 110u8, 103u8])) {
        Ok(n) => { if (n != 4) { return fail(17); } },
        Err(e) => { return fail(18); },
    }
    match (net.recv_from(s, buf)) {
        Ok(got3) => {
            if (got3.0 != 4 || buf[3] != 103u8) { return fail(19); }
            if (!got3.1.eq(lo6(pt))) { return fail(20); }
        },
        Err(e) => { return fail(21); },
    }
    tcp_close(s);
    tcp_close(t);
    // The same :: listener through std/net: peer_addr hands the IPv4
    // peer back as the V4 it is, and peer_key keys it as one.
    if (target_os() != "wasi") {
        var dl: i32 = 0;
        match (net.listen_at(net.socket_addr(net.ipv6_unspecified(), 0), net.listen_options())) {
            Ok(fd) => { dl = fd; },
            Err(e) => { return fail(30); },
        }
        var dc: i32 = 0;
        match (net.connect(net.socket_addr(net.ipv4_loopback(), tcp_local_port(dl)))) {
            Ok(fd) => { dc = fd; },
            Err(e) => { return fail(31); },
        }
        var da: i32 = tcp_accept(dl);
        if (da < 0) { return fail(32); }
        match (net.peer_addr(da)) {
            Ok(pa) => { if (!pa.ip.eq(net.ipv4_loopback()) || pa.port != tcp_local_port(dc)) { return fail(33); } },
            Err(e) => { return fail(34); },
        }
        match (net.peer_key(da)) {
            Some(k) => { if (k != 16777343) { return fail(35); } },
            None => { return fail(36); },
        }
        tcp_close(da);
        tcp_close(dc);
        tcp_close(dl);
    }
    print("ok");
    return 42;
}
`
}
