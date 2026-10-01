package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// std/dns's exchange against a nameserver on the loopback interface
// (#9855): a reply over UDP, a truncated reply that sends the query again
// over TCP, and a server that never answers, reported as the silence it
// is once the timeout passes.
func TestDnsExchangeX86_64(t *testing.T) {
	_, runner := x86_64Tooling(t)
	fern := buildLangBinForInterp(t)
	stdlib, err := filepath.Abs("../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		mode e2eharness.FakeNameserverMode
	}{
		{"udp", e2eharness.FakeNameserverAnswer},
		{"tcp-retry", e2eharness.FakeNameserverTruncate},
		{"silent", e2eharness.FakeNameserverSilent},
	} {
		t.Run(c.name, func(t *testing.T) {
			ns := e2eharness.StartFakeNameserver(t, c.mode)
			dir := t.TempDir()
			src := filepath.Join(dir, "lookup.fern")
			if err := os.WriteFile(src, []byte(e2eharness.DnsExchangeSource(ns.Port)), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "lookup")
			if out, err := exec.Command(fern, "-target", "x86-64-linux", "-o", bin, src, stdlib).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			cmd := runX86_64Bin(runner, bin)
			out, _ := cmd.Output()
			e2eharness.CheckDnsExchange(t, c.mode, ns, string(out), cmd.ProcessState.ExitCode())
		})
	}
}

// TestDnsPairX86_64 is std/dns's paired lookup against a nameserver that
// answers the A query only once the AAAA query has arrived: the two go
// out together, so both come back.
func TestDnsPairX86_64(t *testing.T) {
	_, runner := x86_64Tooling(t)
	fern := buildLangBinForInterp(t)
	stdlib, err := filepath.Abs("../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	ns := e2eharness.StartFakeNameserver(t, e2eharness.FakeNameserverPair)
	dir := t.TempDir()
	src := filepath.Join(dir, "pair.fern")
	if err := os.WriteFile(src, []byte(e2eharness.DnsPairSource(ns.Port)), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "pair")
	if out, err := exec.Command(fern, "-target", "x86-64-linux", "-o", bin, src, stdlib).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	cmd := runX86_64Bin(runner, bin)
	out, _ := cmd.Output()
	e2eharness.CheckDnsPair(t, ns, string(out), cmd.ProcessState.ExitCode())
}

// TestDnsPairInterp is TestDnsPairX86_64 under the interpreter, whose poll
// is a stub: the paired wait tries every pending socket in turn there, so
// the AAAA reply that arrives first is read while the A query waits.
func TestDnsPairInterp(t *testing.T) {
	ns := e2eharness.StartFakeNameserver(t, e2eharness.FakeNameserverPair)
	out, code := runInterpExitCode(t, e2eharness.DnsPairSource(ns.Port))
	e2eharness.CheckDnsPair(t, ns, out, code)
}

// compileDnsX86 compiles a std/dns program for x86-64 and returns its
// stdout and exit code.
func compileDnsX86(t *testing.T, src string) (string, int) {
	t.Helper()
	_, runner := x86_64Tooling(t)
	fern := buildLangBinForInterp(t)
	stdlib, err := filepath.Abs("../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	if out, err := exec.Command(fern, "-target", "x86-64-linux", "-o", bin, path, stdlib).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	cmd := runX86_64Bin(runner, bin)
	out, _ := cmd.Output()
	return string(out), cmd.ProcessState.ExitCode()
}

// TestDnsNat64X86_64 reads the NAT64 prefix a nameserver reveals through
// ipv4only.arpa.
func TestDnsNat64X86_64(t *testing.T) {
	ns := e2eharness.StartFakeNameserver(t, e2eharness.FakeNameserverNat64)
	out, code := compileDnsX86(t, e2eharness.DnsNat64Source(ns.Port))
	e2eharness.CheckDnsNat64(t, out, code)
}

// TestDnsDialX86_64 is the Happy Eyeballs race: an address that refuses
// at once hands its turn to the loopback listener without waiting out
// the attempt delay, and one whose packets go nowhere (TEST-NET-1, or
// unreachable outright on a host without a route) is overtaken by the
// loopback attempt that starts beside it after the delay.
func TestDnsDialX86_64(t *testing.T) {
	for _, c := range []struct{ name, first string }{
		{"refused-first", "127.0.0.2"},
		{"blackhole-first", "192.0.2.1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			port := e2eharness.StartEchoListener(t)
			start := time.Now()
			out, code := compileDnsX86(t, e2eharness.DnsDialSource(c.first, port))
			e2eharness.CheckDnsDial(t, out, code)
			if took := time.Since(start); took > 4*time.Second {
				t.Errorf("the dial took %v; the loopback attempt should have won within the attempt delay", took)
			}
		})
	}
}

// TestDnsDialInterp is the refused-first race under the interpreter, whose
// connect blocks until it ends, so only an address that refuses at once
// is a sound first candidate there.
func TestDnsDialInterp(t *testing.T) {
	port := e2eharness.StartEchoListener(t)
	out, code := runInterpExitCode(t, e2eharness.DnsDialSource("127.0.0.2", port))
	e2eharness.CheckDnsDial(t, out, code)
}
