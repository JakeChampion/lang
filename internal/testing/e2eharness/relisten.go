package e2eharness

func RelistenProbe() string {
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
    if (tcp_send_bytes(a, [120u8]) != 1) { return fail(5); }
    tcp_close(a);
    let got: u8[] = recv(c, 16);
    if (got.len() != 1 || got[0] != 120u8) { return fail(6); }
    let eof: u8[] = recv(c, 16);
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
