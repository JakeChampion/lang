package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestDnsExchangeX86_64(t *testing.T) {
	_, runner := x86_64Tooling(t)
	fern := buildLangBinForInterp(t)
	stdlib, err := filepath.Abs("../../stdlib")
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

func TestDnsPairX86_64(t *testing.T) {
	_, runner := x86_64Tooling(t)
	fern := buildLangBinForInterp(t)
	stdlib, err := filepath.Abs("../../stdlib")
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

func TestDnsPairInterp(t *testing.T) {
	ns := e2eharness.StartFakeNameserver(t, e2eharness.FakeNameserverPair)
	out, code := runInterpExitCode(t, e2eharness.DnsPairSource(ns.Port))
	e2eharness.CheckDnsPair(t, ns, out, code)
}

func TestDnsPairInterpReadsTheReadySocket(t *testing.T) {
	ns := e2eharness.StartFakeNameserver(t, e2eharness.FakeNameserverAAAAOnly)
	bin := buildLangBinForInterp(t)
	p := filepath.Join(t.TempDir(), "pair.fern")
	if err := os.WriteFile(p, []byte(e2eharness.DnsPairSource(ns.Port)), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-interp", p)
	var out bytes.Buffer
	cmd.Stdout = &out
	timedOut, _ := runBounded(cmd, 30*time.Second)
	if timedOut {
		t.Fatalf("the paired wait did not return within 30 s: it is blocked on the socket whose reply never comes")
	}
	if got := strings.TrimSpace(out.String()); got != "2001:db8::1" || cmd.ProcessState.ExitCode() != 0 {
		t.Fatalf("exit %d, stdout %q; want exit 0 and the AAAA address alone", cmd.ProcessState.ExitCode(), out.String())
	}
}

func compileDnsX86(t *testing.T, src string) (string, int) {
	t.Helper()
	_, runner := x86_64Tooling(t)
	fern := buildLangBinForInterp(t)
	stdlib, err := filepath.Abs("../../stdlib")
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

func TestDnsNat64X86_64(t *testing.T) {
	ns := e2eharness.StartFakeNameserver(t, e2eharness.FakeNameserverNat64)
	out, code := compileDnsX86(t, e2eharness.DnsNat64Source(ns.Port))
	e2eharness.CheckDnsNat64(t, out, code)
}

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

func TestDnsDialInterp(t *testing.T) {
	port := e2eharness.StartEchoListener(t)
	out, code := runInterpExitCode(t, e2eharness.DnsDialSource("127.0.0.2", port))
	e2eharness.CheckDnsDial(t, out, code)
}
