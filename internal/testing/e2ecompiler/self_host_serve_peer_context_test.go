package e2ecompiler

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestSelfHostServePeerContexts(t *testing.T) {
	cli := buildSelfHostCLI(t)
	t.Setenv("FERN_SANITIZE", "1")
	t.Setenv("FERN_LEAKCHECK", "1")
	for _, stateful := range []bool{false, true} {
		for _, proxy := range []bool{false, true} {
			t.Run(fmt.Sprintf("stateful=%v/proxy=%v", stateful, proxy), func(t *testing.T) {
				entry := "serve.run(0, opts, handle)"
				if stateful {
					entry = "serve.run_with(0, opts, 0, state_handle)"
				}
				source := fmt.Sprintf(`import "std/http";
import "std/platform";
import "std/serve";
import "std/i32";
import "std/time";

function handle(req: HttpRequest, plat: platform.Host): HttpResponse {
 plat.sleep(time.duration_millis(25i64));
 match (plat.peer) {
  Some(addr) => { return http.ok(addr.port.to_string()); },
  None => { return http.ok("no peer"); }
 }
}
function state_handle(n: i32, req: HttpRequest, plat: platform.Host): (i32, HttpResponse) {
 return (n + 1, handle(req, plat));
}
function main(): i32 {
 let opts = serve.Config { ...serve.config(), proxy_protocol: %v };
 return serve.exit_code(%s);
}
`, proxy, entry)
				addr := startHeldConnectionsServer(t, cli.runner, cli.bin, cli.stdlib, func() string { return source })
				dial := func(port int) (net.Conn, string) {
					c, err := net.DialTimeout("tcp", addr, 5*time.Second)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { c.Close() })
					if proxy {
						if _, err := fmt.Fprintf(c, "PROXY TCP4 203.0.113.7 192.0.2.9 %d 80\r\n", port); err != nil {
							t.Fatal(err)
						}
					} else {
						port = c.LocalAddr().(*net.TCPAddr).Port
					}
					return c, strconv.Itoa(port)
				}
				send := func(c net.Conn, close bool) {
					if err := c.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
						t.Fatal(err)
					}
					header := ""
					if close {
						header = "Connection: close\r\n"
					}
					if _, err := fmt.Fprintf(c, "GET /peer HTTP/1.1\r\nHost: x\r\n%s\r\n", header); err != nil {
						t.Fatal(err)
					}
				}
				receive := func(c net.Conn, want string) {
					resp, err := http.ReadResponse(bufio.NewReader(c), nil)
					if err != nil {
						t.Fatal(err)
					}
					body, err := io.ReadAll(resp.Body)
					resp.Body.Close()
					if err != nil || resp.StatusCode != 200 || string(body) != want {
						t.Fatalf("peer response: status=%d body=%q err=%v, want 200 %q", resp.StatusCode, body, err, want)
					}
				}
				request := func(c net.Conn, want string, close bool) {
					send(c, close)
					receive(c, want)
				}
				first, firstPeer := dial(21001)
				request(first, firstPeer, false)
				second, secondPeer := dial(21002)
				send(first, false)
				send(second, false)
				receive(first, firstPeer)
				receive(second, secondPeer)
				request(first, firstPeer, true)
				request(second, secondPeer, false)
				third, thirdPeer := dial(21003)
				request(third, thirdPeer, false)
				request(second, secondPeer, false)
				request(third, thirdPeer, true)
			})
		}
	}
}
