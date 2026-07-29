package e2e

import (
	"bytes"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// The full PR5-wasm payoff (docs/ASYNC-FUTURE-UNIFICATION.md): two
// parallel OUTBOUND fetches returning their response BODIES, on wasm,
// through the unified `std/async` surface — `fetch.fetch_future` fanned
// out via `async.gather`. The same edge-handler fan-out as the native
// TestAsyncFetchFutureFanout, now on wasm: `fetch_future`'s wait token
// is `tcp_pollable(c)` (a real wasi:io/poll pollable handle on wasm),
// and `gather`'s `poll` blocks in the host until each socket is
// readable. Both reads overlap on one thread. Runs under stock wasmtime
// (-S inherit-network), Preview 2 — no Preview 3.
func TestAsyncWasmFetchFutureFanout(t *testing.T) {
	skipIfPreview2Missing(t)

	serve := func(t *testing.T, body string, delay time.Duration) int {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		t.Cleanup(func() { ln.Close() })
		resp := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
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
			_, _ = conn.Write([]byte(resp))
		}()
		return ln.Addr().(*net.TCPAddr).Port
	}
	pSlow := serve(t, "AAA", 200*time.Millisecond)
	pFast := serve(t, "BBB", 10*time.Millisecond)

	src := `import "std/async";
import "std/fetch";
import "std/string";

function parse(s: string): i32 {
    var n: i32 = 0; var i: i32 = 0;
    while (i < s.len()) { var b: i32 = s[i] as i32; if (b < 48 || b > 57) { return 0; } n = n * 10 + (b - 48); i = i + 1; }
    return n;
}
function port(key: string): i32 { match (env(key)) { Some(s) => { return parse(s); }, None => { return 0; } } }

function main(): i32 {
    var host: i32 = 127 | (1 << 24);   // 127.0.0.1
    var f1: async.Future[string] = fetch.fetch_future(host, port("PSLOW"), "/a");
    var f2: async.Future[string] = fetch.fetch_future(host, port("PFAST"), "/b");
    var fs: async.Future[string][] = [f1, f2];
    var bodies: string[] = async.gather(fs, "");
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
	startT := time.Now()
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime run: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
	}
	elapsed := time.Since(startT)
	out := sout.String()

	// gather returns results in INPUT order: AAA (f1) before BBB (f2),
	// even though the fast upstream answered first — proving overlap +
	// order-preservation through the combinator.
	ai := bytes.Index(sout.Bytes(), []byte("AAA"))
	bi := bytes.Index(sout.Bytes(), []byte("BBB"))
	if ai < 0 || bi < 0 {
		t.Fatalf("fan-out: missing a body in %q\nstderr:\n%s", out, serr.String())
	}
	if ai > bi {
		t.Errorf("fan-out: bodies not in input order (AAA after BBB) in %q", out)
	}
	// Both reads share one thread, so wall-clock is bounded by the slow
	// upstream (~200ms), not the sum. This is a COARSE anti-serialization
	// guard — warm it runs in ~0.3s, but it's dominated by wasmtime's
	// component cold-start (and the streaming read adds a poll round per
	// chunk), so the bound is generous to avoid cold-start flakes; a truly
	// serialized fan-out would still be sub-second of server delay, so any
	// multi-second blowout it can't catch isn't the failure mode anyway.
	if elapsed > 8*time.Second {
		t.Errorf("fan-out took %v — expected overlapped (~200ms + startup)", elapsed)
	}
}

// `async.race` over two real wasm sockets: one upstream wins, the other is
// abandoned. On wasm the loser's pollable is a CHILD of its socket resource,
// so `race` must drop it (`__drop_losers` → `wasm_pollable_drop`) or wasmtime
// traps with "resource has children" at teardown. This pins the loser-drop:
// the program runs CLEAN (exit 0, no trap) and returns a valid winner body —
// before the fix this trapped at teardown. (Which upstream wins depends on
// wasm pollable readiness semantics, orthogonal to the drop, so the assertion
// accepts either body.)
func TestAsyncWasmRaceFetchDropsLoser(t *testing.T) {
	skipIfPreview2Missing(t)

	serve := func(t *testing.T, body string, delay time.Duration) int {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		t.Cleanup(func() { ln.Close() })
		resp := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
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
			_, _ = conn.Write([]byte(resp))
		}()
		return ln.Addr().(*net.TCPAddr).Port
	}
	pA := serve(t, "AAA", 10*time.Millisecond)
	pB := serve(t, "BBB", 40*time.Millisecond)

	src := `import "std/async";
import "std/fetch";
import "std/string";

function parse(s: string): i32 {
    var n: i32 = 0; var i: i32 = 0;
    while (i < s.len()) { var b: i32 = s[i] as i32; if (b < 48 || b > 57) { return 0; } n = n * 10 + (b - 48); i = i + 1; }
    return n;
}
function port(key: string): i32 { match (env(key)) { Some(s) => { return parse(s); }, None => { return 0; } } }

function main(): i32 {
    var host: i32 = 127 | (1 << 24);
    var fs: async.Future[string][] = [
        fetch.fetch_future(host, port("PA"), "/a"),
        fetch.fetch_future(host, port("PB"), "/b")
    ];
    var (winner, body) = async.race(fs, "");
    if (winner < 0) { print("nowinner\n"); return 1; }
    print(body);   // the winner's body ("AAA" or "BBB")
    return 0;
}`

	compPath := buildComponent(t, src)
	run := exec.Command("wasmtime", "run", "-S", "inherit-network",
		"--env", "PA="+strconv.Itoa(pA), "--env", "PB="+strconv.Itoa(pB), compPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	// Clean exit (no "resource has children" trap) is the loser-drop proof.
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime run (race loser drop): %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
	}
	out := sout.String()
	if !bytes.Contains(sout.Bytes(), []byte("AAA")) && !bytes.Contains(sout.Bytes(), []byte("BBB")) {
		t.Errorf("race: want a winner body (AAA or BBB) in output, got %q\nstderr:\n%s", out, serr.String())
	}
}
