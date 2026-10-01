package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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
