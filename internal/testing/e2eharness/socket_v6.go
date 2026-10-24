package e2eharness

func SocketV6Probe() string {
	return `import "core/int";
import "std/net";

// recv reads once from fd within five seconds: the bytes, none at the end
// of the stream, when nothing came, or when the read failed.
function recv(fd: i32, max: i32): u8[] {
    match (net.read_watched(fd, max, 5000i64, 1)) {
        Ok(net.Chunk(b)) => { return b; },
        _ => {}
    }
    let none: u8[] = [];
    return none;
}

function fail(n: i32): i32 {
    print(int.int_to_string(n));
    return n;
}

function zeros(n: i32): u8[] {
    let b: u8[] = [];
    let i: i32 = 0;
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
    let i: i32 = 1;
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
    let lo6: u8[] = zeros(15).append(1u8);
    let peer: u8[] = [];
    let ln: i32 = tcp_listen_with(lo6, 0, 4, false);
    if (no_v6(ln)) {
        print("nov6");
        return 43;
    }
    if (ln < 0) { return fail(1); }
    let port: i32 = tcp_local_port(ln);
    if (port <= 0) { return fail(2); }
    let c: i32 = tcp_connect_with(lo6, port, false);
    if (c < 0) { return fail(3); }
    let a: i32 = tcp_accept(ln);
    if (a < 0) { return fail(4); }
    if (tcp_send_bytes(c, [104u8, 105u8]) != 2) { return fail(5); }
    let got: u8[] = recv(a, 16);
    if (got.len() != 2 || got[0] != 104u8 || got[1] != 105u8) { return fail(6); }
    if (tcp_send_bytes(a, [121u8, 111u8]) != 2) { return fail(7); }
    let back: u8[] = recv(c, 16);
    if (back.len() != 2 || back[0] != 121u8) { return fail(8); }
    tcp_close(a);
    tcp_close(c);
    tcp_close(ln);
    let s: i32 = udp_bind(lo6, 0);
    if (s < 0) { return fail(9); }
    let ps: i32 = tcp_local_port(s);
    if (ps <= 0) { return fail(10); }
    let t: i32 = udp_bind(lo6, 0);
    if (t < 0) { return fail(11); }
    let pt: i32 = tcp_local_port(t);
    if (pt <= 0 || pt == ps) { return fail(12); }
    if (udp_sendto(s, lo6, pt, "ping") != 4) { return fail(13); }
    let buf: u8[] = zeros(16);
    let from: u8[] = zeros(19);
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
        let dl: i32 = tcp_listen_with(zeros(16), 0, 4, false);
        if (dl < 0) { return fail(30); }
        let dc: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], tcp_local_port(dl), false);
        if (dc < 0) { return fail(31); }
        let da: i32 = tcp_accept(dl);
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
        // A :: datagram socket from udp_bind_with takes an IPv4 sender
        // too, reported v4-mapped: ::ffff:127.0.0.1.
        let du: i32 = udp_bind_with(zeros(16), 0, false);
        if (du < 0) { return fail(39); }
        let lo4: u8[] = [127u8, 0u8, 0u8, 1u8];
        let d4: i32 = udp_bind(lo4, 0);
        if (d4 < 0) { return fail(40); }
        if (udp_sendto(d4, lo4, tcp_local_port(du), "ping") != 4) { return fail(41); }
        if (udp_recvfrom(du, buf, from) != 4 || buf[0] != 112u8) { return fail(42); }
        if (from[0] != 6u8 || from[10] != 0u8 || from[11] != 255u8 || from[12] != 255u8 || from[13] != 127u8 || from[16] != 1u8) { return fail(43); }
        if (port_of(from) != tcp_local_port(d4)) { return fail(44); }
        if (tcp_close(du) != 0 || tcp_close(d4) != 0) { return fail(45); }
    }
    print("ok");
    return 42;
}
`
}

func NetV6Probe() string {
	return `import "core/int";
import "std/net";
import "std/errno";

// recv reads once from fd within five seconds: the bytes, none at the end
// of the stream, when nothing came, or when the read failed.
function recv(fd: i32, max: i32): u8[] {
    match (net.read_watched(fd, max, 5000i64, 1)) {
        Ok(net.Chunk(b)) => { return b; },
        _ => {}
    }
    let none: u8[] = [];
    return none;
}

function fail(n: i32): i32 {
    print(int.int_to_string(n));
    return n;
}

function zeros(n: i32): u8[] {
    let b: u8[] = [];
    let i: i32 = 0;
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
function no_v6(e: net.NetError): boolean {
    return e.errno() == errno.EAFNOSUPPORT || e.errno() == errno.ENOTSUP;
}

function main(): i32 {
    let ln: i32 = 0;
    match (net.listen_at(lo6(0), net.listen_options())) {
        Ok(fd) => { ln = fd; },
        Err(e) => {
            if (no_v6(e)) {
                print("nov6");
                return 43;
            }
            return fail(1);
        },
    }
    let port: i32 = 0;
    match (net.local_port(ln)) {
        Ok(p) => { port = p; },
        Err(e) => { return fail(2); },
    }
    let c: i32 = 0;
    match (net.connect(lo6(port))) {
        Ok(fd) => { c = fd; },
        Err(e) => { return fail(3); },
    }
    let a: i32 = tcp_accept(ln);
    if (a < 0) { return fail(4); }
    if (tcp_send_bytes(c, [104u8, 105u8]) != 2) { return fail(5); }
    let got: u8[] = recv(a, 16);
    if (got.len() != 2 || got[0] != 104u8) { return fail(6); }
    tcp_close(a);
    tcp_close(c);
    tcp_close(ln);
    let s: i32 = 0;
    match (net.udp_socket(lo6(0))) {
        Ok(fd) => { s = fd; },
        Err(e) => { return fail(7); },
    }
    let ps: i32 = 0;
    match (net.local_port(s)) {
        Ok(p) => { ps = p; },
        Err(e) => { return fail(8); },
    }
    let t: i32 = 0;
    match (net.udp_socket(lo6(0))) {
        Ok(fd) => { t = fd; },
        Err(e) => { return fail(9); },
    }
    let pt: i32 = 0;
    match (net.local_port(t)) {
        Ok(p) => { pt = p; },
        Err(e) => { return fail(10); },
    }
    match (net.send_to(s, [112u8, 105u8, 110u8, 103u8], lo6(pt))) {
        Ok(n) => { if (n != 4) { return fail(11); } },
        Err(e) => { return fail(12); },
    }
    let buf: u8[] = zeros(16);
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
        let dl: i32 = 0;
        match (net.listen_at(net.socket_addr(net.ipv6_unspecified(), 0), net.listen_options())) {
            Ok(fd) => { dl = fd; },
            Err(e) => { return fail(30); },
        }
        let dc: i32 = 0;
        match (net.connect(net.socket_addr(net.ipv4_loopback(), tcp_local_port(dl)))) {
            Ok(fd) => { dc = fd; },
            Err(e) => { return fail(31); },
        }
        let da: i32 = tcp_accept(dl);
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
