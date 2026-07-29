package e2e

import (
	"bytes"
	"net"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// The wasm outbound TCP client: tcp_connect (the wasm analog of the
// native builtin) drives wasi:sockets/tcp start-connect / finish-connect
// through the composer's connect instance-type variant. A Go upstream
// listens; the wasm guest connects to it, sends a request, reads the
// reply, and prints it. The whole point is the OUTBOUND direction —
// today the wasm TCP path was server-only — so a successful round-trip
// proves connect + the connection's input/output streams work end to
// end under `wasmtime -S inherit-network`.
func TestWasmTcpConnectOutbound(t *testing.T) {
	skipIfPreview2Missing(t)

	// Go upstream: accept one connection, drain the request, reply with
	// a known body, close.
	const body = "hello-from-upstream\n"
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 256)
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, _ = conn.Read(buf) // drain the guest's request
		_, _ = conn.Write([]byte(body))
	}()

	// Guest: read PORT from env, connect to 127.0.0.1:PORT, send a
	// request, recv the reply, print it. 127.0.0.1 packs (std/fetch
	// ipv4 convention a|b<<8|c<<16|d<<24) to 127 | 1<<24.
	src := `function port_from_env(): i32 {
    match (env("PORT")) {
        Some(s) => {
            var n: i32 = 0;
            var i: i32 = 0;
            while (i < s.len()) {
                var b: i32 = s[i] as i32;
                if (b < 48 || b > 57) { return 8080; }
                n = n * 10 + (b - 48);
                i = i + 1;
            }
            if (i == 0) { return 8080; }
            return n;
        },
        None => { return 8080; }
    }
}

function main(): i32 {
    var host: i32 = 127 | (1 << 24);   // 127.0.0.1
    var c: i32 = tcp_connect(host, port_from_env());
    if (c < 0) { return 1; }
    if (tcp_send(c, "GET / HTTP/1.1\r\nConnection: close\r\n\r\n") < 0) { return 2; }
    var resp: string = tcp_recv(c, 4096);
    print(resp);
    tcp_close(c);
    return 0;
}`

	compPath := buildComponent(t, src)

	run := exec.Command("wasmtime", "run", "-S", "inherit-network", "--env", "PORT="+strconv.Itoa(port), compPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime run: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
	}
	if !bytes.Contains(sout.Bytes(), []byte(body)) {
		t.Errorf("outbound connect: response %q missing upstream body %q\nstderr:\n%s", sout.String(), body, serr.String())
	}
}
