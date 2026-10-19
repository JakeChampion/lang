package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/envflag"
)

// The kernel's TLS record layer (std/tls/ktls, docs/NET-P4-TLS-PLAN.md):
// x86-64 only, on a kernel with the tls upper-layer protocol loaded. The
// x86-64 e2e lane loads it and sets FERN_REQUIRE_KTLS=1, which turns a
// missing one into a failure; elsewhere the tests skip and say why.

func requireKtls(t *testing.T) {
	t.Helper()
	ulps, _ := os.ReadFile("/proc/sys/net/ipv4/tcp_available_ulp")
	if strings.Contains(string(ulps), "tls") {
		return
	}
	if envflag.On("FERN_REQUIRE_KTLS") {
		t.Fatalf("FERN_REQUIRE_KTLS=1 and the kernel offers no tls upper-layer protocol (available: %q); load it with modprobe tls", strings.TrimSpace(string(ulps)))
	}
	t.Skip("the kernel offers no tls upper-layer protocol: modprobe tls, or FERN_REQUIRE_KTLS=1 to fail here")
}

func buildKtlsProgram(t *testing.T, name, src string) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, name+".fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, name)
	if o, err := exec.Command(buildFernCLI(t), "-target", "x86-64-linux", "-o", out, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	return out
}

// TestX86_64KtlsRecords hands each suite's keys, from
// record.Protection.kernel_info, to the kernel in both directions over a
// loopback pair, from sequence number 5: the record layer opens what the
// kernel sealed, the kernel opens what the record layer sealed, and an
// alert the record layer sealed with padding comes back through the
// kernel's typed read.
func TestX86_64KtlsRecords(t *testing.T) {
	requireKtls(t)
	bin := buildKtlsProgram(t, "ktls_records", ktlsRecordsSource)
	got, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, got)
	}
	want := ""
	for _, s := range []string{"aes128", "aes256", "chacha"} {
		want += s + ": sealed by the kernel / sealed by the record layer / alert 21 2 1\n"
	}
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

const ktlsRecordsSource = `import "std/i32";
import "std/string";
import "std/tls/keyschedule";
import "std/tls/record";

// One suite both ways over a loopback pair: the kernel seals what a
// writes and the record layer opens it at c, then the record layer seals
// at c and the kernel opens it at a, each from sequence number 5, so
// the kernel's key structure (record.Protection.kernel_info) is shown to
// carry the key, the IV, the salt and the sequence number.
function suite(name: string, s: keyschedule.CipherSuite, ln: i32): string {
  let c: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], tcp_local_port(ln), false);
  let a: i32 = tcp_accept(ln);
  if (c < 0 || a < 0) {
    return name + " connect";
  }
  let none: u8[] = [];
  let u: i32 = tcp_ktls(a, 1, 0, none);
  if (u < 0) {
    return name + " ulp " + u.to_string();
  }
  let secret: u8[] = [];
  let i: i32 = 0;
  while (i < s.hash().size()) {
    secret = secret.append((i * 7 + 3) as u8);
    i = i + 1;
  }
  let out: record.Protection = record.Protection { ...record.protection(s, secret), seq: 5 as u64 };
  let tx: i32 = tcp_ktls(a, 2, 0, out.kernel_info());
  if (tx != 0) {
    return name + " tx " + tx.to_string();
  }
  if (tcp_send_bytes(a, [115u8, 101u8, 97u8, 108u8, 101u8, 100u8, 32u8, 98u8, 121u8, 32u8, 116u8, 104u8, 101u8, 32u8, 107u8, 101u8, 114u8, 110u8, 101u8, 108u8]) != 20) {
    return name + " send";
  }
  let rawbuf: u8[] = __alloc_u8(4096);
  let nraw: i32 = tcp_recv_into(c, rawbuf);
  if (nraw <= 0) {
    return name + " recv " + nraw.to_string();
  }
  let raw: u8[] = rawbuf.take(nraw);
  let opened: string = "";
  match (record.deframe(raw)) {
    Ok(d) => {
      if (d.records.len() != 1) {
        return name + " records " + d.records.len().to_string();
      }
      match (out.open(d.records[0])) {
        Ok(o) => {
          opened = string_from_bytes_unchecked(o.content);
        },
        Err(e) => {
          return name + " open " + e.message();
        }
      }
    },
    Err(e) => {
      return name + " deframe " + e.message();
    }
  }
  secret = secret.with(0, 200 as u8);
  let back: record.Protection = record.Protection { ...record.protection(s, secret), seq: 5 as u64 };
  let rx: i32 = tcp_ktls(a, 3, 0, back.kernel_info());
  if (rx != 0) {
    return name + " rx " + rx.to_string();
  }
  match (back.seal(record.ApplicationData, "sealed by the record layer".bytes(), 0)) {
    Ok(sealed) => {
      if (tcp_send_bytes(c, sealed.record) != sealed.record.len()) {
        return name + " send sealed";
      }
      match (sealed.next.seal(record.Alert, [1 as u8, 0 as u8], 3)) {
        Ok(alert) => {
          tcp_send_bytes(c, alert.record);
        },
        Err(e) => {
          return name + " seal alert " + e.message();
        }
      }
    },
    Err(e) => {
      return name + " seal " + e.message();
    }
  }
  let buf: u8[] = __alloc_u8(64);
  let n: i32 = tcp_ktls(a, 5, 0, buf);
  if (n >> 24 != 23) {
    return name + " recv " + n.to_string();
  }
  let kept: u8[] = [];
  for b in buf[0:n & 16777215] {
    kept = kept.append(b);
  }
  let got: string = string_from_bytes_unchecked(kept);
  let m: i32 = tcp_ktls(a, 5, 0, buf);
  tcp_close(c);
  tcp_close(a);
  return name + ": " + opened + " / " + got + " / alert " + (m >> 24).to_string() + " " + (m & 16777215).to_string() + " " + (buf[0] as i32).to_string();
}

function main(): i32 {
  let any: u8[] = [0 as u8, 0 as u8, 0 as u8, 0 as u8];
  let ln: i32 = tcp_listen_with(any, 0, 4, false);
  if (ln < 0) {
    print("listen " + ln.to_string());
    return 1;
  }
  print(suite("aes128", keyschedule.Aes128GcmSha256, ln));
  print(suite("aes256", keyschedule.Aes256GcmSha384, ln));
  print(suite("chacha", keyschedule.ChaCha20Poly1305Sha256, ln));
  return 0;
}
`
