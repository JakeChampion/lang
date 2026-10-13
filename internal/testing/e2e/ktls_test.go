package e2e

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// kernelRekeys is whether the kernel takes a second key in a direction,
// which TLS 1.3's KeyUpdate needs: Linux 6.14 and later.
func kernelRekeys(t *testing.T) bool {
	t.Helper()
	rel, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(strings.TrimSpace(string(rel)), ".", 3)
	if len(parts) < 2 {
		t.Fatalf("osrelease %q", rel)
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(strings.TrimFunc(parts[1], func(r rune) bool { return r < '0' || r > '9' }))
	return major > 6 || major == 6 && minor >= 14
}

// TestX86_64KtlsAgainstGo offloads both ends of a connection against Go's
// crypto/tls. As the server: a line echoed, 200,000 bytes answered, and
// Go's close_notify read; then a KeyUpdate asking Go to update too, which
// a kernel from 6.14 takes both ways (the next line echoed under the new
// keys) and an older one refuses, closing the connection. As the client:
// a line echoed and 100,000 bytes read through the kernel, and the ticket
// Go sends after the handshake taken from a record the kernel read.
func TestX86_64KtlsAgainstGo(t *testing.T) {
	requireKtls(t)
	dir := t.TempDir()
	ca := newTestCA(t, mustKey(t, "p256"), "Fern kTLS CA")
	key := mustKey(t, "p256")
	leaf := ca.leaf(t, key, []string{"localhost"}, true)
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	caPath := filepath.Join(dir, "ca.pem")
	for path, data := range map[string][]byte{
		certPath: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Certificate[0]}),
		keyPath:  keyPEM(t, key, true),
		caPath:   ca.pem,
	} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	src := strings.NewReplacer("@CERT@", certPath, "@KEY@", keyPath, "@CA@", caPath).Replace(ktlsConnSource)
	bin := buildKtlsProgram(t, "ktls_conn", src)
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	rekeys := kernelRekeys(t)

	t.Run("server", func(t *testing.T) {
		cmd := exec.Command(bin, "2")
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer cmd.Process.Kill()
		lines := bufio.NewScanner(out)
		if !lines.Scan() || !strings.HasPrefix(lines.Text(), "port ") {
			t.Fatalf("no port line: %q", lines.Text())
		}
		addr := "127.0.0.1:" + strings.TrimPrefix(lines.Text(), "port ")
		cfg := &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS13}

		ktlsGoClient(t, addr, cfg, false)
		if !lines.Scan() || lines.Text() != "conn 1: kernel hello big closed" {
			t.Fatalf("first connection: %q", lines.Text())
		}
		ktlsGoClient(t, addr, cfg, true)
		want := "conn 2: kernel hello big rekey after closed"
		if !rekeys {
			want = "conn 2: kernel hello big rekey the kernel's TLS record layer refused new keys (errno 16); Linux before 6.14 takes one key per direction"
		}
		if !lines.Scan() || lines.Text() != want {
			t.Fatalf("second connection: %q, want %q", lines.Text(), want)
		}
		if err := cmd.Wait(); err != nil {
			t.Fatalf("server: %v", err)
		}
	})

	t.Run("client", func(t *testing.T) {
		l, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{leaf}, MinVersion: tls.VersionTLS13})
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		ended := make(chan error, 1)
		go func() {
			c, err := l.Accept()
			if err != nil {
				ended <- err
				return
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(30 * time.Second))
			r := bufio.NewReader(c)
			for {
				s, err := r.ReadString('\n')
				if err == io.EOF {
					ended <- nil
					return
				}
				if err != nil {
					ended <- err
					return
				}
				s = strings.TrimSuffix(s, "\n")
				if n, ok := strings.CutPrefix(s, "big "); ok {
					k, _ := strconv.Atoi(n)
					c.Write(append(ktlsPattern(k), '\n'))
				} else {
					io.WriteString(c, "echo "+s+"\n")
				}
			}
		}()
		port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
		got, err := exec.Command(bin, "client", port).CombinedOutput()
		if err != nil {
			t.Fatalf("client: %v\n%s", err, got)
		}
		if string(got) != "client: kernel echo big tickets 1\n" {
			t.Fatalf("client reported %q", got)
		}
		select {
		case err := <-ended:
			if err != nil {
				t.Fatalf("Go's server read %v, not the client's close_notify", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Go's server never saw the client close")
		}
	})
}

// ktlsGoClient talks to the Fern server: a line, a large answer, and, with
// rekey, a KeyUpdate that asks Go to update too, followed by a line under
// the new keys, or the end of the connection where the kernel refuses them.
func ktlsGoClient(t *testing.T, addr string, cfg *tls.Config, rekey bool) {
	t.Helper()
	c, err := tls.Dial("tcp", addr, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(30 * time.Second))
	r := bufio.NewReader(c)
	say := func(line string) {
		if _, err := io.WriteString(c, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	read := func() string {
		s, err := r.ReadString('\n')
		if err != nil {
			return fmt.Sprintf("error %v", err)
		}
		return strings.TrimSuffix(s, "\n")
	}
	say("hello")
	if got := read(); got != "echo hello" {
		t.Fatalf("hello: %q", got)
	}
	say("big 200000")
	big := make([]byte, 200001)
	if _, err := io.ReadFull(r, big); err != nil || !bytes.Equal(big[:200000], ktlsPattern(200000)) || big[200000] != '\n' {
		t.Fatalf("big: %v", err)
	}
	if !rekey {
		return
	}
	say("rekey")
	got := read()
	if !kernelRekeys(t) {
		if got == "rekeyed" {
			t.Fatal("a kernel before 6.14 took the new keys")
		}
		return
	}
	if got != "rekeyed" {
		t.Fatalf("rekey: %q", got)
	}
	say("after")
	if got := read(); got != "echo after" {
		t.Fatalf("after the rekey: %q", got)
	}
}

func ktlsPattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

const ktlsConnSource = `import "std/i32";
import "std/net";
import "std/string";
import "std/time";
import "std/tls/client";
import "std/tls/identity";
import "std/tls/server";
import "std/tls/verify";

// The server: a listener on a port the host picks, printed first, then
// count connections one at a time, each offloaded to the kernel and
// reported on a line of its own: where its records went, then each line it
// answered, then how it ended. A line is echoed; big N is answered with N
// bytes of a pattern and a newline; rekey updates the keys, asking the
// client to update its own, and is answered rekeyed.
// The client: connects to port, offloads, sends hello and big 100000
// and checks both answers, waits for the tickets, and reports.

function read_text(path: string): Result[string, string] {
  match (read_file(path)) {
    Ok(text) => {
      return Ok(text);
    },
    Err(_) => {
      return Err("cannot read " + path);
    }
  }
  return Err(path);
}

function identity_of(): Result[identity.Identity, string] {
  let cert: string = read_text("@CERT@")?;
  let key: string = read_text("@KEY@")?;
  match (identity.from_pem(cert, key)) {
    Ok(id) => {
      return Ok(id);
    },
    Err(e) => {
      return Err(e.message());
    }
  }
  return Err("identity");
}

function pattern(n: i32): u8[] {
  let out: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    out = out.with(i, (i % 251) as u8);
    i = i + 1;
  }
  return out;
}

function where(offloaded: boolean): string {
  if (offloaded) {
    return "kernel";
  }
  return "user";
}

function answer(c: server.Connection, line: string): Result[server.Connection, string] {
  let reply: u8[] = [];
  let conn: server.Connection = c;
  if (line.starts_with("big ")) {
    let n: i32 = 0;
    match (line.drop(4).parse_int()) {
      Some(v) => {
        n = v;
      },
      None => {}
    }
    let body: u8[] = pattern(n);
    reply = body.append(10 as u8);
  } else if (line == "rekey") {
    match (conn.update_keys(true)) {
      Ok(next) => {
        conn = next;
      },
      Err(e) => {
        return Err(e.message());
      }
    }
    reply = "rekeyed\n".bytes();
  } else {
    reply = ("echo " + line + "\n").bytes();
  }
  match (conn.send(reply)) {
    Ok(sent) => {
      return Ok(sent);
    },
    Err(e) => {
      return Err("send " + e.message());
    }
  }
  return Err("send");
}

function serve_conn(first: server.Connection): string {
  let c: server.Connection = first;
  match (first.offload()) {
    Ok(o) => {
      c = o;
    },
    Err(e) => {
      return "offload " + e.message();
    }
  }
  let report: string = where(c.offloaded());
  let held: string = "";
  while (true) {
    match (c.recv(time.duration_seconds(20 as i64))) {
      Ok(got) => {
        c = got.conn;
        held = held + string_from_bytes_unchecked(got.data);
        let nl: i32 = held.index_of("\n");
        while (nl >= 0) {
          let line: string = held.take(nl);
          held = held.drop(nl + 1);
          let word: string = line;
          if (line.starts_with("big ")) {
            word = "big";
          }
          match (answer(c, line)) {
            Ok(next) => {
              c = next;
            },
            Err(e) => {
              c.close();
              return report + " " + word + " " + e;
            }
          }
          report = report + " " + word;
          nl = held.index_of("\n");
        }
        if (got.ended) {
          c.close();
          return report + " closed";
        }
        if (got.data.len() == 0) {
          c.close();
          return report + " timed out";
        }
      },
      Err(e) => {
        return report + " recv " + e.message();
      }
    }
  }
  return report;
}

function serve(count: i32): i32 {
  let cfg: server.Config = server.config(identity.Identity { chain: [], key: identity.Ed25519Seed([]) });
  match (identity_of()) {
    Ok(id) => {
      cfg = server.config(id);
    },
    Err(e) => {
      print("identity: " + e);
      return 2;
    }
  }
  let l: i32 = 0;
  match (net.listen_at(net.socket_addr(net.ipv4_loopback(), 0), net.listen_options())) {
    Ok(fd) => {
      l = fd;
    },
    Err(e) => {
      print("listen: " + e.message());
      return 3;
    }
  }
  match (net.local_port(l)) {
    Ok(port) => {
      print("port " + port.to_string());
    },
    Err(e) => {
      print("port: " + e.message());
      return 4;
    }
  }
  let n: i32 = 0;
  while (n < count) {
    n = n + 1;
    match (net.accept(l)) {
      Ok(fd) => {
        match (server.accept(fd, cfg, time.duration_seconds(20 as i64))) {
          Ok(c) => {
            print("conn " + n.to_string() + ": " + serve_conn(c));
          },
          Err(e) => {
            print("conn " + n.to_string() + ": refused " + e.message());
          }
        }
      },
      Err(e) => {
        print("conn " + n.to_string() + ": accept " + e.message());
      }
    }
  }
  tcp_close(l);
  return 0;
}

// await_bytes reads until n bytes have come, or the stream ends.
function await_bytes(first: client.Connection, n: i32): Result[(client.Connection, u8[]), string] {
  let c: client.Connection = first;
  let got: u8[] = [];
  while (got.len() < n) {
    match (c.recv(time.duration_seconds(20 as i64))) {
      Ok(r) => {
        c = r.conn;
        for b in r.data {
          got = got.append(b);
        }
        if (r.ended || r.data.len() == 0) {
          return Err("ended after " + got.len().to_string() + " bytes");
        }
      },
      Err(e) => {
        return Err(e.message());
      }
    }
  }
  return Ok((c, got));
}

function talk(first: client.Connection): Result[string, string] {
  let c: client.Connection = first;
  match (c.send("hello\n".bytes())) {
    Ok(s) => {
      c = s;
    },
    Err(e) => {
      return Err("send " + e.message());
    }
  }
  let echo: (client.Connection, u8[]) = await_bytes(c, 11)?;
  c = echo.0;
  if (string_from_bytes_unchecked(echo.1) != "echo hello\n") {
    return Err("echo " + string_from_bytes_unchecked(echo.1));
  }
  match (c.send("big 100000\n".bytes())) {
    Ok(s) => {
      c = s;
    },
    Err(e) => {
      return Err("send " + e.message());
    }
  }
  let big: (client.Connection, u8[]) = await_bytes(c, 100001)?;
  c = big.0;
  let want: u8[] = pattern(100000);
  let i: i32 = 0;
  while (i < 100000) {
    if (big.1[i] != want[i]) {
      return Err("big differs at " + i.to_string());
    }
    i = i + 1;
  }
  let report: string = where(c.offloaded()) + " echo big";
  match (c.recv(time.duration_millis(300 as i64))) {
    Ok(r) => {
      c = r.conn;
    },
    Err(e) => {
      return Err("recv " + e.message());
    }
  }
  report = report + " tickets " + c.tickets.len().to_string();
  c.close();
  return Ok(report);
}

function dial(port: i32): i32 {
  let roots: verify.Roots = verify.roots([]);
  match (verify.roots_from_file("@CA@")) {
    Ok(r) => {
      roots = r;
    },
    Err(e) => {
      print("roots: " + e.message());
      return 5;
    }
  }
  let fd: i32 = tcp_connect(16777343, port);
  if (fd < 0) {
    print("client: connect " + fd.to_string());
    return 0;
  }
  match (client.over(fd, client.config("localhost", roots), time.duration_seconds(20 as i64))) {
    Ok(c) => {
      match (c.offload()) {
        Ok(o) => {
          match (talk(o)) {
            Ok(report) => {
              print("client: " + report);
            },
            Err(e) => {
              print("client: " + e);
            }
          }
        },
        Err(e) => {
          print("client: offload " + e.message());
        }
      }
    },
    Err(e) => {
      print("client: connect " + e.message());
    }
  }
  return 0;
}

function main(): i32 {
  let argv: string[] = args();
  if (argv.len() >= 2 && argv[argv.len() - 2] == "client") {
    match (argv[argv.len() - 1].parse_int()) {
      Some(port) => {
        return dial(port);
      },
      None => {
        return 6;
      }
    }
  }
  let count: i32 = 1;
  if (argv.len() >= 1) {
    match (argv[argv.len() - 1].parse_int()) {
      Some(n) => {
        count = n;
      },
      None => {}
    }
  }
  return serve(count);
}
`
