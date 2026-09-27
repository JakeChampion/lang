package e2eharness

// UdpSocketProbe exercises the datagram sockets (#9853) over loopback:
// two sockets bound to ports the host picks, a datagram from one to the
// other whose bytes and sender the receiver reads back, a reply through a
// connected socket, a short receive buffer that truncates, the refusal a
// second bind of a held port draws, the would-block a non-blocking receive
// on an empty socket reports (on wasm, the -ENOTSUP the control answers on
// a datagram socket too), and the close of both. Exit 42 and "ok" on
// stdout iff every check holds, else the number of the first failing
// check; a preview-2 wasm host reports only 0 or 1, so the wasm legs read
// stdout.
func UdpSocketProbe() string {
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
    return ((from[4] as i32) << 8) | (from[5] as i32);
}

function main(): i32 {
    // 127.0.0.1 packed in network order: 127 | 1 << 24.
    var lo: i32 = 16777343;
    var a: i32 = udp_bind(lo, 0);
    if (a < 0) { return fail(1); }
    var pa: i32 = tcp_local_port(a);
    if (pa <= 0) { return fail(2); }
    var b: i32 = udp_bind(lo, 0);
    if (b < 0) { return fail(3); }
    var pb: i32 = tcp_local_port(b);
    if (pb <= 0 || pb == pa) { return fail(4); }
    if (udp_sendto(a, lo, pb, "ping") != 4) { return fail(5); }
    var buf: u8[] = zeros(16);
    var from: u8[] = zeros(6);
    if (udp_recvfrom(b, buf, from) != 4) { return fail(6); }
    if (buf[0] != 112u8 || buf[1] != 105u8 || buf[2] != 110u8 || buf[3] != 103u8) { return fail(7); }
    if (from[0] != 127u8 || from[1] != 0u8 || from[2] != 0u8 || from[3] != 1u8) { return fail(8); }
    if (port_of(from) != pa) { return fail(9); }
    // b fixes a as its peer and answers without naming it.
    if (udp_connect(b, lo, pa) != 0) { return fail(10); }
    if (udp_sendto(b, 0, 0, "pong!") != 5) { return fail(11); }
    if (udp_recvfrom(a, buf, from) != 5 || buf[4] != 33u8) { return fail(12); }
    if (port_of(from) != pb) { return fail(13); }
    // A receive buffer shorter than the datagram keeps its first bytes.
    if (udp_sendto(a, lo, pb, "abcdef") != 6) { return fail(14); }
    var small: u8[] = zeros(4);
    if (udp_recvfrom(b, small, from) != 4 || small[3] != 100u8) { return fail(15); }
    // The port a holds is refused to a second socket.
    if (udp_bind(lo, pa) >= 0) { return fail(16); }
    if (target_os() == "wasi") {
        // wasi:sockets has no blocking mode to switch off, on any socket.
        if (tcp_socket_ctl(a, 3, 1) != 0 - 58) { return fail(17); }
    } else {
        if (tcp_socket_ctl(a, 3, 1) != 0) { return fail(17); }
        if (udp_recvfrom(a, buf, from) >= 0) { return fail(18); }
    }
    if (tcp_close(a) != 0 || tcp_close(b) != 0) { return fail(19); }
    print("ok");
    return 42;
}
`
}

// NetUdpProbe is UdpSocketProbe through std/net's typed faces:
// `udp_socket`, `local_port`, `send_to`, `recv_from`, `set_peer`, `send`,
// `recv` and `close`, each answering a `Result` with the errno mapped, the
// `AddrInUse` a second bind of a held port reports, the refusal of an IPv6
// address, and the `WouldBlock` of a non-blocking receive. Same verdict
// channel: exit 42 and "ok", or the first failing check.
func NetUdpProbe() string {
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

function loopback(port: i32): net.SocketAddr {
    return net.socket_addr(net.ipv4_loopback(), port);
}

function open_socket(n: i32): i32 {
    match (net.udp_socket(loopback(0))) {
        Ok(fd) => { return fd; },
        Err(e) => { return fail(n); },
    }
}

function port_or(sock: i32, n: i32): i32 {
    match (net.local_port(sock)) {
        Ok(p) => { return p; },
        Err(e) => { return fail(n); },
    }
}

function sent(r: Result[i32, net.NetError]): i32 {
    match (r) {
        Ok(n) => { return n; },
        Err(e) => { return 0 - 1; },
    }
}

function main(): i32 {
    var a: i32 = open_socket(1);
    var pa: i32 = port_or(a, 2);
    var b: i32 = open_socket(3);
    var pb: i32 = port_or(b, 4);
    if (a < 0 || pa <= 0 || b < 0 || pb <= 0) { return 5; }
    if (sent(net.send_to(a, "ping", loopback(pb))) != 4) { return fail(6); }
    var buf: u8[] = zeros(16);
    match (net.recv_from(b, buf)) {
        Ok(got) => {
            if (got.0 != 4 || buf[0] != 112u8 || buf[3] != 103u8) { return fail(7); }
            if (!got.1.eq(loopback(pa))) { return fail(8); }
        },
        Err(e) => { return fail(9); },
    }
    match (net.set_peer(b, loopback(pa))) {
        Ok(u) => {},
        Err(e) => { return fail(10); },
    }
    if (sent(net.send(b, "pong!")) != 5) { return fail(11); }
    match (net.recv(a, buf)) {
        Ok(n) => { if (n != 5 || buf[4] != 33u8) { return fail(12); } },
        Err(e) => { return fail(13); },
    }
    match (net.udp_socket(loopback(pa))) {
        Ok(fd) => { return fail(14); },
        Err(e) => { if (!e.eq(net.AddrInUse)) { return fail(15); } },
    }
    match (net.udp_socket(net.socket_addr(net.ipv6_loopback(), 0))) {
        Ok(fd) => { return fail(16); },
        Err(e) => { if (e.errno() != net.address_family_errno()) { return fail(17); } },
    }
    if (target_os() == "wasi") {
        match (net.set_nonblocking(a, true)) {
            Ok(u) => { return fail(18); },
            Err(e) => { if (e.errno() != 58) { return fail(19); } },
        }
    } else {
        match (net.set_nonblocking(a, true)) {
            Ok(u) => {},
            Err(e) => { return fail(18); },
        }
        match (net.recv(a, buf)) {
            Ok(n) => { return fail(19); },
            Err(e) => { if (!e.eq(net.WouldBlock)) { return fail(20); } },
        }
    }
    match (net.close(a)) {
        Ok(u) => {},
        Err(e) => { return fail(21); },
    }
    match (net.close(b)) {
        Ok(u) => {},
        Err(e) => { return fail(22); },
    }
    print("ok");
    return 42;
}
`
}

// ConnectProbe exercises `tcp_connect_with` and control op 5 (#9853): a
// non-blocking connect to a listener on a host-picked port, settled by
// asking op 5 until it stops answering -EINPROGRESS, then accepted and
// used both ways; a completed connect answering 0 on every later ask; and
// the connect to the port once nothing listens, refused either as the
// connect starts or once it settles. The interpreter connects before
// answering, so both legs accept either shape. Exit 42 and "ok" on stdout
// iff every check holds, else the number of the first failing check.
func ConnectProbe() string {
	return `import "core/int";

function fail(n: i32): i32 {
    print(int.int_to_string(n));
    return n;
}

function einprogress(): i32 {
    if (target_os() == "darwin") { return 36; }
    if (target_os() == "wasi") { return 26; }
    return 115;
}

function econnrefused(): i32 {
    if (target_os() == "darwin") { return 61; }
    if (target_os() == "wasi") { return 14; }
    return 111;
}

// The connect's result once it is no longer under way.
function settle(c: i32): i32 {
    var tries: i32 = 0;
    while (tries < 5000) {
        var r: i32 = tcp_socket_ctl(c, 5, 0);
        if (r != 0 - einprogress()) { return r; }
        sleep_ms(1);
        tries = tries + 1;
    }
    return 0 - einprogress();
}

function main(): i32 {
    // 127.0.0.1 packed in network order: 127 | 1 << 24.
    var lo: i32 = 16777343;
    var ln: i32 = tcp_listen_with(0, 4, false);
    if (ln < 0) { return fail(1); }
    var port: i32 = tcp_local_port(ln);
    if (port <= 0) { return fail(2); }
    var c: i32 = tcp_connect_with(lo, port, true);
    if (c < 0) { return fail(3); }
    if (settle(c) != 0) { return fail(4); }
    var a: i32 = tcp_accept(ln);
    if (a < 0) { return fail(5); }
    if (tcp_socket_ctl(c, 5, 0) != 0) { return fail(6); }
    // Blocking again for the exchange: loopback delivery is not synchronous
    // on every kernel, so a non-blocking read of the reply could find
    // nothing queued yet. wasm has no blocking mode to switch back to, and
    // its tcp_recv waits regardless.
    if (target_os() != "wasi" && tcp_socket_ctl(c, 3, 0) != 0) { return fail(7); }
    if (tcp_send(c, "hi") != 2) { return fail(8); }
    var got: u8[] = tcp_recv(a, 16);
    if (got.len() != 2 || got[0] != 104u8 || got[1] != 105u8) { return fail(9); }
    if (tcp_send(a, "yo") != 2) { return fail(10); }
    var back: u8[] = tcp_recv(c, 16);
    if (back.len() != 2 || back[0] != 121u8) { return fail(11); }
    tcp_close(a);
    tcp_close(c);
    tcp_close(ln);
    var c2: i32 = tcp_connect_with(lo, port, true);
    if (c2 >= 0) {
        var r: i32 = settle(c2);
        tcp_close(c2);
        if (r != 0 - econnrefused()) { return fail(12); }
    } else if (c2 != 0 - econnrefused()) {
        return fail(13);
    }
    print("ok");
    return 42;
}
`
}

// NetConnectProbe is ConnectProbe through std/net: `connect_start`,
// `connect_result` (`InProgress` while under way, then `Ok`), the accepted
// side used both ways, the `ConnectionRefused` a settled or a starting
// connect to a closed port reports, and the same from a blocking
// `connect`. Same verdict channel.
func NetConnectProbe() string {
	return `import "core/int";
import "std/net";

function fail(n: i32): i32 {
    print(int.int_to_string(n));
    return n;
}

function loopback(port: i32): net.SocketAddr {
    return net.socket_addr(net.ipv4_loopback(), port);
}

// The connect's result once it is no longer under way: 0, or its errno.
function settle(c: i32): i32 {
    var tries: i32 = 0;
    while (tries < 5000) {
        match (net.connect_result(c)) {
            Ok(u) => { return 0; },
            Err(e) => { if (!e.eq(net.InProgress)) { return e.errno(); } },
        }
        sleep_ms(1);
        tries = tries + 1;
    }
    return 0 - 1;
}

function refused(errno: i32): boolean {
    return net.error_from_errno(errno).eq(net.ConnectionRefused);
}

function main(): i32 {
    var ln: i32 = 0;
    match (net.listen_with(0, net.listen_options())) {
        Ok(fd) => { ln = fd; },
        Err(e) => { return fail(1); },
    }
    var port: i32 = 0;
    match (net.local_port(ln)) {
        Ok(p) => { port = p; },
        Err(e) => { return fail(2); },
    }
    var c: i32 = 0;
    match (net.connect_start(loopback(port))) {
        Ok(fd) => { c = fd; },
        Err(e) => { return fail(3); },
    }
    if (settle(c) != 0) { return fail(4); }
    var a: i32 = tcp_accept(ln);
    if (a < 0) { return fail(5); }
    if (tcp_send(c, "hi") != 2) { return fail(6); }
    var got: u8[] = tcp_recv(a, 16);
    if (got.len() != 2 || got[0] != 104u8) { return fail(7); }
    tcp_close(a);
    tcp_close(c);
    tcp_close(ln);
    match (net.connect_start(loopback(port))) {
        Ok(fd) => {
            var r: i32 = settle(fd);
            tcp_close(fd);
            if (!refused(r)) { return fail(8); }
        },
        Err(e) => { if (!e.eq(net.ConnectionRefused)) { return fail(9); } },
    }
    match (net.connect(loopback(port))) {
        Ok(fd) => { return fail(10); },
        Err(e) => { if (!e.eq(net.ConnectionRefused)) { return fail(11); } },
    }
    print("ok");
    return 42;
}
`
}
