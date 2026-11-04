package e2eharness

func NetReadResetProbe() string {
	return `import "core/int";
import "std/net";
import "std/async";
import "std/clock";
import "std/time";

function fail(n: i32): i32 {
    print(n.to_string());
    return n;
}

function dial(addr: net.SocketAddr): Option[Socket] {
    match (net.connect(addr)) {
        Ok(sock) => { return Some(sock); },
        Err(e) => { return None; }
    }
}

function take(ln: Socket): Option[Socket] {
    match (net.accept(ln)) {
        Ok(sock) => { return Some(sock); },
        Err(e) => { return None; }
    }
}

function main(): i32 {
    let Ok(ln) = net.listen_at(net.socket_addr(net.ipv4_loopback(), 0), net.listen_options()) else {
        return fail(1);
    };
    let port: i32 = 0;
    match (net.local_port(ln)) {
        Ok(p) => { port = p; },
        Err(e) => { return fail(2); }
    }
    let addr: net.SocketAddr = net.socket_addr(net.ipv4_loopback(), port);
    let wait: Duration = time.duration_seconds(5 as i64);

    let Some(c) = dial(addr) else {
        return fail(3);
    };
    let Some(a) = take(ln) else {
        return fail(4);
    };
    let closed: Result[void, net.NetError] = net.close(a);
    match (net.read_deadline(c, 16, wait)) {
        Ok(net.Chunk(b)) => { if (b.len() != 0) { return fail(5); } },
        Ok(_) => { return fail(6); },
        Err(e) => { return fail(7); }
    }
    closed = net.close(c);

    let Some(c2) = dial(addr) else {
        return fail(8);
    };
    let Some(a2) = take(ln) else {
        return fail(9);
    };
    match (net.write(c2, [120u8])) {
        Ok(n) => { if (n != 1) { return fail(10); } },
        Err(e) => { return fail(10); }
    }
    let set: i32[] = [a2.fd, 1];
    match (async.wait_any(set, Some(clock.instant().add(time.duration_millis(5000))))) { Woke(i) => {}, _ => { return fail(11); } }
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
