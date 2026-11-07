package e2eharness

// AcceptWithCloexecProbe reads the open flags of a socket tcp_accept_with
// returned from /proc/self/fdinfo: close-on-exec on both forms, non-blocking
// only when asked. The file is Linux's, so the probe answers ok elsewhere,
// and it runs on the compiled targets only: the interpreter's socket numbers
// are not descriptors.
func AcceptWithCloexecProbe() string {
	return `import "core/int";
import "std/string";

function fail(n: i32): i32 {
    print(n.to_string());
    return n;
}

// flags_of reads the octal "flags:" field of a /proc/self/fdinfo text.
function flags_of(info: string): i32 {
    let Some(at) = info.index_of("flags:") else {
        return 0 - 1;
    };
    let b: u8[] = info.bytes();
    let i: i32 = at + 6;
    while (i < b.len() && (b[i] == 9u8 || b[i] == 32u8)) {
        i = i + 1;
    }
    let v: i32 = 0;
    while (i < b.len() && b[i] >= 48u8 && b[i] <= 55u8) {
        v = v * 8 + (b[i] as i32 - 48);
        i = i + 1;
    }
    return v;
}

function flags_of_fd(fd: i32): i32 {
    let Ok(info) = read_file("/proc/self/fdinfo/" + fd.to_string()) else {
        return 0 - 1;
    };
    return flags_of(info);
}

function main(): i32 {
    if (target_os() != "linux") {
        print("ok");
        return 42;
    }
    let any: u8[] = [0u8, 0u8, 0u8, 0u8];
    let ln: Socket = tcp_listen_with(any, 0, 4, false);
    if (ln.fd < 0) { return fail(1); }
    let port: i32 = tcp_local_port(ln);
    let c1: Socket = tcp_connect_with([127u8, 0u8, 0u8, 1u8], port, false);
    if (c1.fd < 0) { return fail(2); }
    let a1: Socket = tcp_accept_with(ln, false);
    if (a1.fd < 0) { return fail(3); }
    // O_CLOEXEC is 02000000 and O_NONBLOCK is 04000.
    let f1: i32 = flags_of_fd(a1.fd);
    if (f1 < 0) { return fail(4); }
    if ((f1 & 524288) == 0) { return fail(5); }
    if ((f1 & 2048) != 0) { return fail(6); }
    let c2: Socket = tcp_connect_with([127u8, 0u8, 0u8, 1u8], port, false);
    if (c2.fd < 0) { return fail(7); }
    let a2: Socket = tcp_accept_with(ln, true);
    if (a2.fd < 0) { return fail(8); }
    let f2: i32 = flags_of_fd(a2.fd);
    if (f2 < 0) { return fail(9); }
    if ((f2 & 524288) == 0) { return fail(10); }
    if ((f2 & 2048) == 0) { return fail(11); }
    // A bad listener is refused with the errno, as tcp_accept answers it.
    let bad: Socket = tcp_listen_with([1u8], 0, 4, false);
    if (tcp_accept_with(bad, true).fd >= 0) { return fail(12); }
    tcp_close(c1);
    tcp_close(a1);
    tcp_close(c2);
    tcp_close(a2);
    tcp_close(ln);
    print("ok");
    return 42;
}
`
}
