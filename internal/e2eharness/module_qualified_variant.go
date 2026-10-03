package e2eharness

// ModuleQualifiedVariantProbe constructs and matches an imported module's
// enum variants through the `mod.Variant` spelling (#10430), including the
// two `std/net` variants whose names `IoError` also declares. Exit 42 iff
// every check holds; a failing check prints its number and exits 1.
func ModuleQualifiedVariantProbe() string {
	return `import "std/net";

function kind(e: net.NetError): i32 {
    match (e) {
        net.Other(n) => { return 1000 + n; },
        net.Interrupted => { return 2; },
        net.AddrInUse => { return 3; },
        _ => { return 0; },
    }
}
function nested(o: Option[net.NetError]): i32 {
    match (o) {
        Some(net.Other(n)) => { return n; },
        Some(net.Interrupted) => { return -2; },
        Some(_) => { return -3; },
        None => { return -1; },
    }
}
function family(a: net.IpAddr): i32 {
    match (a) {
        net.V4(b) => { return b.len(); },
        net.V6(b) => { return b.len(); },
    }
}
function main(): i32 {
    let other: net.NetError = net.Other(7);
    let intr: net.NetError = net.Interrupted;
    let in_use: net.NetError = net.AddrInUse;
    if (kind(other) != 1007) { print("1"); return 1; }
    if (kind(intr) != 2) { print("2"); return 1; }
    if (kind(in_use) != 3) { print("3"); return 1; }
    if (kind(net.TimedOut) != 0) { print("4"); return 1; }
    if (nested(Some(other)) != 7) { print("5"); return 1; }
    if (nested(Some(intr)) != -2) { print("6"); return 1; }
    if (nested(Some(in_use)) != -3) { print("7"); return 1; }
    if (nested(None) != -1) { print("8"); return 1; }
    if (other.errno() != 7) { print("9"); return 1; }
    if (!intr.eq(net.error_from_errno(intr.errno()))) { print("10"); return 1; }
    if (intr.message() != "Interrupted system call") { print("11"); return 1; }
    if (family(net.ipv4(1, 2, 3, 4)) != 4) { print("12"); return 1; }
    if (family(net.ipv6_loopback()) != 16) { print("13"); return 1; }
    match ((other, 1)) {
        (net.Other(n), 1) => { if (n != 7) { print("14"); return 1; } },
        _ => { print("15"); return 1; },
    }
    return 42;
}
`
}
