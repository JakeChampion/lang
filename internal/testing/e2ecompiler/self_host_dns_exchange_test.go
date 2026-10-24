package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostDnsExchange(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
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
			asm, progDir := compileSourceModload(t, runner, driverBin, e2eharness.DnsExchangeSource(ns.Port))
			bin := buildBin(t, gcc, progDir, "lookup", asm)
			cmd := binCmd(runner, bin)
			out, _ := cmd.Output()
			e2eharness.CheckDnsExchange(t, c.mode, ns, string(out), cmd.ProcessState.ExitCode())
		})
	}
}

func TestSelfHostDnsPair(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	ns := e2eharness.StartFakeNameserver(t, e2eharness.FakeNameserverPair)
	asm, progDir := compileSourceModload(t, runner, driverBin, e2eharness.DnsPairSource(ns.Port))
	bin := buildBin(t, gcc, progDir, "pair", asm)
	cmd := binCmd(runner, bin)
	out, _ := cmd.Output()
	e2eharness.CheckDnsPair(t, ns, string(out), cmd.ProcessState.ExitCode())
}

func TestSelfHostDnsNat64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	ns := e2eharness.StartFakeNameserver(t, e2eharness.FakeNameserverNat64)
	asm, progDir := compileSourceModload(t, runner, driverBin, e2eharness.DnsNat64Source(ns.Port))
	bin := buildBin(t, gcc, progDir, "nat64", asm)
	cmd := binCmd(runner, bin)
	out, _ := cmd.Output()
	e2eharness.CheckDnsNat64(t, string(out), cmd.ProcessState.ExitCode())
}

func TestSelfHostDnsDial(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	for _, c := range []struct{ name, first string }{
		{"refused-first", "127.0.0.2"},
		{"blackhole-first", "192.0.2.1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			port := e2eharness.StartEchoListener(t)
			asm, progDir := compileSourceModload(t, runner, driverBin, e2eharness.DnsDialSource(c.first, port))
			bin := buildBin(t, gcc, progDir, "dial", asm)
			cmd := binCmd(runner, bin)
			out, _ := cmd.Output()
			e2eharness.CheckDnsDial(t, string(out), cmd.ProcessState.ExitCode())
		})
	}
}
