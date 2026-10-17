package e2ecompiler

import (
	"bytes"
	"fmt"
	"net"
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
// on one HTTP/2 connection (docs/NET-P6-HTTP2-PLAN.md slice 4), a ratchet
// like the HTTP/1.1 zero above, as a ceiling on the mean since the count
// varies from request to request: the frames of a request and its
// response, the HPACK blocks, the stream's slot and the request handed
// over. The first readings were 99.0 to 100.0 on x86-64; with the state
// machine updating its connection in place, the HPACK decode threaded
// through one record and a response's fields written straight into the
// encoder, 72.0 to 72.4 here; with the loop closing an incomplete
// preface at EOF, an arm that hands the table on, 60.0 (the loop's
// splices unchanged, so the plan's doing: the table through the HTTP/2
// turn no longer copied); the CI runner reads a few lower, as the note on
// serveAllocsH2Drift says.
const ServeAllocsH2Ceiling = 62

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

// ServeAllocsH3Ceiling bounds what the loop allocates per hello request
// on one HTTP/3 connection (docs/NET-P8-HTTP3-PLAN.md slice 10), a ratchet
// like the HTTP/2 one: the datagram's packet opened and its frames read,
// the stream table and the HTTP/3 session stepped, the request handed
// over, the response's fields encoded, and its packet planned and sealed.
// The first reading was 632 on arm64-darwin with the stream table rebuilt
// on every packet; with idle streams left alone and the loop flushing a
// connection once per burst, 421 there and 423.25 on x86-64; with the
// varints and headers read as scalars, 387.25; with the stream table
// handed back bare through its operations and its filling chain, done
// streams retired lazily and byte ranges copied whole, 348.72; with the
// connection, QPACK's decoder, the session and the datagram planner handed
// back bare, 309.03; with a byte kernel's result known to be fresh
// (#12552), 297.19.
const ServeAllocsH3Ceiling = 303

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
