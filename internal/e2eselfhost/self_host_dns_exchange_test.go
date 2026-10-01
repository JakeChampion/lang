package e2eselfhost

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The self-host twin of TestDnsExchangeX86_64: std/dns's exchange against
// a nameserver on the loopback interface, over UDP, through the TCP retry
// a truncated reply forces, and against a server that stays silent.
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

// TestSelfHostDnsPair is the self-host twin of TestDnsPairX86_64.
func TestSelfHostDnsPair(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	ns := e2eharness.StartFakeNameserver(t, e2eharness.FakeNameserverPair)
	asm, progDir := compileSourceModload(t, runner, driverBin, e2eharness.DnsPairSource(ns.Port))
	bin := buildBin(t, gcc, progDir, "pair", asm)
	cmd := binCmd(runner, bin)
	out, _ := cmd.Output()
	e2eharness.CheckDnsPair(t, ns, string(out), cmd.ProcessState.ExitCode())
}
