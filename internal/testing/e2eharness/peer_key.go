package e2eharness

func PeerKeyProbe() string {
	return `import "core/int";
import "std/net";

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
    let c: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], port, false);
    if (c < 0) { return fail(3); }
    let a: i32 = tcp_accept(ln);
    if (a < 0) { return fail(4); }
    if (tcp_socket_ctl(a, 7, 0) != 16777343) { return fail(5); }
    if (tcp_socket_ctl(c, 7, 0) != 16777343) { return fail(6); }
    if (tcp_socket_ctl(ln, 7, 0) != 0) { return fail(7); }
    match (net.peer_key(a)) {
        Some(k) => { if (k != 16777343) { return fail(8); } },
        None => { return fail(9); }
    }
    match (net.peer_key(ln)) {
        Some(k) => { return fail(10); },
        None => {}
    }
    tcp_close(c);
    tcp_close(a);
    tcp_close(ln);
    print("ok");
    return 42;
}
`
}
