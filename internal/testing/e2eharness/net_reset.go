package e2eharness

// NetReadResetProbe checks that std/net's reads tell a peer that reset the
// connection from one that ended it: a clean close reads as
// `Ok(Chunk([]))`, the end of the stream, and a reset reads as an error.
// The accepted side resets by closing with the client's byte unread, which
// is a reset on every host here. Exit 42 and "ok" on stdout iff every check
// holds, else the number of the first failing check.
func NetReadResetProbe() string {
	return `import "core/int";
import "std/net";
import "std/async";
import "std/time";

function fail(n: i32): i32 {
    print(int.int_to_string(n));
    return n;
}

function dial(addr: net.SocketAddr): i32 {
    match (net.connect(addr)) {
        Ok(fd) => { return fd; },
        Err(e) => { return 0 - 1; }
    }
}

function take(ln: i32): i32 {
    match (net.accept(ln)) {
        Ok(fd) => { return fd; },
        Err(e) => { return 0 - 1; }
    }
}

function main(): i32 {
    let ln: i32 = 0;
    match (net.listen_at(net.socket_addr(net.ipv4_loopback(), 0), net.listen_options())) {
        Ok(fd) => { ln = fd; },
        Err(e) => { return fail(1); }
    }
    let port: i32 = 0;
    match (net.local_port(ln)) {
        Ok(p) => { port = p; },
        Err(e) => { return fail(2); }
    }
    let addr: net.SocketAddr = net.socket_addr(net.ipv4_loopback(), port);
    let wait: Duration = time.duration_seconds(5 as i64);

    let c: i32 = dial(addr);
    if (c < 0) { return fail(3); }
    let a: i32 = take(ln);
    if (a < 0) { return fail(4); }
    let closed: Result[void, net.NetError] = net.close(a);
    match (net.read_deadline(c, 16, wait)) {
        Ok(net.Chunk(b)) => { if (b.len() != 0) { return fail(5); } },
        Ok(_) => { return fail(6); },
        Err(e) => { return fail(7); }
    }
    closed = net.close(c);

    let c2: i32 = dial(addr);
    if (c2 < 0) { return fail(8); }
    let a2: i32 = take(ln);
    if (a2 < 0) { return fail(9); }
    match (net.write(c2, [120u8])) {
        Ok(n) => { if (n != 1) { return fail(10); } },
        Err(e) => { return fail(10); }
    }
    let set: i32[] = [a2, 1];
    if (async.wait_any(set, 5000) != 0) { return fail(11); }
    closed = net.close(a2);
    match (net.read_deadline(c2, 16, wait)) {
        Ok(net.Chunk(b)) => { return fail(12); },
        Ok(_) => { return fail(13); },
        Err(e) => {
            // wasi:sockets reports a failed stream read with no errno.
            if (target_os() != "wasi" && !e.eq(net.ConnectionReset)) { return fail(14); }
        }
    }
    closed = net.close(c2);
    closed = net.close(ln);
    print("ok");
    return 42;
}
`
}
