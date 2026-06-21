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
                var b: i32 = s[i];
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

// The complete wasm edge payoff: two OVERLAPPED outbound fetches whose
// response bodies come back via std/wasm_reactor.run over the
// connections' pollables (tcp_pollable → tcp-socket.subscribe) — the
// wasm analog of native TestReactorFanoutBodies. Task 0 talks to a
// slow upstream (200ms) carrying "AAA", task 1 to a fast one (10ms)
// carrying "BBB"; both reads overlap on one thread (the reactor blocks
// in wasm_poll), and the results come back in TASK order regardless of
// completion order. Proves tcp_connect + tcp_pollable + tcp_recv +
// the reactor compose into real concurrent I/O on the edge target.
func TestWasmReactorOutboundFanout(t *testing.T) {
	skipIfPreview2Missing(t)

	serve := func(t *testing.T, payload string, delay time.Duration) int {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		t.Cleanup(func() { ln.Close() })
		go func() {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			buf := make([]byte, 256)
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			_, _ = conn.Read(buf)
			time.Sleep(delay)
			_, _ = conn.Write([]byte(payload))
		}()
		return ln.Addr().(*net.TCPAddr).Port
	}
	pSlow := serve(t, "AAA\n", 200*time.Millisecond)
	pFast := serve(t, "BBB\n", 10*time.Millisecond)

	src := `import "std/wasm_reactor";
function parse(s: string): i32 {
    var n: i32 = 0; var i: i32 = 0;
    while (i < s.len()) { var b: i32 = s[i]; if (b < 48 || b > 57) { return 0; } n = n * 10 + (b - 48); i = i + 1; }
    return n;
}
function port(key: string): i32 { match (env(key)) { Some(s) => { return parse(s); }, None => { return 0; } } }

function start(p: i32): wasm_reactor.Step[string] {
    var host: i32 = 127 | (1 << 24);   // 127.0.0.1
    var c: i32 = tcp_connect(host, p);
    tcp_send(c, "GET / HTTP/1.1\r\nConnection: close\r\n\r\n");
    function resume(pol: i32): wasm_reactor.Step[string] { return Done(tcp_recv(c, 4096)); }
    return Wait(tcp_pollable(c), resume);
}

function main(): i32 {
    var tasks: wasm_reactor.Step[string][] = [start(port("PSLOW")), start(port("PFAST"))];
    var bodies: string[] = wasm_reactor.run(tasks, "");
    if (bodies.len() != 2) { return 90; }
    print(bodies[0]);   // task 0 (slow upstream) → "AAA"
    print(bodies[1]);   // task 1 (fast upstream) → "BBB"
    return 0;
}`

	compPath := buildComponent(t, src)
	run := exec.Command("wasmtime", "run", "-S", "inherit-network",
		"--env", "PSLOW="+strconv.Itoa(pSlow), "--env", "PFAST="+strconv.Itoa(pFast), compPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	start := time.Now()
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime run: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
	}
	elapsed := time.Since(start)
	out := sout.String()
	// Task-order results: AAA (task 0) must precede BBB (task 1) in the
	// output even though BBB's upstream answered first.
	ai := bytes.Index(sout.Bytes(), []byte("AAA"))
	bi := bytes.Index(sout.Bytes(), []byte("BBB"))
	if ai < 0 || bi < 0 {
		t.Fatalf("fan-out: missing a body in %q\nstderr:\n%s", out, serr.String())
	}
	if ai > bi {
		t.Errorf("fan-out: results not in task order (AAA after BBB) in %q", out)
	}
	// Overlap sanity: both reads share one thread, so the run is bounded
	// by the slow upstream (~200ms), not the sum (~210ms+); allow ample
	// slack for wasmtime startup but catch gross serialization.
	if elapsed > 2*time.Second {
		t.Errorf("fan-out took %v — expected overlapped (~200ms + startup)", elapsed)
	}
}
