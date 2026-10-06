package e2eharness

// UnixSocketProbe exercises the Unix-domain sockets (#9853): a listener at a
// fresh path under /tmp, a connect to it, the accepted side used both ways
// through the tcp verbs, the refusal of a second listener while the path is
// held, the -ENOENT a connect reports once the socket file is removed, and
// the -ENAMETOOLONG a path longer than the address holds draws before any
// socket exists. Native only: the unix capability refuses the builtins on
// wasm at check time. Exit 42 and "ok" on stdout iff every check holds,
// else the number of the first failing check.
func UnixSocketProbe() string {
	return `import "core/int";
import "std/string";

function fail(n: i32): i32 {
    print(int.int_to_string(n));
    return n;
}

function enametoolong(): i32 {
    if (target_os() == "darwin") { return 63; }
    return 36;
}

function main(): i32 {
    let path: string = "/tmp/fern-unix-" + int.int_to_string((now_unix_ms() % (1000000 as i64)) as i32);
    let ln: i32 = unix_listen(path, 4);
    if (ln < 0) { return fail(1); }
    let c: i32 = unix_connect(path);
    if (c < 0) { return fail(2); }
    let a: i32 = tcp_accept(ln);
    if (a < 0) { return fail(3); }
    if (tcp_send(c, "hi") != 2) { return fail(4); }
    let got: u8[] = tcp_recv(a, 16);
    if (got.len() != 2 || got[0] != 104u8 || got[1] != 105u8) { return fail(5); }
    if (tcp_send(a, "yo") != 2) { return fail(6); }
    let back: u8[] = tcp_recv(c, 16);
    if (back.len() != 2 || back[0] != 121u8) { return fail(7); }
    if (unix_listen(path, 4) >= 0) { return fail(8); }
    tcp_close(a);
    tcp_close(c);
    tcp_close(ln);
    match (remove_file(path)) {
        Ok(_) => {},
        Err(e) => { return fail(9); },
    }
    if (unix_connect(path) != 0 - 2) { return fail(10); }
    let long: string = "/tmp/" + "a".repeat(120);
    if (unix_listen(long, 4) != 0 - enametoolong()) { return fail(11); }
    if (unix_connect(long) != 0 - enametoolong()) { return fail(12); }
    print("ok");
    return 42;
}
`
}

// NetUnixProbe is UnixSocketProbe through std/net: listen_unix, connect_unix
// and accept answering Results, the AddrInUse a second listener reports, the
// Other(ENOENT) of a connect to a removed path and the Other(ENAMETOOLONG)
// of a path too long for the address. Same verdict channel.
func NetUnixProbe() string {
	return `import "core/int";
import "std/net";
import "std/string";

function fail(n: i32): i32 {
    print(int.int_to_string(n));
    return n;
}

function enametoolong(): i32 {
    if (target_os() == "darwin") { return 63; }
    return 36;
}

function errno_of(r: Result[i32, net.NetError]): i32 {
    match (r) {
        Ok(fd) => { return 0; },
        Err(e) => { return e.errno(); },
    }
}

function main(): i32 {
    let path: string = "/tmp/fern-net-unix-" + int.int_to_string((now_unix_ms() % (1000000 as i64)) as i32);
    let ln: i32 = 0;
    match (net.listen_unix(path, 4)) {
        Ok(fd) => { ln = fd; },
        Err(e) => { return fail(1); },
    }
    let c: i32 = 0;
    match (net.connect_unix(path)) {
        Ok(fd) => { c = fd; },
        Err(e) => { return fail(2); },
    }
    let a: i32 = 0;
    match (net.accept(ln)) {
        Ok(fd) => { a = fd; },
        Err(e) => { return fail(3); },
    }
    if (tcp_send(c, "hi") != 2) { return fail(4); }
    let got: u8[] = tcp_recv(a, 16);
    if (got.len() != 2 || got[0] != 104u8) { return fail(5); }
    match (net.listen_unix(path, 4)) {
        Ok(fd) => { return fail(6); },
        Err(e) => { if (!e.eq(net.AddrInUse)) { return fail(7); } },
    }
    tcp_close(a);
    tcp_close(c);
    tcp_close(ln);
    match (remove_file(path)) {
        Ok(_) => {},
        Err(e) => { return fail(8); },
    }
    if (errno_of(net.connect_unix(path)) != 2) { return fail(9); }
    let long: string = "/tmp/" + "a".repeat(120);
    if (errno_of(net.listen_unix(long, 4)) != enametoolong()) { return fail(10); }
    print("ok");
    return 42;
}
`
}
