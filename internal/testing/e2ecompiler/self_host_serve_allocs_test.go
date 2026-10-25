package e2ecompiler

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostServeAllocsPerRequest(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ServeAllocsServerSource(0, e2eharness.ServeAllocsRounds)), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1")
	cmd := runX86_64Bin(cli.runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckServeAllocs(t, addr, 0, e2eharness.ServeAllocsRounds)
}

func TestSelfHostServeAllocsPerRequestRing(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ServeAllocsServerSource(0, e2eharness.ServeAllocsRounds)), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1")
	cmd := runX86_64Bin(cli.runner, bin)
	cmd.Env = append(os.Environ(), "FERN_IO_URING=1")
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckServeAllocs(t, addr, 0, e2eharness.ServeAllocsRounds)
}

func TestSelfHostServeAllocsPerRequestArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ServeAllocsServerSource(0, e2eharness.ServeAllocsRoundsEmulated)), 0o644); err != nil {
		t.Fatal(err)
	}
	addr, _ := e2eharness.StartInheritedServer(t, runArm64Bin(qemu, cli.arm64Binary(t, src, "FERN_STRICT_IR=1")))
	e2eharness.CheckServeAllocs(t, addr, 0, e2eharness.ServeAllocsRoundsEmulated)
}

func TestSelfHostServeAllocsPerRequestWasm(t *testing.T) {
	wasmtime := e2eharness.Wasmtime(t)
	cli := buildSelfHostCLI(t)
	port := freeTCPPort(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ServeAllocsServerSource(port, e2eharness.ServeAllocsRoundsEmulated)), 0o644); err != nil {
		t.Fatal(err)
	}
	run := exec.Command(wasmtime, "run", "-S", "inherit-network", cli.wasmComponent(t, src, "FERN_STRICT_IR=1"))
	var out bytes.Buffer
	run.Stdout, run.Stderr = &out, &out
	if err := run.Start(); err != nil {
		t.Fatalf("wasmtime run: %v", err)
	}
	t.Cleanup(func() {
		_ = run.Process.Kill()
		_ = run.Wait()
		if t.Failed() {
			t.Logf("server output:\n%s", out.String())
		}
	})
	e2eharness.CheckServeAllocs(t, fmt.Sprintf("127.0.0.1:%d", port), 0, e2eharness.ServeAllocsRoundsEmulated)
}

const ServeAllocsH2Ceiling = 21

func TestSelfHostServeAllocsPerRequestH2(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ServeAllocsH2ServerSource(0, e2eharness.ServeAllocsRounds)), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1")
	addr, _ := e2eharness.StartInheritedServer(t, runX86_64Bin(cli.runner, bin))
	e2eharness.CheckServeAllocsH2(t, addr, ServeAllocsH2Ceiling, e2eharness.ServeAllocsRounds)
}

const ServeAllocsTLSCeiling = 17

func TestSelfHostServeAllocsPerRequestTLS(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	certPEM, keyPEM, pool := e2eharness.TLSAllocsCert(t)
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ServeAllocsTLSServerSource(0, e2eharness.ServeAllocsRounds, certPath, keyPath)), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1")
	addr, _ := e2eharness.StartInheritedServer(t, runX86_64Bin(cli.runner, bin))
	e2eharness.CheckServeAllocsTLS(t, addr, ServeAllocsTLSCeiling, e2eharness.ServeAllocsRounds, pool)
}

const ServeAllocsH3Ceiling = 72

const ServeAllocsH3MuxCeiling = 39

const ServeAllocsH3MuxConcurrency = 64

const ServeAllocsH3MuxMinPeak = ServeAllocsH3MuxConcurrency * 3 / 4

func TestSelfHostServeAllocsPerRequestH3(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ServeAllocsH3ServerSource(0, e2eharness.ServeAllocsRounds)), 0o644); err != nil {
		t.Fatal(err)
	}
	clientSrc := filepath.Join(dir, "client.fern")
	if err := os.WriteFile(clientSrc, []byte(e2eharness.ServeAllocsH3ClientSource), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1")
	client := cli.x86Binary(t, clientSrc, "FERN_STRICT_IR=1")
	addr, _ := e2eharness.StartInheritedServer(t, runX86_64Bin(cli.runner, bin))
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	e2eharness.CheckServeAllocsH3(t, addr, runX86_64Bin(cli.runner, client, port, strconv.Itoa(e2eharness.ServeAllocsRounds)), ServeAllocsH3Ceiling, e2eharness.ServeAllocsRounds)
}

func TestSelfHostServeAllocsPerRequestH3Mux(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ServeAllocsH3ServerSource(0, e2eharness.ServeAllocsRounds)), 0o644); err != nil {
		t.Fatal(err)
	}
	clientSrc := filepath.Join(dir, "client.fern")
	if err := os.WriteFile(clientSrc, []byte(e2eharness.ServeAllocsH3MuxClientSource), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1")
	client := cli.x86Binary(t, clientSrc, "FERN_STRICT_IR=1")
	addr, _ := e2eharness.StartInheritedServer(t, runX86_64Bin(cli.runner, bin))
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	run := runX86_64Bin(cli.runner, client, port, strconv.Itoa(e2eharness.ServeAllocsRounds), strconv.Itoa(ServeAllocsH3MuxConcurrency))
	e2eharness.CheckServeAllocsH3Mux(t, addr, run, ServeAllocsH3MuxCeiling, e2eharness.ServeAllocsRounds, ServeAllocsH3MuxMinPeak)
}

const ServeFetchAllocsCeiling = 91

func TestSelfHostServeAllocsPerRequestTwoFetch(t *testing.T) {
	cli := buildSelfHostCLI(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "upstream")
	}))
	t.Cleanup(up.Close)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ServeFetchAllocsServerSource(0, e2eharness.ServeAllocsRoundsEmulated)), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(cli.runner, cli.x86Binary(t, src, "FERN_STRICT_IR=1"))
	cmd.Env = append(os.Environ(), "http_proxy=http://u@"+up.Listener.Addr().String())
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckServeFetchAllocs(t, addr, ServeFetchAllocsCeiling, e2eharness.ServeAllocsRoundsEmulated)
}
