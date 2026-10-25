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

func TestAsyncCombinatorsPortable(t *testing.T) {
	bin := buildFernCLI(t)
	const src = `import "std/async";
import "std/time";
function main(): i32 {
    let fs: async.Future[i32][] = [Ready(5), Ready(7), Ready(30)];
    let summed: i32[] = async.gather(fs, -1);
    let sum: i32 = summed[0] + summed[1] + summed[2];   // 42
    let (w, v) = async.race(fs, -1);                    // (0, 5)
    let d: Option[i32][] = async.with_deadline(time.duration_millis(50), fs); // [Some(5),Some(7),Some(30)]
    let d2: i32 = 0;
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

func TestAsyncCombinatorsRealFd(t *testing.T) {
	bin := buildFernCLI(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no free TCP port: %v", err)
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

	gatherSrc := fmt.Sprintf(`import "std/async";

function read_future(conn: i32): async.Future[i32] {
    function resume(woken_fd: i32): async.Future[i32] {
        let resp: u8[] = __alloc_u8(4096);
        if (tcp_recv_into(woken_fd, resp) > 0) { return Ready(1); }
        return Ready(0);
    }
    return Pending(conn, resume);
}

function main(): i32 {
    let c1: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], %d, false);
    let c2: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], %d, false);
    if (c1 < 0) { return 81; }
    if (c2 < 0) { return 82; }
    if (tcp_send_bytes(c1, [71u8, 69u8, 84u8, 32u8, 47u8, 49u8, 32u8, 72u8, 84u8, 84u8, 80u8, 47u8, 49u8, 46u8, 49u8, 13u8, 10u8, 72u8, 111u8, 115u8, 116u8, 58u8, 32u8, 120u8, 13u8, 10u8, 13u8, 10u8]) < 0) { return 83; }
    if (tcp_send_bytes(c2, [71u8, 69u8, 84u8, 32u8, 47u8, 50u8, 32u8, 72u8, 84u8, 84u8, 80u8, 47u8, 49u8, 46u8, 49u8, 13u8, 10u8, 72u8, 111u8, 115u8, 116u8, 58u8, 32u8, 120u8, 13u8, 10u8, 13u8, 10u8]) < 0) { return 84; }
    let fs: async.Future[i32][] = [read_future(c1), read_future(c2)];
    let r: i32[] = async.gather(fs, -1);
    if (r[0] == 1 && r[1] == 1) { return 42; }
    return 85;
}`, port, port)

	raceSrc := fmt.Sprintf(`import "std/async";

function read_future(conn: i32): async.Future[i32] {
    function resume(woken_fd: i32): async.Future[i32] {
        if (woken_fd == async.cancelled()) { return Ready(0); }
        let resp: u8[] = __alloc_u8(4096);
        if (tcp_recv_into(woken_fd, resp) > 0) { return Ready(1); }
        return Ready(0);
    }
    return Pending(conn, resume);
}

function main(): i32 {
    let c1: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], %d, false);
    let c2: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], %d, false);
    if (c1 < 0) { return 81; }
    if (c2 < 0) { return 82; }
    if (tcp_send_bytes(c1, [71u8, 69u8, 84u8, 32u8, 47u8, 49u8, 32u8, 72u8, 84u8, 84u8, 80u8, 47u8, 49u8, 46u8, 49u8, 13u8, 10u8, 72u8, 111u8, 115u8, 116u8, 58u8, 32u8, 120u8, 13u8, 10u8, 13u8, 10u8]) < 0) { return 83; }
    if (tcp_send_bytes(c2, [71u8, 69u8, 84u8, 32u8, 47u8, 50u8, 32u8, 72u8, 84u8, 84u8, 80u8, 47u8, 49u8, 46u8, 49u8, 13u8, 10u8, 72u8, 111u8, 115u8, 116u8, 58u8, 32u8, 120u8, 13u8, 10u8, 13u8, 10u8]) < 0) { return 84; }
    let fs: async.Future[i32][] = [read_future(c1), read_future(c2)];
    let (winner, result) = async.race(fs, -1);
    if (winner >= 0 && result == 1) { return 42; }
    return 86;
}`, port, port)

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
				qemu := be.qemu(t)
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
