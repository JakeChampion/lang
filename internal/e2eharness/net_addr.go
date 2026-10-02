package e2eharness

import (
	"fmt"
	"strings"
)

// NetAddrProbe exercises std/net's address and error layer: IPv4 and IPv6
// parsing against RFC 4291 text, RFC 5952 canonical rendering, the
// bracketed socket-address form, the range predicates, IPv4-mapped
// unwrapping, and the errno tables on every target. Each check prints its
// index and exits 1 on the first failure; 42 means every check held.
//
// `NetError.Interrupted` and `NetError.Other` share their names with
// `IoError`'s variants and user code has no spelling that names the
// `std/net` ones (#10430), so the probe reaches them through
// `error_from_errno` and matches them unqualified on a typed scrutinee.
func NetAddrProbe() string {
	var src strings.Builder
	src.WriteString(`import "std/net";
import "std/error";
import "std/option";
import "core/cmp";

function v6rt(s: string, want: string): boolean {
    match (net.ipv6_parse(s)) {
        Some(ip) => { return ip.is_v6() && ip.to_string() == want; },
        None => { return false; },
    }
}
function v6bad(s: string): boolean {
    match (net.ipv6_parse(s)) { Some(ip) => { return false; }, None => { return true; } }
}
function v4rt(s: string, want: string): boolean {
    match (net.ipv4_parse(s)) {
        Some(ip) => { return ip.is_v4() && ip.to_string() == want; },
        None => { return false; },
    }
}
function v4bad(s: string): boolean {
    match (net.ipv4_parse(s)) { Some(ip) => { return false; }, None => { return true; } }
}
function sart(s: string, want: string): boolean {
    match (net.socket_addr_parse(s)) {
        Some(sa) => { return sa.to_string() == want; },
        None => { return false; },
    }
}
function sabad(s: string): boolean {
    match (net.socket_addr_parse(s)) { Some(sa) => { return false; }, None => { return true; } }
}
function ip(s: string): net.IpAddr {
    match (net.ip_parse(s)) {
        Some(a) => { return a; },
        None => { return net.ipv4(0, 0, 0, 0); },
    }
}
function show[T: cmp.Display](v: T): string { return v.to_string(); }
function same[T: cmp.Eq](a: T, b: T): boolean { return a.eq(b); }
function describe[E: error.Error](e: E): string { return e.message(); }
function roundtrips(e: net.NetError): boolean {
    let back: net.NetError = net.error_from_errno(0 - e.errno());
    return back.eq(e) && net.error_from_errno(e.errno()).eq(e) && back.errno() == e.errno();
}
function is_other(e: net.NetError, want: i32): boolean {
    match (e) {
        Other(n) => { return n == want; },
        _ => { return false; },
    }
}
function is_interrupted(e: net.NetError): boolean {
    match (e) {
        Interrupted => { return true; },
        _ => { return false; },
    }
}
function on_wasi(): boolean {
    return target_os() == "wasi" || target_os() == "wasi-http";
}
function intr(): net.NetError {
    if (on_wasi()) { return net.error_from_errno(27); }
    return net.error_from_errno(4);
}
function main(): i32 {
    let addr_in_use: net.NetError = AddrInUse;
    let addr_not_available: net.NetError = AddrNotAvailable;
    let refused: net.NetError = ConnectionRefused;
    let reset: net.NetError = ConnectionReset;
    let aborted: net.NetError = ConnectionAborted;
    let timed_out: net.NetError = TimedOut;
    let would_block: net.NetError = WouldBlock;
    let in_progress: net.NetError = InProgress;
    let net_unreach: net.NetError = NetworkUnreachable;
    let host_unreach: net.NetError = HostUnreachable;
    let other_a: net.NetError = net.error_from_errno(12345);
    let other_b: net.NetError = net.error_from_errno(-12345);
    let other_c: net.NetError = net.error_from_errno(12346);
`)
	index := 0
	check := func(cond string) {
		index++
		fmt.Fprintf(&src, "    if (!(%s)) { print(%q); return 1; }\n", cond, fmt.Sprint(index))
	}
	// IPv4.
	check(`net.ipv4(1, 2, 3, 4).to_string() == "1.2.3.4"`)
	check(`net.ipv4(256, 511, -1, 0).to_string() == "0.255.255.0"`)
	check(`v4rt("192.168.0.1", "192.168.0.1")`)
	check(`v4rt("0.0.0.0", "0.0.0.0")`)
	check(`v4rt("255.255.255.255", "255.255.255.255")`)
	for _, bad := range []string{"", "1.2.3", "1.2.3.4.5", "01.2.3.4", "1.2.3.04", "256.1.1.1", "1.2.3.a", "1..2.3", ".1.2.3", "1.2.3.4.", "+1.2.3.4", "1.2.3.4 ", "0x1.2.3.4", "1.2.3.1000"} {
		check(fmt.Sprintf("v4bad(%q)", bad))
	}
	// IPv6 text in, RFC 5952 text out.
	for _, rt := range [][2]string{
		{"::1", "::1"},
		{"::", "::"},
		{"0:0:0:0:0:0:0:0", "::"},
		{"0:0:0:0:0:0:0:1", "::1"},
		{"2001:db8::1", "2001:db8::1"},
		{"2001:0DB8:0000:0000:0000:0000:0000:0001", "2001:db8::1"},
		{"2001:DB8:0:0:1:0:0:1", "2001:db8::1:0:0:1"},
		{"1:0:0:1:0:0:0:1", "1:0:0:1::1"},
		{"1:0:0:0:1:0:0:1", "1::1:0:0:1"},
		{"0:0:1:0:0:1:0:0", "::1:0:0:1:0:0"},
		{"1:2:3:4:5:6:7:8", "1:2:3:4:5:6:7:8"},
		{"1:2:3:4:5:6:7::", "1:2:3:4:5:6:7:0"},
		{"::2:3:4:5:6:7:8", "0:2:3:4:5:6:7:8"},
		{"1::", "1::"},
		{"::ffff:192.168.0.1", "::ffff:192.168.0.1"},
		{"::FFFF:10.0.0.1", "::ffff:10.0.0.1"},
		{"::1.2.3.4", "::102:304"},
		{"64:ff9b::192.0.2.33", "64:ff9b::c000:221"},
		{"1:2:3:4:5:6:1.2.3.4", "1:2:3:4:5:6:102:304"},
		{"fe80::1", "fe80::1"},
		{"FF02::1", "ff02::1"},
		{"abcd:ef01:2345:6789:abcd:ef01:2345:6789", "abcd:ef01:2345:6789:abcd:ef01:2345:6789"},
	} {
		check(fmt.Sprintf("v6rt(%q, %q)", rt[0], rt[1]))
	}
	for _, bad := range []string{"", ":", ":::", "1::2::3", "::1::", "1:2:3:4:5:6:7:8:9", "1:2:3:4:5:6:7", "1:2:3:4:5:6:7:8::", "::1:2:3:4:5:6:7:8", "12345::", "g::", "::1%eth0", "[::1]", "1.2.3.4", "::1.2.3", "1.2.3.4::", "::ffff:1.2.3.256", "1:2:3:4:5:6:7:1.2.3.4", ":1::", "::1:", "1:::2", " ::1", "::1 "} {
		check(fmt.Sprintf("v6bad(%q)", bad))
	}
	check(`ip("1.2.3.4").is_v4()`)
	check(`ip("::1").is_v6()`)
	check(`ip("1.2.3.4").packed_v4().unwrap_or(0) == 1 + (2 << 8) + (3 << 16) + (4 << 24)`)
	check(`ip("127.0.0.1").packed_v4().unwrap_or(0) == 127 + (1 << 24)`)
	check(`ip("::1").packed_v4().is_none()`)
	// Socket addresses.
	for _, rt := range [][2]string{
		{"127.0.0.1:8080", "127.0.0.1:8080"},
		{"0.0.0.0:0", "0.0.0.0:0"},
		{"10.1.2.3:65535", "10.1.2.3:65535"},
		{"[::1]:443", "[::1]:443"},
		{"[2001:DB8::1]:0", "[2001:db8::1]:0"},
		{"[::ffff:1.2.3.4]:80", "[::ffff:1.2.3.4]:80"},
	} {
		check(fmt.Sprintf("sart(%q, %q)", rt[0], rt[1]))
	}
	for _, bad := range []string{"", "127.0.0.1", "127.0.0.1:", "127.0.0.1:65536", "127.0.0.1:1a", "127.0.0.1:-1", "127.0.0.1:123456", "[::1]", "[::1]443", "[::1]:", "::1:80", "[]:80", "[::1", "[::1]:x", ":80", "1.2.3:80", "[1.2.3.4]:80", "[::1]:80:", "[::1]:80 "} {
		check(fmt.Sprintf("sabad(%q)", bad))
	}
	check(`net.socket_addr(net.ipv6_loopback(), 22).to_string() == "[::1]:22"`)
	check(`net.socket_addr(net.ipv4_loopback(), 22).to_string() == "127.0.0.1:22"`)
	check(`net.socket_addr(net.ipv4_unspecified(), 80).eq(net.socket_addr_parse("0.0.0.0:80").unwrap_or(net.socket_addr(net.ipv6_unspecified(), 80)))`)
	check(`!net.socket_addr(net.ipv4_loopback(), 80).eq(net.socket_addr(net.ipv4_loopback(), 81))`)
	check(`!net.socket_addr(net.ipv4_loopback(), 80).eq(net.socket_addr(net.ipv6_loopback(), 80))`)
	// Predicates.
	check(`net.ipv4_unspecified().is_unspecified() && net.ipv6_unspecified().is_unspecified()`)
	check(`!ip("0.0.0.1").is_unspecified() && !ip("::1").is_unspecified()`)
	check(`net.ipv4_loopback().is_loopback() && net.ipv6_loopback().is_loopback()`)
	check(`ip("127.255.255.255").is_loopback() && !ip("128.0.0.1").is_loopback() && !ip("::2").is_loopback()`)
	check(`ip("10.1.2.3").is_private() && ip("172.16.0.1").is_private() && ip("172.31.255.255").is_private() && ip("192.168.1.1").is_private()`)
	check(`!ip("172.32.0.1").is_private() && !ip("172.15.255.255").is_private() && !ip("192.169.0.1").is_private() && !ip("11.0.0.0").is_private()`)
	check(`ip("fc00::1").is_private() && ip("fd12:3456::1").is_private() && !ip("fe00::1").is_private() && !ip("fb00::1").is_private()`)
	check(`ip("169.254.1.1").is_link_local() && !ip("169.253.1.1").is_link_local()`)
	check(`ip("fe80::1").is_link_local() && ip("febf::1").is_link_local() && !ip("fec0::1").is_link_local() && !ip("fe7f::1").is_link_local()`)
	check(`ip("224.0.0.1").is_multicast() && ip("239.255.255.255").is_multicast() && !ip("240.0.0.1").is_multicast() && !ip("223.255.255.255").is_multicast()`)
	check(`ip("ff02::1").is_multicast() && !ip("fe02::1").is_multicast()`)
	check(`ip("::ffff:10.0.0.1").is_v4_mapped() && !ip("::fffe:10.0.0.1").is_v4_mapped() && !ip("10.0.0.1").is_v4_mapped() && !ip("1::ffff:10.0.0.1").is_v4_mapped()`)
	check(`!ip("::ffff:10.0.0.1").is_private() && ip("::ffff:10.0.0.1").to_canonical().is_private()`)
	check(`ip("::ffff:10.0.0.1").to_canonical().to_string() == "10.0.0.1"`)
	check(`ip("::ffff:127.0.0.1").to_canonical().is_loopback()`)
	check(`ip("2001:db8::1").to_canonical().to_string() == "2001:db8::1"`)
	check(`ip("10.0.0.1").to_canonical().to_string() == "10.0.0.1"`)
	// Equality and the trait adoptions.
	check(`net.ipv4(1, 2, 3, 4).eq(ip("1.2.3.4"))`)
	check(`!net.ipv4(1, 2, 3, 4).eq(ip("1.2.3.5"))`)
	check(`!ip("::ffff:1.2.3.4").eq(ip("1.2.3.4"))`)
	check(`ip("::ffff:1.2.3.4").to_canonical().eq(ip("1.2.3.4"))`)
	check(`same(ip("::1"), net.ipv6_loopback())`)
	check(`show(ip("2001:db8::1")) == "2001:db8::1"`)
	check(`show(net.socket_addr(ip("::1"), 8)) == "[::1]:8"`)
	check(`net.ipv6([0 as u8, 1 as u8]).is_none()`)
	check(`net.ipv6(net.ipv6_loopback().bytes()).is_some()`)
	check(`net.ipv6_loopback().bytes().len() == 16 && net.ipv4_loopback().bytes().len() == 4`)
	// Errors: every named variant round-trips through its errno on this
	// target, in both signs.
	for _, v := range []string{"addr_in_use", "addr_not_available", "refused", "reset", "aborted", "timed_out", "would_block", "in_progress", "net_unreach", "host_unreach"} {
		check(fmt.Sprintf("roundtrips(%s)", v))
	}
	check(`is_interrupted(intr()) && roundtrips(intr())`)
	check(`is_other(other_a, 12345) && is_other(other_b, 12345) && !is_other(other_c, 12345)`)
	check(`!is_other(addr_in_use, 12345) && !is_interrupted(addr_in_use)`)
	check(`other_b.errno() == 12345 && net.error_from_errno(0).errno() == 0`)
	check(`other_a.eq(other_b) && !other_a.eq(other_c) && same(other_a, other_b)`)
	check(`!addr_in_use.eq(addr_not_available) && !addr_in_use.eq(other_a) && !other_a.eq(addr_in_use)`)
	check(`addr_in_use.message() == "Address already in use"`)
	check(`refused.to_string() == "Connection refused"`)
	check(`describe(host_unreach) == "No route to host"`)
	check(`intr().message() == "Interrupted system call"`)
	check(`show(other_a) == "Network error (errno 12345)"`)
	check(`same(timed_out, timed_out)`)
	// The table in force is the compile target's.
	check(`target_os() != "linux" || (addr_in_use.errno() == 98 && would_block.errno() == 11 && intr().errno() == 4 && refused.errno() == 111)`)
	check(`target_os() != "darwin" || (addr_in_use.errno() == 48 && would_block.errno() == 35 && intr().errno() == 4 && refused.errno() == 61)`)
	check(`!on_wasi() || (addr_in_use.errno() == 3 && would_block.errno() == 6 && intr().errno() == 27 && refused.errno() == 14)`)
	check(`on_wasi() || is_other(net.error_from_errno(27), 27) || target_os() == "darwin"`)
	src.WriteString("    return 42;\n}\n")
	return src.String()
}
