package e2eharness

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func WebSocketServerSource() string {
	return `import "std/http";
import "std/serve";
import "std/platform";
import "std/http/websocket";
function echo(c: websocket.Conn, m: websocket.Message): void {
    match (m) {
        Text(s) => {
            if (s == "close") { c.close(1001, "handler"); return; }
            let r: websocket.SendResult = c.send_text(s);
        },
        Binary(b) => { let r: websocket.SendResult = c.send_binary(b); }
    }
}
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/ws") {
        let opts: websocket.Options = websocket.Options { ...websocket.options(), max_message: 204800 };
        return http.respond(websocket.accept(req, opts, websocket.handlers(echo)));
    }
    return http.ok("ok");
}
function main(): i32 {
    let opts: serve.Config = serve.Config { ...serve.config(), workers: 1 };
    return serve.exit_code(serve.supervise(0, opts, handle));
}
`
}

type wsClient struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func wsDial(t *testing.T, addr string) *wsClient {
	t.Helper()
	c := dialRaw(t, addr)
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	req := "GET /ws HTTP/1.1\r\nHost: h\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := io.WriteString(c, req); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(c)
	status, err := r.ReadString('\n')
	if err != nil || !strings.HasPrefix(status, "HTTP/1.1 101 ") {
		t.Fatalf("handshake: status %q, err %v", status, err)
	}
	headers := map[string]string{}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("handshake headers: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if i := strings.Index(line, ":"); i > 0 {
			headers[strings.ToLower(line[:i])] = strings.TrimSpace(line[i+1:])
		}
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	want := base64.StdEncoding.EncodeToString(sum[:])
	if headers["sec-websocket-accept"] != want || !strings.EqualFold(headers["upgrade"], "websocket") || !strings.EqualFold(headers["connection"], "upgrade") {
		t.Fatalf("handshake headers: %v (want accept %s)", headers, want)
	}
	return &wsClient{t: t, conn: c, r: r}
}

func (c *wsClient) send(fin bool, opcode byte, payload []byte) {
	c.t.Helper()
	var head []byte
	b0 := opcode
	if fin {
		b0 |= 0x80
	}
	head = append(head, b0)
	n := len(payload)
	switch {
	case n < 126:
		head = append(head, 0x80|byte(n))
	case n < 65536:
		head = append(head, 0x80|126, byte(n>>8), byte(n))
	default:
		head = append(head, 0x80|127)
		head = binary.BigEndian.AppendUint64(head, uint64(n))
	}
	mask := []byte{0x12, 0x34, 0x56, 0x78}
	head = append(head, mask...)
	masked := make([]byte, n)
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	if _, err := c.conn.Write(append(head, masked...)); err != nil {
		c.t.Fatal(err)
	}
}

func (c *wsClient) recv() (byte, []byte, error) {
	var head [2]byte
	if _, err := io.ReadFull(c.r, head[:]); err != nil {
		return 0, nil, err
	}
	if head[1]&0x80 != 0 {
		return 0, nil, fmt.Errorf("the server masked a frame")
	}
	n := int(head[1] & 0x7f)
	switch n {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.r, ext[:]); err != nil {
			return 0, nil, err
		}
		n = int(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.r, ext[:]); err != nil {
			return 0, nil, err
		}
		n = int(binary.BigEndian.Uint64(ext[:]))
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(c.r, payload); err != nil {
		return 0, nil, err
	}
	return head[0] & 0x0f, payload, nil
}

func (c *wsClient) expect(opcode byte, payload []byte, what string) {
	c.t.Helper()
	got, data, err := c.recv()
	if err != nil {
		c.t.Fatalf("%s: %v", what, err)
	}
	if got != opcode || !bytes.Equal(data, payload) {
		c.t.Fatalf("%s: got opcode %d payload %q (%d bytes), want opcode %d payload of %d bytes", what, got, trimmed(data), len(data), opcode, len(payload))
	}
}

func trimmed(b []byte) string {
	if len(b) > 40 {
		return string(b[:40]) + "…"
	}
	return string(b)
}

func CheckWebSocketSequential(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr)

	c := wsDial(t, addr)
	c.send(true, 1, []byte("hello"))
	c.expect(1, []byte("hello"), "text echo")
	c.send(true, 2, []byte{1, 2, 3, 0xff})
	c.expect(2, []byte{1, 2, 3, 0xff}, "binary echo")
	c.send(false, 1, []byte("one "))
	c.send(true, 9, []byte("ping"))
	c.expect(10, []byte("ping"), "pong inside a fragmented message")
	c.send(false, 0, []byte("two "))
	c.send(true, 0, []byte("three"))
	c.expect(1, []byte("one two three"), "fragments joined")
	big := bytes.Repeat([]byte("z"), 150*1024)
	c.send(true, 2, big)
	c.expect(2, big, "a 150 KiB message")
	c.send(true, 8, []byte{0x03, 0xe8, 'b', 'y', 'e'})
	c.expect(8, []byte{0x03, 0xe8}, "the close echoed")
	if _, _, err := c.recv(); err == nil {
		t.Fatalf("the server kept the connection open after the close handshake")
	}
	c.conn.Close()

	c = wsDial(t, addr)
	c.send(true, 1, []byte("close"))
	c.expect(8, []byte{0x03, 0xe9, 'h', 'a', 'n', 'd', 'l', 'e', 'r'}, "the handler's close")
	c.send(true, 8, []byte{0x03, 0xe9})
	if _, _, err := c.recv(); err == nil {
		t.Fatalf("the server kept the connection open after its own close was answered")
	}
	c.conn.Close()

	c = wsDial(t, addr)
	c.send(true, 3, []byte("?"))
	c.expect(8, []byte{0x03, 0xea}, "an unknown opcode closed 1002")
	c.conn.Close()

	checkDeclinedHandshakeKeepsConnection(t, addr)

	if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("/ok after the sessions: want 200, got\n%s", resp)
	}
}

func checkDeclinedHandshakeKeepsConnection(t *testing.T, addr string) {
	t.Helper()
	conn := dialRaw(t, addr)
	defer conn.Close()
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	handshake := "GET /ws HTTP/1.1\r\nHost: h\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 8\r\n\r\n"
	if _, err := io.WriteString(conn, handshake); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(conn)
	declined, err := http.ReadResponse(r, nil)
	if err != nil {
		t.Fatalf("declined handshake: %v", err)
	}
	if declined.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("declined handshake: status %d, want 426", declined.StatusCode)
	}
	_, _ = io.Copy(io.Discard, declined.Body)
	declined.Body.Close()
	if _, err := io.WriteString(conn, "GET /ok HTTP/1.1\r\nHost: h\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	after, err := http.ReadResponse(r, nil)
	if err != nil {
		t.Fatalf("request after a declined handshake (connection not kept): %v", err)
	}
	if after.StatusCode != http.StatusOK {
		t.Fatalf("request after a declined handshake: status %d, want 200", after.StatusCode)
	}
	_, _ = io.Copy(io.Discard, after.Body)
	after.Body.Close()
}

func CheckWebSocketOverlap(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr)
	c := wsDial(t, addr)
	c.send(true, 1, []byte("first"))
	c.expect(1, []byte("first"), "text echo before the other connection")
	time.Sleep(50 * time.Millisecond)
	started := time.Now()
	if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("/ok beside an idle session: want 200, got\n%s", resp)
	}
	if waited := time.Since(started); waited > 500*time.Millisecond {
		t.Fatalf("/ok beside an idle session took %v: the worker stalled on the session", waited)
	}
	c.send(true, 1, []byte("second"))
	c.expect(1, []byte("second"), "text echo after the other connection")
	c.send(true, 8, []byte{0x03, 0xe8})
	c.expect(8, []byte{0x03, 0xe8}, "the close echoed")
	c.conn.Close()
}
