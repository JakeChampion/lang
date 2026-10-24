package e2ecompiler

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSelfHostTcpServerIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../../compiler", "drivers/asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	prog := `function port_str(n: i32): string {
    let s: string = "";
    let v: i32 = n;
    while (v > 0) {
        let d: i32 = v % 10;
        s = slice_unchecked("0123456789", d, d + 1) + s;
        v = v / 10;
    }
    return s;
}

function main(): i32 {
    let fd: i32 = tcp_listen(0);
    if (fd < 0) { return 91; }
    let port: i32 = tcp_local_port(fd);
    if (port <= 0) { return 97; }
    print(port_str(port));
    let lfds: i32[] = [fd];
    if (poll(lfds, 10000) < 0) { return 95; }
    let c: i32 = tcp_accept(fd);
    if (c < 0) { return 92; }
    let cfds: i32[] = [c];
    if (poll(cfds, 10000) < 0) { return 96; }
    let req: u8[] = __alloc_u8(4096);
    if (tcp_recv_into(c, req) <= 0) { return 93; }
    let n: i32 = tcp_send_bytes(c, [72u8, 84u8, 84u8, 80u8, 47u8, 49u8, 46u8, 49u8, 32u8, 50u8, 48u8, 48u8, 32u8, 79u8, 75u8, 13u8, 10u8, 67u8, 111u8, 110u8, 116u8, 101u8, 110u8, 116u8, 45u8, 76u8, 101u8, 110u8, 103u8, 116u8, 104u8, 58u8, 32u8, 53u8, 13u8, 10u8, 13u8, 10u8, 104u8, 101u8, 108u8, 108u8, 111u8]);
    tcp_close(c);
    tcp_close(fd);
    if (n < 0) { return 94; }
    return 42;
}`

	asm := runCapture(t, gcc, runner, driverBin, []byte(prog+"\n"))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the TCP server program")
	}
	progBin := buildBin(t, gcc, dir, "tcp_server", string(asm))

	cmd := runX86_64Bin(runner, progBin)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	port := readAnnouncedPort(t, stdout)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		t.Fatalf("dial the announced port %d: %v", port, err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	buf := make([]byte, 256)
	n, _ := bufio.NewReader(conn).Read(buf)
	resp := string(buf[:n])
	conn.Close()

	if !strings.Contains(resp, "hello") {
		t.Errorf("response = %q, want it to contain %q (self-host IR tcp_listen/accept serve path, #4371)", resp, "hello")
	}
	_ = cmd.Wait()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("self-host TCP server exit = %d, want 42 (listen/local_port/accept/recv/send on the announced port %d; #4371)", code, port)
	}
}

func readAnnouncedPort(t *testing.T, stdout io.Reader) int {
	t.Helper()
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		ch <- result{line, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("reading the server's port line: %v", r.err)
		}
		port, err := strconv.Atoi(strings.TrimSpace(r.line))
		if err != nil || port <= 0 || port > 65535 {
			t.Fatalf("server announced %q, want a port", r.line)
		}
		return port
	case <-time.After(20 * time.Second):
		t.Fatal("server never announced a port")
		return 0
	}
}
