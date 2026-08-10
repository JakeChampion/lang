package e2e

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// std/async is the blessed structured-concurrency surface
// (docs/ASYNC-REDESIGN.md): `gather` / `race` / `with_deadline` over a
// portable `Future[T]`, replacing the `concurrent`/`await`/`race`
// keyword surface. These two tests pin (1) the combinators are portable
// — a fan-out of already-`Ready` futures resolves on interp + wasm —
// and (2) `Pending` futures drive REAL overlapping socket I/O on the
// native backends through `gather` and `race`.

// Portable: gather/race/with_deadline over Ready futures resolve on
// interp (exit 42) and the program compiles on wasm. No real I/O — this
// is the backend-agnostic combinator contract.
func TestAsyncCombinatorsPortable(t *testing.T) {
	bin := buildFernCLI(t)
	const src = `import "std/async";
function main(): i32 {
    var fs: async.Future[i32][] = [Ready(5), Ready(7), Ready(30)];
    var summed: i32[] = async.gather(fs, -1);
    var sum: i32 = summed[0] + summed[1] + summed[2];   // 42
    var (w, v) = async.race(fs, -1);                    // (0, 5)
    var d: Option[i32][] = async.with_deadline(50, fs); // [Some(5),Some(7),Some(30)]
    var d2: i32 = 0;
    match (d[2]) { Some(x) => { d2 = x; }, None => { } }
    if (sum == 42 && w == 0 && v == 5 && d2 == 30) { return 42; }
    return 1;
}`

	t.Run("interp", func(t *testing.T) {
		cmd := exec.Command(bin, "-interp", "-")
		cmd.Stdin = strings.NewReader(src)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 42 {
			t.Errorf("interp gather/race/with_deadline exit = %d, want 42", code)
		}
	})

	t.Run("wasm-compiles", func(t *testing.T) {
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "async.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, "async.wasm")
		if o, err := exec.Command(bin, "-target", "wasm32-wasi", "-o", out, srcPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm build of a std/async program failed: %v\n%s", err, o)
		}
	})
}

// Native real I/O: two parallel tcp_connect fetches expressed as
// `Pending` futures and driven through `async.gather` (await-all) and
// `async.race` (first-wins) over real sockets. The combinator surface
// overlapping real descriptors on one thread — the edge-handler
// fan-out, now through the redesign's blessed API rather than the
// hand-rolled reactor. A Go upstream answers both connections; exit 42
// iff the combinator returned the expected result(s).
func TestAsyncCombinatorsRealFd(t *testing.T) {
	bin := buildFernCLI(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no free TCP port: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				b := make([]byte, 256)
				_, _ = c.Read(b)
				fmt.Fprint(c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
			}(conn)
		}
	}()

	const host = 127 | (1 << 24)

	gatherSrc := fmt.Sprintf(`import "std/async";

function fetch_future(conn: i32): async.Future[i32] {
    function resume(woken_fd: i32): async.Future[i32] {
        var resp: string = tcp_recv(woken_fd, 4096);
        if (resp.len() > 0) { return Ready(1); }
        return Ready(0);
    }
    return Pending(conn, resume);
}

function main(): i32 {
    var c1: i32 = tcp_connect(%d, %d);
    var c2: i32 = tcp_connect(%d, %d);
    if (c1 < 0) { return 81; }
    if (c2 < 0) { return 82; }
    if (tcp_send(c1, "GET /1 HTTP/1.1\r\nHost: x\r\n\r\n") < 0) { return 83; }
    if (tcp_send(c2, "GET /2 HTTP/1.1\r\nHost: x\r\n\r\n") < 0) { return 84; }
    var fs: async.Future[i32][] = [fetch_future(c1), fetch_future(c2)];
    var r: i32[] = async.gather(fs, -1);
    if (r[0] == 1 && r[1] == 1) { return 42; }
    return 85;
}`, host, port, host, port)

	raceSrc := fmt.Sprintf(`import "std/async";

function fetch_future(conn: i32): async.Future[i32] {
    function resume(woken_fd: i32): async.Future[i32] {
        var resp: string = tcp_recv(woken_fd, 4096);
        if (resp.len() > 0) { return Ready(1); }
        return Ready(0);
    }
    return Pending(conn, resume);
}

function main(): i32 {
    var c1: i32 = tcp_connect(%d, %d);
    var c2: i32 = tcp_connect(%d, %d);
    if (c1 < 0) { return 81; }
    if (c2 < 0) { return 82; }
    if (tcp_send(c1, "GET /1 HTTP/1.1\r\nHost: x\r\n\r\n") < 0) { return 83; }
    if (tcp_send(c2, "GET /2 HTTP/1.1\r\nHost: x\r\n\r\n") < 0) { return 84; }
    var fs: async.Future[i32][] = [fetch_future(c1), fetch_future(c2)];
    var (winner, result) = async.race(fs, -1);
    if (winner >= 0 && result == 1) { return 42; }
    return 86;
}`, host, port, host, port)

	dir := t.TempDir()
	progs := []struct {
		name string
		src  string
	}{
		{"gather", gatherSrc},
		{"race", raceSrc},
	}
	backends := []struct {
		target string
		qemu   func(*testing.T) string
		run    func(qemu, bin string, args ...string) *exec.Cmd
	}{
		{"x86-64-linux", x86QemuOrEmpty, runX86Bin},
		{"arm64-linux", arm64QemuOrEmpty, runArm64Bin},
	}
	for _, p := range progs {
		p := p
		srcPath := filepath.Join(dir, p.name+".fern")
		if err := os.WriteFile(srcPath, []byte(p.src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		for _, be := range backends {
			be := be
			t.Run(p.name+"/"+be.target, func(t *testing.T) {
				qemu := be.qemu(t) // skips if no runner
				out := filepath.Join(dir, p.name+"_"+be.target+".bin")
				if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
					t.Fatalf("build failed: %v\n%s", err, o)
				}
				cmd := be.run(qemu, out)
				_ = cmd.Run()
				if code := cmd.ProcessState.ExitCode(); code != 42 {
					t.Errorf("%s/%s: std/async real-fd fan-out exit = %d, want 42", p.name, be.target, code)
				}
			})
		}
	}
}
