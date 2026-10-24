package e2e

import (
	"bytes"
	"net"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestWasmTcpConnectOutbound(t *testing.T) {
	skipIfPreview2Missing(t)

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
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte(body))
	}()

	src := `import "std/net";

function port_from_env(): i32 {
    match (env("PORT")) {
        Some(s) => {
            let n: i32 = 0;
            let i: i32 = 0;
            while (i < s.len()) {
                let b: i32 = s[i] as i32;
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
    let c: i32 = match (net.connect(net.socket_addr(net.ipv4_loopback(), port_from_env()))) {
        Ok(c) => c,
        Err(_) => { return 1; },
    };
    match (net.write(c, [71u8, 69u8, 84u8, 32u8, 47u8, 32u8, 72u8, 84u8, 84u8, 80u8, 47u8, 49u8, 46u8, 49u8, 13u8, 10u8, 67u8, 111u8, 110u8, 110u8, 101u8, 99u8, 116u8, 105u8, 111u8, 110u8, 58u8, 32u8, 99u8, 108u8, 111u8, 115u8, 101u8, 13u8, 10u8, 13u8, 10u8])) { Err(_) => { return 2; }, Ok(_) => {} }
    match (net.read_watched(c, 4096, 5000i64, 1)) {
        Ok(net.Chunk(b)) => { print(string_from_bytes_unchecked(b)); },
        _ => { return 3; },
    }
    tcp_close(c);
    return 0;
}`

	compPath := buildCLIComponent(t, src)

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
