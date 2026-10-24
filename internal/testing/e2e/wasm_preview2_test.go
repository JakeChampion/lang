package e2e

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWasmPreview2HelloWorld(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "hello.fern")
	if err := os.WriteFile(srcPath, []byte(`function main(): i32 {
    let b = random_bytes(8);
    print("hello preview2");
    eprint("err preview2");
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	bin := buildFernCLI(t)

	componentPath := filepath.Join(dir, "hello.component.wasm")
	emit := exec.Command(bin,
		"-target", "wasm32-wasi",
		"-o", componentPath,
		srcPath,
	)
	var obuf, ebuf bytes.Buffer
	emit.Stdout = &obuf
	emit.Stderr = &ebuf
	if err := emit.Run(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\nstdout:\n%s\nstderr:\n%s", err, obuf.String(), ebuf.String())
	}

	dump := exec.Command("wasm-tools", "print", componentPath)
	dout, err := dump.CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print: %v\n%s", err, dout)
	}
	if !strings.Contains(string(dout), "(component") {
		t.Fatalf("output is not a component (no `(component` token):\n%s", dout)
	}

	run := exec.Command("wasmtime", "run", componentPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime run %s: %v\nstdout:\n%s\nstderr:\n%s",
			componentPath, err, sout.String(), serr.String())
	}
	if got, want := strings.TrimRight(sout.String(), "\n"), "hello preview2"; got != want {
		t.Fatalf("stdout = %q; want %q (stderr=%q)", got, want, serr.String())
	}
	if got, want := strings.TrimRight(serr.String(), "\n"), "err preview2"; got != want {
		t.Fatalf("stderr = %q; want %q (stdout=%q)", got, want, sout.String())
	}
}

func TestWasmPreview2StdinReadLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "echo.fern")
	if err := os.WriteFile(srcPath, []byte(`import "std/io";

function main(): i32 {
    while (true) {
        match (io.read_line(stdin())) {
            Ok(Some(line)) => { write(line); },
            Ok(None()) => { return 0; },
            Err(_) => { return 1; }
        }
    }
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	bin := buildFernCLI(t)

	componentPath := filepath.Join(dir, "echo.component.wasm")
	emit := exec.Command(bin,
		"-target", "wasm32-wasi",
		"-o", componentPath,
		srcPath,
	)
	var emitOut, emitErr bytes.Buffer
	emit.Stdout = &emitOut
	emit.Stderr = &emitErr
	if err := emit.Run(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\nstdout:\n%s\nstderr:\n%s", err, emitOut.String(), emitErr.String())
	}

	input := "alpha\nbeta\ngamma\n"
	run := exec.Command("wasmtime", "run", componentPath)
	run.Stdin = strings.NewReader(input)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime run %s: %v\nstdout:\n%s\nstderr:\n%s",
			componentPath, err, sout.String(), serr.String())
	}
	if got := sout.String(); got != input {
		t.Fatalf("stdout = %q; want %q (stderr=%q)", got, input, serr.String())
	}
}

func TestWasmPreview2FileRoundtrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "fs.fern")
	if err := os.WriteFile(srcPath, []byte(`import "std/io";

function main(): i32 {
    match (open_writer("out.txt")) {
        Ok(w) => {
            match (w.write("line 1\n")) { Err(_) => { return 1; }, Ok(_) => {} }
            match (w.write("line 2\n")) { Err(_) => { return 2; }, Ok(_) => {} }
            match (w.close()) { Err(_) => { return 3; }, Ok(_) => {} }
        },
        Err(_) => { return 4; }
    }
    match (open_reader("out.txt")) {
        Ok(r) => {
            match (io.read_line(r)) { Ok(Some(line)) => { write(line); }, _ => { return 5; } }
            match (io.read_line(r)) { Ok(Some(line)) => { write(line); }, _ => { return 6; } }
            match (io.read_line(r)) { Ok(None()) => {}, _ => { return 7; } }
            match (r.close()) { Err(_) => { return 8; }, Ok(_) => {} }
        },
        Err(_) => { return 9; }
    }
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	bin := buildFernCLI(t)

	componentPath := filepath.Join(dir, "fs.component.wasm")
	emit := exec.Command(bin,
		"-target", "wasm32-wasi",
		"-o", componentPath,
		srcPath,
	)
	var emitOut, emitErr bytes.Buffer
	emit.Stdout = &emitOut
	emit.Stderr = &emitErr
	if err := emit.Run(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\nstdout:\n%s\nstderr:\n%s", err, emitOut.String(), emitErr.String())
	}

	run := exec.Command("wasmtime", "run", "--dir", dir, componentPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime run: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
	}
	want := "line 1\nline 2\n"
	if got := sout.String(); got != want {
		t.Fatalf("stdout = %q; want %q (stderr=%q)", got, want, serr.String())
	}
	on_disk, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("read out.txt: %v", err)
	}
	if string(on_disk) != want {
		t.Fatalf("on-disk = %q; want %q", string(on_disk), want)
	}
}

func TestWasmPreview2FileReadWriteAdapterFree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	want := "copied-through-wasm"
	if err := os.WriteFile(filepath.Join(root, "in.txt"), []byte(want), 0o644); err != nil {
		t.Fatalf("write in: %v", err)
	}
	srcPath := filepath.Join(dir, "rw.fern")
	src := `function main(): i32 {
    match (read_file("in.txt")) {
        Ok(content) => {
            match (write_file("out.txt", content)) { Err(e) => { return 2; }, Ok(_) => {} }
            return 0;
        },
        Err(e) => { return 1; }
    }
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	bin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}
	componentPath := filepath.Join(dir, "rw.wasm")
	if out, err := exec.Command(bin, "-target", "wasm32-wasi", "-o", componentPath, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi (read+write, no adapter): %v\n%s", err, out)
	}
	wit, err := exec.Command("wasm-tools", "component", "wit", componentPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools component wit: %v\n%s", err, wit)
	}
	for _, w := range []string{"read-via-stream", "write-via-stream"} {
		if !bytes.Contains(wit, []byte(w)) {
			t.Errorf("expected %q in the component, got:\n%s", w, wit)
		}
	}
	if out, err := exec.Command("wasmtime", "run", "--dir", root+"::/", componentPath).CombinedOutput(); err != nil {
		t.Fatalf("wasmtime run (read+write): %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(root, "out.txt"))
	if err != nil {
		t.Fatalf("read out.txt: %v", err)
	}
	if string(got) != want {
		t.Fatalf("out.txt = %q; want %q", string(got), want)
	}
}

func TestWasmPreview2FileCloseAdapterFree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}
	fsdir := filepath.Join(dir, "fs")
	if err := os.Mkdir(fsdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	build := func(name, src string) string {
		srcPath := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		comp := filepath.Join(dir, name+".wasm")
		emit := exec.Command(bin, "-target", "wasm32-wasi", "-o", comp, srcPath)
		if out, err := emit.CombinedOutput(); err != nil {
			t.Fatalf("fern -target wasm32-wasi (%s, no adapter): %v\n%s", name, err, out)
		}
		printed, err := exec.Command("wasm-tools", "print", comp).CombinedOutput()
		if err != nil {
			t.Fatalf("wasm-tools print (%s): %v\n%s", name, err, printed)
		}
		if bytes.Contains(printed, []byte("fd_close")) || bytes.Contains(printed, []byte("wasi_snapshot_preview1")) {
			t.Errorf("%s: expected no preview-1 imports, got:\n%s", name, printed)
		}
		return comp
	}

	want := "close-via-drop\n"
	writer := build("writer", `function main(): i32 {
    match (open_writer("f.txt")) {
        Ok(w) => {
            match (w.write("close-via-drop\n")) { Err(_) => { return 1; }, Ok(_) => {} }
            match (w.close()) { Err(_) => { return 2; }, Ok(_) => {} }
            return 0;
        },
        Err(_) => { return 3; }
    }
}`)
	if out, err := exec.Command("wasmtime", "run", "--dir", fsdir+"::/", writer).CombinedOutput(); err != nil {
		t.Fatalf("wasmtime run writer: %v\n%s", err, out)
	}
	onDisk, err := os.ReadFile(filepath.Join(fsdir, "f.txt"))
	if err != nil {
		t.Fatalf("read f.txt: %v", err)
	}
	if string(onDisk) != want {
		t.Fatalf("on-disk = %q; want %q", string(onDisk), want)
	}

	reader := build("reader", `import "std/io";

function main(): i32 {
    match (open_reader("f.txt")) {
        Ok(r) => {
            match (io.read_line(r)) { Ok(Some(line)) => { write(line); }, _ => { return 1; } }
            match (r.close()) { Err(_) => { return 2; }, Ok(_) => {} }
            return 0;
        },
        Err(_) => { return 3; }
    }
}`)
	out, err := exec.Command("wasmtime", "run", "--dir", fsdir+"::/", reader).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run reader: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("close-via-drop")) {
		t.Errorf("reader stdout = %q; want it to contain the file line", string(out))
	}
}

func TestWasmPreview2ReadWriteFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "rwf.fern")
	if err := os.WriteFile(srcPath, []byte(`function main(): i32 {
    let content = "";
    let i = 0;
    while (i < 600) {
        content = content + "hello world\n";
        i = i + 1;
    }
    match (write_file("rwf.txt", content)) {
        Err(_) => { return 1; },
        Ok(_) => {}
    }
    match (read_file("rwf.txt")) {
        Ok(s) => {
            if (s.len() == content.len()) { print("match"); }
            else { print("mismatch"); }
        },
        Err(_) => { return 2; }
    }
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	bin := buildFernCLI(t)

	componentPath := filepath.Join(dir, "rwf.component.wasm")
	emit := exec.Command(bin,
		"-target", "wasm32-wasi",
		"-o", componentPath,
		srcPath,
	)
	var emitOut, emitErr bytes.Buffer
	emit.Stdout = &emitOut
	emit.Stderr = &emitErr
	if err := emit.Run(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\nstdout:\n%s\nstderr:\n%s", err, emitOut.String(), emitErr.String())
	}

	run := exec.Command("wasmtime", "run", "--dir", dir, componentPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime run: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
	}
	if got := strings.TrimRight(sout.String(), "\n"); got != "match" {
		t.Fatalf("stdout = %q; want %q (stderr=%q)", got, "match", serr.String())
	}
	on_disk, err := os.ReadFile(filepath.Join(dir, "rwf.txt"))
	if err != nil {
		t.Fatalf("read rwf.txt: %v", err)
	}
	if want := strings.Repeat("hello world\n", 600); string(on_disk) != want {
		t.Fatalf("on-disk len=%d, want %d", len(on_disk), len(want))
	}
}

func TestWasmPreview2TcpEcho(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "echo.fern")
	src := `import "core/int";
import "std/net";

function main(): i32 {
    let sock = tcp_listen(0);
    if (sock < 0) { return 1; }
    let port: i32 = tcp_local_port(sock);
    if (port <= 0) { return 4; }
    print(int.int_to_string(port));
    let conn = tcp_accept(sock);
    if (conn < 0) { return 2; }
    let msg: u8[] = match (net.read_watched(conn, 1024, 5000i64, 1)) {
        Ok(net.Chunk(b)) => b,
        _ => { return 5; },
    };
    match (net.write(conn, msg)) { Err(_) => { return 3; }, Ok(_) => {} }
    tcp_close(conn);
    tcp_close(sock);
    return 0;
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	bin := buildFernCLI(t)

	componentPath := filepath.Join(dir, "echo.component.wasm")
	emit := exec.Command(bin,
		"-target", "wasm32-wasi",
		"-o", componentPath,
		srcPath,
	)
	var emitOut, emitErr bytes.Buffer
	emit.Stdout = &emitOut
	emit.Stderr = &emitErr
	if err := emit.Run(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\nstdout:\n%s\nstderr:\n%s", err, emitOut.String(), emitErr.String())
	}

	run := exec.Command("wasmtime", "run", "-S", "inherit-network", componentPath)
	stdout, err := run.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	var serr bytes.Buffer
	run.Stderr = &serr
	if err := run.Start(); err != nil {
		t.Fatalf("wasmtime run start: %v", err)
	}
	t.Cleanup(func() {
		if run.Process != nil {
			run.Process.Kill()
		}
		run.Wait()
	})

	port := readGuestPort(t, stdout, &serr)

	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(port)), 5*time.Second)
	if err != nil {
		t.Fatalf("dial %d: %v\nstderr:\n%s", port, err, serr.String())
	}
	defer conn.Close()

	want := "hello from the host\n"
	if _, err := conn.Write([]byte(want)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if cw, ok := conn.(*net.TCPConn); ok {
		cw.CloseWrite()
	}

	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != want {
		t.Fatalf("echo = %q; want %q (stderr=%q)", string(got), want, serr.String())
	}
	if err := run.Wait(); err != nil {
		t.Fatalf("wasmtime exit: %v\nstderr:\n%s", err, serr.String())
	}
}

func readGuestPort(t *testing.T, stdout io.Reader, serr *bytes.Buffer) int {
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
			t.Fatalf("reading the guest's port line: %v\nstderr:\n%s", r.err, serr.String())
		}
		port, err := strconv.Atoi(strings.TrimSpace(r.line))
		if err != nil || port <= 0 || port > 65535 {
			t.Fatalf("guest announced %q, want a port\nstderr:\n%s", r.line, serr.String())
		}
		return port
	case <-time.After(20 * time.Second):
		t.Fatalf("guest never announced a port\nstderr:\n%s", serr.String())
		return 0
	}
}

func TestWasmPreview2TcpServerStdoutAdapterFree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "echolog.fern")
	src := strings.Replace(`import "std/net";

function main(): i32 {
    let sock = tcp_listen(__PORT__);
    if (sock < 0) { return 1; }
    print("LISTENING");
    let conn = tcp_accept(sock);
    if (conn < 0) { return 2; }
    let msg: u8[] = match (net.read_watched(conn, 1024, 5000i64, 1)) {
        Ok(net.Chunk(b)) => b,
        _ => { return 5; },
    };
    print("GOTDATA");
    match (net.write(conn, msg)) { Err(_) => { return 3; }, Ok(_) => {} }
    tcp_close(conn);
    tcp_close(sock);
    return 0;
}
`, "__PORT__", strings.TrimSpace(itoa(port)), 1)
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	bin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}
	componentPath := filepath.Join(dir, "echolog.component.wasm")
	emit := exec.Command(bin, "-target", "wasm32-wasi", "-o", componentPath, srcPath)
	var emitOut, emitErr bytes.Buffer
	emit.Stdout = &emitOut
	emit.Stderr = &emitErr
	if err := emit.Run(); err != nil {
		t.Fatalf("fern -target wasm32-wasi (tcp+stdout): %v\nstdout:\n%s\nstderr:\n%s", err, emitOut.String(), emitErr.String())
	}
	wit, err := exec.Command("wasm-tools", "component", "wit", componentPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools component wit failed: %v\n%s", err, wit)
	}
	if !bytes.Contains(wit, []byte("wasi:cli/stdout")) {
		t.Errorf("expected wasi:cli/stdout import, got:\n%s", wit)
	}

	run := exec.Command("wasmtime", "run", "-S", "inherit-network", componentPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Start(); err != nil {
		t.Fatalf("wasmtime run start: %v", err)
	}
	t.Cleanup(func() {
		if run.Process != nil {
			run.Process.Kill()
		}
		run.Wait()
	})

	deadline := time.Now().Add(5 * time.Second)
	var conn net.Conn
	for {
		conn, err = net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer conn.Close()

	want := "tcp-with-logging\n"
	if _, err := conn.Write([]byte(want)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if cw, ok := conn.(*net.TCPConn); ok {
		cw.CloseWrite()
	}
	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != want {
		t.Fatalf("echo = %q; want %q (stderr=%q)", string(got), want, serr.String())
	}
	if err := run.Wait(); err != nil {
		t.Fatalf("wasmtime exit: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
	}
	if !bytes.Contains(sout.Bytes(), []byte("LISTENING")) || !bytes.Contains(sout.Bytes(), []byte("GOTDATA")) {
		t.Errorf("expected LISTENING + GOTDATA in server stdout, got:\n%s", sout.String())
	}
}

func runWasmtimeUDPFlaky(t *testing.T, label string, args ...string) []byte {
	t.Helper()
	const attempts = 4
	var out []byte
	var err error
	for i := 1; i <= attempts; i++ {
		out, err = exec.Command("wasmtime", args...).CombinedOutput()
		if err == nil {
			return out
		}
		t.Logf("%s: wasmtime UDP run attempt %d/%d failed (#4358 flaky wasi:sockets race), retrying: %v\n%s", label, i, attempts, err, out)
	}
	t.Fatalf("%s: wasmtime run failed after %d attempts: %v\n%s", label, attempts, err, out)
	return nil
}

func TestWasmPreview2UdpSendStdoutAdapterFree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer pc.Close()
	port := pc.LocalAddr().(*net.UDPAddr).Port

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "send.fern")
	src := fernUDPSend + "function main(): i32 {\n" +
		"    print(\"emitting metric\");\n" +
		"    if (send_datagram(net.ipv4_loopback(), " + itoa(port) + ", \"metric:1|c\")) { return 0; }\n" +
		"    return 1;\n" +
		"}\n"
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	bin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}
	componentPath := filepath.Join(dir, "send.component.wasm")
	if out, err := exec.Command(bin, "-target", "wasm32-wasi", "-o", componentPath, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi (udp+print, no adapter): %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", componentPath).CombinedOutput(); err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	out := runWasmtimeUDPFlaky(t, "udp+print", "run", "-S", "inherit-network", componentPath)
	if !bytes.Contains(out, []byte("emitting metric")) {
		t.Errorf("expected log line on stdout, got: %q", string(out))
	}
	pc.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("read datagram: %v", err)
	}
	if got := string(buf[:n]); got != "metric:1|c" {
		t.Fatalf("datagram = %q; want %q", got, "metric:1|c")
	}
}

func TestWasmPreview2UdpSendAdapterFree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer pc.Close()
	port := pc.LocalAddr().(*net.UDPAddr).Port

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "send.fern")
	src := fernUDPSend + "function main(): i32 {\n" +
		"    if (send_datagram(net.ipv4_loopback(), " + itoa(port) + ", \"ping-from-fern\")) { return 0; }\n" +
		"    return 1;\n" +
		"}\n"
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	bin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}
	componentPath := filepath.Join(dir, "send.component.wasm")
	emit := exec.Command(bin, "-target", "wasm32-wasi", "-o", componentPath, srcPath)
	if out, err := emit.CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi (udp send, no adapter): %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", componentPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}

	for i := 0; i < 10; i++ {
		run := exec.Command("wasmtime", "run", "-S", "inherit-network", componentPath)
		if out, err := run.CombinedOutput(); err != nil {
			t.Fatalf("wasmtime run (udp client, iteration %d): %v\n%s", i, err, out)
		}

		pc.SetReadDeadline(time.Now().Add(3 * time.Second))
		buf := make([]byte, 2048)
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			t.Fatalf("read datagram (iteration %d): %v", i, err)
		}
		if got := string(buf[:n]); got != "ping-from-fern" {
			t.Fatalf("datagram (iteration %d) = %q; want %q", i, got, "ping-from-fern")
		}
	}
}

func TestWasmPreview2TcpUdpAdapterFree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer pc.Close()
	udpPort := pc.LocalAddr().(*net.UDPAddr).Port

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	tcpPort := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "both.fern")
	src := fernUDPSend + `function main(): i32 {
    let s: i32 = tcp_listen(` + itoa(tcpPort) + `);
    if (s < 0) { return 1; }
    if (!send_datagram(net.ipv4_loopback(), ` + itoa(udpPort) + `, "tcp-and-udp")) {
        tcp_close(s);
        return 2;
    }
    tcp_close(s);
    return 0;
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	bin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}
	componentPath := filepath.Join(dir, "both.component.wasm")
	if out, err := exec.Command(bin, "-target", "wasm32-wasi", "-o", componentPath, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi (tcp+udp, no adapter): %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", componentPath).CombinedOutput(); err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	wit, err := exec.Command("wasm-tools", "component", "wit", componentPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools component wit: %v\n%s", err, wit)
	}
	for _, w := range []string{"wasi:sockets/tcp", "wasi:sockets/udp", "wasi:sockets/instance-network"} {
		if !bytes.Contains(wit, []byte(w)) {
			t.Errorf("expected %q import, got:\n%s", w, wit)
		}
	}

	runWasmtimeUDPFlaky(t, "tcp+udp", "run", "-S", "inherit-network", componentPath)
	pc.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("read datagram: %v", err)
	}
	if got := string(buf[:n]); got != "tcp-and-udp" {
		t.Fatalf("datagram = %q; want %q", got, "tcp-and-udp")
	}
}

func TestWasmPreview2UdpFileAdapterFree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer pc.Close()
	port := pc.LocalAddr().(*net.UDPAddr).Port

	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const want = "shipped-from-disk"
	if err := os.WriteFile(filepath.Join(root, "payload.txt"), []byte(want), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	srcPath := filepath.Join(dir, "ship.fern")
	src := fernUDPSend + `function main(): i32 {
    match (read_file("payload.txt")) {
        Ok(content) => {
            if (!send_datagram(net.ipv4_loopback(), ` + itoa(port) + `, content)) { return 2; }
        },
        Err(e) => { return 3; }
    }
    return 0;
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	bin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}
	componentPath := filepath.Join(dir, "ship.component.wasm")
	if out, err := exec.Command(bin, "-target", "wasm32-wasi", "-o", componentPath, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi (udp+files, no adapter): %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", componentPath).CombinedOutput(); err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	wit, err := exec.Command("wasm-tools", "component", "wit", componentPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools component wit: %v\n%s", err, wit)
	}
	for _, w := range []string{"wasi:sockets/udp", "wasi:filesystem/types", "wasi:filesystem/preopens"} {
		if !bytes.Contains(wit, []byte(w)) {
			t.Errorf("expected %q import, got:\n%s", w, wit)
		}
	}

	runWasmtimeUDPFlaky(t, "udp+files", "run", "-S", "inherit-network", "--dir", root+"::/", componentPath)
	pc.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("read datagram: %v", err)
	}
	if got := string(buf[:n]); got != want {
		t.Fatalf("datagram = %q; want %q", got, want)
	}
}

func TestWasmPreview2TcpFileServerAdapterFree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	want := "<!doctype html><h1>static from wasm</h1>"
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte(want), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	srcPath := filepath.Join(dir, "srv.fern")
	src := `function main(): i32 {
    let s: i32 = tcp_listen(` + itoa(port) + `);
    if (s < 0) { return 1; }
    print("file server up");
    let c: i32 = tcp_accept(s);
    if (c < 0) { return 2; }
    match (read_file_bytes("index.html")) {
        Ok(content) => { tcp_send_bytes(c, content); },
        Err(e) => { tcp_send_bytes(c, [69u8, 82u8, 82u8]); }
    }
    tcp_close(c);
    tcp_close(s);
    return 0;
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	bin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}
	componentPath := filepath.Join(dir, "srv.wasm")
	if out, err := exec.Command(bin, "-target", "wasm32-wasi", "-o", componentPath, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi (tcp file server, no adapter): %v\n%s", err, out)
	}
	wit, err := exec.Command("wasm-tools", "component", "wit", componentPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools component wit: %v\n%s", err, wit)
	}
	for _, w := range []string{"wasi:filesystem/types", "wasi:filesystem/preopens", "wasi:cli/stdout", "wasi:sockets/tcp"} {
		if !bytes.Contains(wit, []byte(w)) {
			t.Errorf("expected %q import, got:\n%s", w, wit)
		}
	}

	run := exec.Command("wasmtime", "run", "-S", "inherit-network", "--dir", root+"::/", componentPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Start(); err != nil {
		t.Fatalf("wasmtime run start: %v", err)
	}
	t.Cleanup(func() {
		if run.Process != nil {
			run.Process.Kill()
		}
		run.Wait()
	})

	deadline := time.Now().Add(5 * time.Second)
	var conn net.Conn
	for {
		conn, err = net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer conn.Close()
	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != want {
		t.Fatalf("served = %q; want %q (stderr=%q)", string(got), want, serr.String())
	}
	if err := run.Wait(); err != nil {
		t.Fatalf("wasmtime exit: %v\nstderr:\n%s", err, serr.String())
	}
	if !bytes.Contains(sout.Bytes(), []byte("file server up")) {
		t.Errorf("expected log line in server stdout, got:\n%s", sout.String())
	}
}

func TestWasmPreview2TcpFileWriteAdapterFree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}
	run := func(name, src, file, want string) {
		root := filepath.Join(dir, name)
		if err := os.Mkdir(root, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		sp := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(sp, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		comp := filepath.Join(dir, name+".wasm")
		if out, err := exec.Command(bin, "-target", "wasm32-wasi", "-o", comp, sp).CombinedOutput(); err != nil {
			t.Fatalf("fern -target wasm32-wasi (%s, no adapter): %v\n%s", name, err, out)
		}
		if out, err := exec.Command("wasm-tools", "validate", comp).CombinedOutput(); err != nil {
			t.Fatalf("validate (%s): %v\n%s", name, err, out)
		}
		if out, err := exec.Command("wasmtime", "run", "-S", "inherit-network", "--dir", root+"::/", comp).CombinedOutput(); err != nil {
			t.Fatalf("wasmtime run (%s): %v\n%s", name, err, out)
		}
		got, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if string(got) != want {
			t.Fatalf("%s: on-disk = %q; want %q", name, string(got), want)
		}
	}
	probe, _ := net.Listen("tcp", "127.0.0.1:0")
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	p := itoa(port)

	run("wsrv", `function main(): i32 {
    print("write server");
    match (write_file("access.log", "GET / 200\n")) { Err(e) => { return 3; }, Ok(_) => {} }
    let s: i32 = tcp_listen(`+p+`);
    if (s < 0) { return 1; }
    tcp_close(s);
    return 0;
}`, "access.log", "GET / 200\n")

	run("asrv", `function main(): i32 {
    match (open_appender("a.log")) {
        Ok(w) => { w.write("entry\n"); w.close(); },
        Err(e) => { return 3; }
    }
    let s: i32 = tcp_listen(`+p+`);
    if (s < 0) { return 1; }
    tcp_close(s);
    return 0;
}`, "a.log", "entry\n")
}

func TestWasmPreview2TcpStdinAdapterFree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}
	probe, _ := net.Listen("tcp", "127.0.0.1:0")
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "stdin.fern")
	src := `import "std/io";

function main(): i32 {
    match (io.read_line(stdin())) {
        Ok(Some(line)) => { print(line); },
        _ => { print("no-input"); }
    }
    let s: i32 = tcp_listen(` + itoa(port) + `);
    if (s < 0) { return 1; }
    tcp_close(s);
    return 0;
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	bin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}
	componentPath := filepath.Join(dir, "stdin.wasm")
	if out, err := exec.Command(bin, "-target", "wasm32-wasi", "-o", componentPath, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi (tcp+stdin, no adapter): %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", componentPath).CombinedOutput(); err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	run := exec.Command("wasmtime", "run", "-S", "inherit-network", componentPath)
	run.Stdin = strings.NewReader("config-line\n")
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (tcp+stdin): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("config-line")) {
		t.Errorf("expected the stdin line echoed on stdout, got: %q", string(out))
	}
}

func TestWasmPreview2SocketCliExtrasAdapterFree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}
	build := func(name, src string) string {
		sp := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(sp, []byte(src), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		comp := filepath.Join(dir, name+".wasm")
		if out, err := exec.Command(bin, "-target", "wasm32-wasi", "-o", comp, sp).CombinedOutput(); err != nil {
			t.Fatalf("fern -target wasm32-wasi (%s, no adapter): %v\n%s", name, err, out)
		}
		if out, err := exec.Command("wasm-tools", "validate", comp).CombinedOutput(); err != nil {
			t.Fatalf("validate (%s): %v\n%s", name, err, out)
		}
		return comp
	}

	tcpProbe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	tcpPort := tcpProbe.Addr().(*net.TCPAddr).Port
	tcpProbe.Close()
	tcpComp := build("tcpx", `function main(): i32 {
    print("starting");
    let t: i64 = now_ns();
    match (env("MODE")) { Some(_) => {}, None => {} }
    let s: i32 = tcp_listen(`+itoa(tcpPort)+`);
    if (s < 0) { return 1; }
    tcp_close(s);
    if (t > 0) { return 0; }
    return 2;
}`)
	if out, err := exec.Command("wasmtime", "run", "-S", "inherit-network", "--env", "MODE=x", tcpComp).CombinedOutput(); err != nil {
		t.Fatalf("wasmtime run (tcp+now+env+print): %v\n%s", err, out)
	}

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer pc.Close()
	udpPort := pc.LocalAddr().(*net.UDPAddr).Port
	udpComp := build("udpx", fernUDPSend+`function main(): i32 {
    let host: string = "127.0.0.1";
    match (env("TARGET")) { Some(v) => { host = v; }, None => {} }
    let t: i64 = now_ns();
    match (net.ipv4_parse(host)) {
        Some(ip) => { if (send_datagram(ip, `+itoa(udpPort)+`, "telemetry") && t > 0) { return 0; } },
        None => {},
    }
    return 1;
}`)
	_ = runWasmtimeUDPFlaky(t, "udp+env+now", "run", "-S", "inherit-network", "--env", "TARGET=127.0.0.1", udpComp)
	pc.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("read datagram: %v", err)
	}
	if got := string(buf[:n]); got != "telemetry" {
		t.Fatalf("datagram = %q; want %q", got, "telemetry")
	}
}

const fernUDPSend = `import "std/net";
import "std/string";

function send_datagram(ip: net.IpAddr, port: i32, data: string): boolean {
    let sock: i32 = match (net.udp_socket(net.socket_addr(net.ipv4_unspecified(), 0))) {
        Ok(s) => s,
        Err(_) => { return false; },
    };
    let sent: boolean = false;
    match (net.send_to(sock, data.bytes(), net.socket_addr(ip, port))) {
        Ok(n) => { sent = n > 0; },
        Err(_) => {},
    }
    match (net.close(sock)) { _ => {} }
    return sent;
}

`

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func TestWasmPreview2HttpHandler(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "router.fern")
	src := `
import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/hello") {
        return http.ok("world");
    }
    if (req.method == "POST") {
        match (req.body_string()) {
            Ok(text) => { return http.ok(text); },
            Err(e) => { return e.to_response(); }
        }
    }
    return http.text(404, "not found");
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	bin := buildFernCLI(t)

	componentPath := filepath.Join(dir, "router.component.wasm")
	emit := exec.Command(bin,
		"-target", "wasm32-wasi-http",
		"-o", componentPath,
		srcPath,
	)
	var emitOut, emitErr bytes.Buffer
	emit.Stdout = &emitOut
	emit.Stderr = &emitErr
	if err := emit.Run(); err != nil {
		t.Fatalf("fern -target wasm32-wasi-http: %v\nstdout:\n%s\nstderr:\n%s", err, emitOut.String(), emitErr.String())
	}

	addr := net.JoinHostPort("127.0.0.1", itoa(port))
	run := exec.Command("wasmtime", "serve", "--addr", addr, componentPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Start(); err != nil {
		t.Fatalf("wasmtime serve start: %v", err)
	}
	t.Cleanup(func() {
		if run.Process != nil {
			run.Process.Kill()
		}
		run.Wait()
	})

	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(5 * time.Second)
	var resp *http.Response
	for {
		resp, err = client.Get("http://" + addr + "/hello")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial /hello: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if resp.StatusCode != 200 {
		t.Errorf("/hello status = %d; want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "world" {
		t.Errorf("/hello body = %q; want %q", string(body), "world")
	}

	resp, err = client.Get("http://" + addr + "/missing")
	if err != nil {
		t.Fatalf("get /missing: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Errorf("/missing status = %d; want 404", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "not found" {
		t.Errorf("/missing body = %q; want %q", string(body), "not found")
	}

	want := "echo me back"
	resp, err = client.Post("http://"+addr+"/echo", "text/plain", strings.NewReader(want))
	if err != nil {
		t.Fatalf("post /echo: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("POST status = %d; want 200", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != want {
		t.Errorf("POST body echo = %q; want %q", string(body), want)
	}
}

func TestWasmPreview2HttpHandlerResponseHeaders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "router.fern")
	src := `
import "std/headers";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    let h: HeaderMap = headers.header_map_new();
    h = h.set("x-served-by", "fern");
    h = h.set("content-type", "text/plain");
    return HttpResponse { status: 201, body: BodyText("ok"), headers: h, trailers: headers.header_map_new(), background: None };
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	bin := buildFernCLI(t)

	componentPath := filepath.Join(dir, "router.component.wasm")
	emit := exec.Command(bin, "-target", "wasm32-wasi-http", "-o", componentPath, srcPath)
	var emitOut, emitErr bytes.Buffer
	emit.Stdout = &emitOut
	emit.Stderr = &emitErr
	if err := emit.Run(); err != nil {
		t.Fatalf("fern -target wasm32-wasi-http: %v\nstdout:\n%s\nstderr:\n%s", err, emitOut.String(), emitErr.String())
	}

	addr := net.JoinHostPort("127.0.0.1", itoa(port))
	run := exec.Command("wasmtime", "serve", "--addr", addr, componentPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Start(); err != nil {
		t.Fatalf("wasmtime serve start: %v", err)
	}
	t.Cleanup(func() {
		if run.Process != nil {
			run.Process.Kill()
		}
		run.Wait()
	})

	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(5 * time.Second)
	var resp *http.Response
	for {
		resp, err = client.Get("http://" + addr + "/")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial /: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if resp.StatusCode != 201 {
		t.Errorf("status = %d; want 201\nstderr:\n%s", resp.StatusCode, serr.String())
	}
	if got := resp.Header.Get("x-served-by"); got != "fern" {
		t.Errorf("x-served-by = %q; want %q", got, "fern")
	}
	if got := resp.Header.Get("content-type"); got != "text/plain" {
		t.Errorf("content-type = %q; want %q", got, "text/plain")
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok" {
		t.Errorf("body = %q; want %q", string(body), "ok")
	}
}

func TestWasmPreview2HttpHandlerRequestHeaders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "router.fern")
	src := `
import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    match (req.headers.get("x-echo")) {
        Some(v) => { return http.ok(v); },
        None => { return http.text(400, "no x-echo"); },
    }
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	bin := buildFernCLI(t)

	componentPath := filepath.Join(dir, "router.component.wasm")
	emit := exec.Command(bin, "-target", "wasm32-wasi-http", "-o", componentPath, srcPath)
	var emitOut, emitErr bytes.Buffer
	emit.Stdout = &emitOut
	emit.Stderr = &emitErr
	if err := emit.Run(); err != nil {
		t.Fatalf("fern -target wasm32-wasi-http: %v\nstdout:\n%s\nstderr:\n%s", err, emitOut.String(), emitErr.String())
	}

	addr := net.JoinHostPort("127.0.0.1", itoa(port))
	run := exec.Command("wasmtime", "serve", "--addr", addr, componentPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Start(); err != nil {
		t.Fatalf("wasmtime serve start: %v", err)
	}
	t.Cleanup(func() {
		if run.Process != nil {
			run.Process.Kill()
		}
		run.Wait()
	})

	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(5 * time.Second)
	wants := []string{"first", "second-value", "third"}
	for i, want := range wants {
		var resp *http.Response
		for {
			req, _ := http.NewRequest("GET", "http://"+addr+"/", nil)
			req.Header.Set("x-echo", want)
			resp, err = client.Do(req)
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("request %d: %v\nstderr:\n%s", i, err, serr.String())
			}
			time.Sleep(50 * time.Millisecond)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("request %d (x-echo=%q): status = %d; want 200\nstderr:\n%s", i, want, resp.StatusCode, serr.String())
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != want {
			t.Errorf("request %d body = %q; want %q", i, string(body), want)
		}
	}
}

func TestWasmPreview2HttpHandlerLoggingAdapterFree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "logger.fern")
	src := `
import "std/http";
import "std/platform";
import "std/serve";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    plat.log(f"LOGLINE {req.method} {req.path}");
    return http.ok("logged");
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	bin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}

	componentPath := filepath.Join(dir, "logger.component.wasm")
	emit := exec.Command(bin, "-target", "wasm32-wasi-http", "-o", componentPath, srcPath)
	var emitOut, emitErr bytes.Buffer
	emit.Stdout = &emitOut
	emit.Stderr = &emitErr
	if err := emit.Run(); err != nil {
		t.Fatalf("fern -target wasm32-wasi-http (logging, no adapter): %v\nstdout:\n%s\nstderr:\n%s", err, emitOut.String(), emitErr.String())
	}
	wit, err := exec.Command("wasm-tools", "component", "wit", componentPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools component wit failed: %v\n%s", err, wit)
	}
	if !bytes.Contains(wit, []byte("wasi:cli/stderr")) {
		t.Errorf("expected wasi:cli/stderr import in the logging handler, got:\n%s", wit)
	}

	addr := net.JoinHostPort("127.0.0.1", itoa(port))
	run := exec.Command("wasmtime", "serve", "--addr", addr, componentPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Start(); err != nil {
		t.Fatalf("wasmtime serve start: %v", err)
	}
	t.Cleanup(func() {
		if run.Process != nil {
			run.Process.Kill()
		}
		run.Wait()
	})

	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(5 * time.Second)
	var resp *http.Response
	for {
		resp, err = client.Get("http://" + addr + "/ping")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial /ping: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if resp.StatusCode != 200 {
		t.Errorf("/ping status = %d; want 200 (stderr=%q)", resp.StatusCode, serr.String())
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "logged" {
		t.Errorf("/ping body = %q; want %q", string(body), "logged")
	}
	time.Sleep(200 * time.Millisecond)
	if !bytes.Contains(serr.Bytes(), []byte("LOGLINE GET /ping")) {
		t.Errorf("expected log line in server stderr, got:\n%s", serr.String())
	}
}

func TestWasmPreview2HttpHandlerClockAdapterFree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "clock.fern")
	src := `
import "std/http";
import "std/platform";
import "std/serve";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    let t: i64 = plat.now_ms();
    let m: i64 = plat.elapsed_ns();
    let r: i32 = plat.random_i32();
    if (t > 0) { return http.ok("clock-ok"); }
    return http.ok("no-clock");
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	bin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}
	componentPath := filepath.Join(dir, "clock.component.wasm")
	if out, err := exec.Command(bin, "-target", "wasm32-wasi-http", "-o", componentPath, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi-http (clock handler, no adapter): %v\n%s", err, out)
	}
	wit, err := exec.Command("wasm-tools", "component", "wit", componentPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools component wit: %v\n%s", err, wit)
	}
	if !bytes.Contains(wit, []byte("wasi:clocks")) {
		t.Errorf("expected wasi:clocks import, got:\n%s", wit)
	}

	addr := net.JoinHostPort("127.0.0.1", itoa(port))
	run := exec.Command("wasmtime", "serve", "--addr", addr, componentPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Start(); err != nil {
		t.Fatalf("wasmtime serve start: %v", err)
	}
	t.Cleanup(func() {
		if run.Process != nil {
			run.Process.Kill()
		}
		run.Wait()
	})

	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(5 * time.Second)
	var resp *http.Response
	for {
		resp, err = client.Get("http://" + addr + "/")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial: %v\nstderr:\n%s", err, serr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "clock-ok" {
		t.Fatalf("status=%d body=%q; want 200 \"clock-ok\" (stderr=%q)", resp.StatusCode, string(body), serr.String())
	}

	envSrc := `
import "std/http";
import "std/platform";
import "std/serve";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    match (plat.env("X")) { Some(_) => {}, None => {} }
    return http.ok("e");
}
`
	envPath := filepath.Join(dir, "env.fern")
	if err := os.WriteFile(envPath, []byte(envSrc), 0o644); err != nil {
		t.Fatalf("write env src: %v", err)
	}
	out, err := exec.Command(bin, "-target", "wasm32-wasi-http", "-o", filepath.Join(dir, "env.wasm"), envPath).CombinedOutput()
	if err == nil {
		t.Errorf("expected env handler to reject (proxy world has no environment), but it composed")
	} else if !bytes.Contains(out, []byte("E066")) || !bytes.Contains(out, []byte("`env`")) {
		t.Errorf("expected an E066 env-capability rejection, got:\n%s", out)
	}
}

func TestWasmPreview2HttpHandlerAdapterFree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "router.fern")
	src := `
import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/hello") {
        return http.ok("world");
    }
    if (req.method == "POST") {
        match (req.body_string()) {
            Ok(text) => { return http.ok(text); },
            Err(e) => { return e.to_response(); }
        }
    }
    return http.text(404, "not found");
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	bin := buildFernCLI(t)

	componentPath := filepath.Join(dir, "router.component.wasm")
	emit := exec.Command(bin, "-target", "wasm32-wasi-http", "-o", componentPath, srcPath)
	var emitOut, emitErr bytes.Buffer
	emit.Stdout = &emitOut
	emit.Stderr = &emitErr
	if err := emit.Run(); err != nil {
		t.Fatalf("fern -target wasm32-wasi-http (no adapter): %v\nstdout:\n%s\nstderr:\n%s", err, emitOut.String(), emitErr.String())
	}
	if out, err := exec.Command("wasm-tools", "validate", componentPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}

	addr := net.JoinHostPort("127.0.0.1", itoa(port))
	run := exec.Command("wasmtime", "serve", "--addr", addr, componentPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Start(); err != nil {
		t.Fatalf("wasmtime serve start: %v", err)
	}
	t.Cleanup(func() {
		if run.Process != nil {
			run.Process.Kill()
		}
		run.Wait()
	})

	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(5 * time.Second)
	var resp *http.Response
	for {
		resp, err = client.Get("http://" + addr + "/hello")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial /hello: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if resp.StatusCode != 200 {
		t.Errorf("/hello status = %d; want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "world" {
		t.Errorf("/hello body = %q; want %q (stderr=%q)", string(body), "world", serr.String())
	}

	resp, err = client.Get("http://" + addr + "/missing")
	if err != nil {
		t.Fatalf("get /missing: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Errorf("/missing status = %d; want 404", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "not found" {
		t.Errorf("/missing body = %q; want %q", string(body), "not found")
	}

	want := "echo me back"
	resp, err = client.Post("http://"+addr+"/echo", "text/plain", strings.NewReader(want))
	if err != nil {
		t.Fatalf("post /echo: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("POST status = %d; want 200", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != want {
		t.Errorf("POST body echo = %q; want %q", string(body), want)
	}
}

func TestWasmPreview2HttpHandlerPlatformCapabilities(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "caps.fern")
	src := `
import "std/http";
import "std/serve";
import "std/platform" as platform;
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    plat.log("handled " + req.path);
    let floor: i64 = 1600000000000;
    if (plat.now_ms() < floor) {
        return http.text(500, "clock");
    }
    if (plat.elapsed_ns() < 0) {
        return http.text(500, "monotonic");
    }
    if (plat.random_i32() == 0 && plat.random_i32() == 0 && plat.random_i32() == 0) {
        return http.text(500, "random");
    }
    return http.ok("caps-ok");
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	bin := buildFernCLI(t)

	componentPath := filepath.Join(dir, "caps.component.wasm")
	emit := exec.Command(bin, "-target", "wasm32-wasi-http", "-o", componentPath, srcPath)
	var emitOut, emitErr bytes.Buffer
	emit.Stdout = &emitOut
	emit.Stderr = &emitErr
	if err := emit.Run(); err != nil {
		t.Fatalf("fern -target wasm32-wasi-http: %v\nstdout:\n%s\nstderr:\n%s", err, emitOut.String(), emitErr.String())
	}

	addr := net.JoinHostPort("127.0.0.1", itoa(port))
	run := exec.Command("wasmtime", "serve", "--addr", addr, componentPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Start(); err != nil {
		t.Fatalf("wasmtime serve start: %v", err)
	}
	t.Cleanup(func() {
		if run.Process != nil {
			run.Process.Kill()
		}
		run.Wait()
	})

	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(5 * time.Second)
	var resp *http.Response
	for {
		resp, err = client.Get("http://" + addr + "/caps")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial /caps: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("/caps status = %d, body %q; want 200 (the body names the capability that failed)", resp.StatusCode, string(body))
	}
	if string(body) != "caps-ok" {
		t.Errorf("/caps body = %q; want %q", string(body), "caps-ok")
	}

	logged := false
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if strings.Contains(serr.String(), "handled /caps") {
			logged = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !logged {
		t.Errorf("plat.log line missing from wasmtime stderr:\n%s", serr.String())
	}
}
