package e2ecompiler

import (
	"fmt"
	"strings"
	"testing"
)

func dnsNameRoundtripSource() string {
	var src strings.Builder
	src.WriteString(`import "std/dns";
import "std/array";
import "std/string";
import "std/result";
function check(wire: u8[], name: string): void {
 let Ok(decoded) = dns.decode_name(wire, 0) else { assert(false); return; };
 assert(decoded.name == name && decoded.next == wire.len());
 let Ok(encoded) = dns.encode_name(name) else { assert(false); return; };
 assert(encoded == wire);
 let compressed = wire.concat([192u8, 0u8]);
 let Ok(pointer) = dns.decode_name(compressed, wire.len()) else { assert(false); return; };
 assert(pointer.name == name && pointer.next == compressed.len());
}
function main(): i32 {
 check([0u8], "");
`)
	for b := 0; b < 256; b++ {
		name := string(rune(b))
		if b == 46 || b == 92 {
			name = "\\" + name
		} else if b < 33 || b > 126 {
			name = fmt.Sprintf("\\%03d", b)
		}
		fmt.Fprintf(&src, "check([1u8,%du8,0u8],%q);\n", b, name)
	}
	src.WriteString(`
 check([3u8, 99u8, 111u8, 46u8, 0u8], "co\\.");
 let Ok(root) = dns.encode_name(".") else { return 1; };
 assert(root == [0u8]);
 let Ok(trailing) = dns.encode_name("example.com.") else { return 2; };
 let Ok(plain) = dns.encode_name("example.com") else { return 3; };
 assert(trailing == plain);
 let Ok(decimal) = dns.encode_name("a\\046b.\\092") else { return 4; };
 check(decimal, "a\\.b.\\\\");
 let Ok(quoted) = dns.encode_name("\\a") else { return 5; };
 check(quoted, "a");
 let label = "a".repeat(63);
 let maximum = label + "." + label + "." + label + "." + "b".repeat(61);
 let Ok(full) = dns.encode_name(maximum) else { return 6; };
 assert(full.len() == 255);
 check(full, maximum);
 assert(dns.encode_name(maximum + "b").is_err());
 assert(dns.encode_name("a".repeat(64)).is_err());
 assert(dns.encode_name("\\000".repeat(63)).is_ok());
 assert(dns.encode_name("\\000".repeat(64)).is_err());
 for invalid in ["..", ".a", "a..b", "a..", "a\\", "\\0", "\\00", "\\0a0", "\\256", "\\999"] {
  assert(dns.encode_name(invalid).is_err());
 }
 return 0;
}`)
	return src.String()
}

func TestSelfHostDNSNameRoundtrip(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, dnsNameRoundtripSource(), target); code != 0 {
				t.Fatalf("DNS label roundtrip: exit %d\n%s", code, stderr)
			}
		})
	}
}
