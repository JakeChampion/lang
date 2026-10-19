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

// #9853 exit criterion A, count half: what the serve loop allocates per
// hello request on a keep-alive connection, through the self-host compiler.
// docs/NET-P0-MESSAGE-LAYER-PLAN.md §6 took it to zero on x86-64 over
// 2,000 requests; the gate spans 100k there and holds the same zero on
// arm64 under qemu and on wasm under wasmtime over 10k each.
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

// The same zero with the loop waiting on an io_uring (`FERN_IO_URING=1`,
// docs/NET-P7-IO-URING-PLAN.md): a wait re-arms its polls in the ring the
// driver mapped when it was made, so a request costs the ring nothing.
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

// The wasm leg binds a port the test picked, since wasmtime hands a guest
// no inherited listener, and runs under `-S inherit-network`.
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

// ServeAllocsH2Ceiling bounds what the loop allocates per hello request
// on one HTTP/2 connection, a ratchet like the HTTP/1.1 zero above, as a
// ceiling on the mean since the count varies from request to request with
// how the client's frames coalesce into reads. The mean reads 20.0 on
// x86-64 (from 99.0 on the first reading; docs/NET-P6-HTTP2-PLAN.md slice
// 4 records each cut and what the 20 are); the CI runner reads a few
// lower, as the note on serveAllocsH2Drift says.
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

// ServeAllocsTLSCeiling bounds what the serve loop and the TLS 1.3 record
// layer allocate per hello request on one keep-alive TLS connection
// (`Config.with_tls`), a ratchet like the HTTP/2 one: the connection's
// handshake sits in the /count bracketing's intercept, so the mean is what
// one record pair costs — a request record opened in, a response record
// sealed out. The first reading was 56.0 on x86-64 (docs/benchmarks/
// net-h2-tls-2026-10-18.md); 55.0 with `record.open` handing the decrypted
// buffer back shortened in place rather than copying the content out; 53.0
// here with the decrypted application data moved into the step and then
// into the read buffer rather than copied at each layer; 49.0 with a byte
// slice's view descriptor in the frame (#12780) and the session taken out of
// its enum slot rather than retained (#12781); 43.0 on arm64-darwin with
// the methods nothing outside the bodies calls returning their variants
// paired (sempair), where 49.0 was measured before. The count's small
// variation between runs (how the client's records coalesce into reads) is
// why it is a ceiling on the mean; a mean serveAllocsH2Drift or more under
// it fails, to lower it. Most of the remaining count is the per-record
// rebuild of the session and handshake state (docs/benchmarks/
// net-h2-tls-2026-10-18.md, "Left").
const ServeAllocsTLSCeiling = 43

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

// ServeAllocsH3Ceiling bounds allocations per hello request over one HTTP/3
// connection. The 100,000-request measurement and ratchet are recorded in
// docs/benchmarks/net-h3-2026-10-17.md; 265.2 on arm64 since elements are
// re-read where their record dies; 255.2 here and 256.2 on the CI runner
// on x86-64 with the request target handed back when already normal and
// the handler started from its request rather than a framed record built
// around it; 247.2 here with a byte slice's view descriptor in the frame
// (#12780) and payloads taken out of enum slots (#12781); 230.33 on
// arm64-darwin and 231.19 on the x86-64 runner with methods' variant returns
// paired, where 247.33 was measured before.
const ServeAllocsH3Ceiling = 232

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

// ServeFetchAllocsCeiling bounds what a handler making two requests
// through `plat.http` allocates per request, the serve loop's share
// included (e2eharness.ServeFetchAllocsServerSource, the nightly two-fetch
// profile; docs/benchmarks/net-twofetch-2026-10-17.md and
// docs/benchmarks/net-fetch-client-2026-10-18.md). 91.00 on arm64-darwin
// with methods' variant returns paired, the transport read's Ok among them,
// where 99.00 was measured before, as on the x86-64 runner.
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
