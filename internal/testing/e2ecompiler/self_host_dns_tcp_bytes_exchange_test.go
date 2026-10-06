package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Exercise the imported DNS module through the actual UDP-to-TCP retry,
// complementing the send-loop fault oracle that substitutes network calls.
func TestSelfHostDnsTCPBytesExchange(t *testing.T) {
	primary, stdlib := witSelfHostCLI(t)
	bootstrap := buildLangBinForInterp(t)
	_, targets, _ := hostTargets()
	targets = append(targets, ssaBackendTarget{target: "wasm32-wasi"})
	for _, compiler := range []struct{ name, path string }{{"primary", primary}, {"bootstrap", bootstrap}} {
		for _, target := range targets {
			t.Run(compiler.name+"/"+target.target, func(t *testing.T) {
				ns := e2eharness.StartFakeNameserver(t, e2eharness.FakeNameserverTruncate)
				dir := t.TempDir()
				src, bin := filepath.Join(dir, "lookup.fern"), filepath.Join(dir, "lookup")
				if err := os.WriteFile(src, []byte(e2eharness.DnsExchangeSource(ns.Port)), 0o644); err != nil {
					t.Fatal(err)
				}
				args := []string{"-target", target.target, "-o", bin}
				if compiler.name == "primary" && target.target == "wasm32-wasi" {
					args = append(args, "-emit", "core-module")
				}
				args = append(args, src)
				if compiler.name == "primary" {
					args = append(args, stdlib)
				}
				compile := exec.Command(compiler.path, args...)
				compile.Env = append(os.Environ(), "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if out, err := compile.CombinedOutput(); err != nil {
					t.Fatalf("compile: %v\n%s", err, out)
				}
				run := runX86_64Bin(target.runner, bin)
				if target.target == "wasm32-wasi" {
					if compiler.name == "primary" {
						run = composeSelfHostWat(t, bin)
					} else {
						run = exec.Command("wasmtime", "run", "-S", "inherit-network", bin)
					}
				}
				var diagnostic bytes.Buffer
				run.Stderr = &diagnostic
				out, err := run.Output()
				if run.ProcessState == nil {
					t.Fatalf("start lookup: %v", err)
				}
				if err != nil {
					t.Fatalf("lookup: %v\n%s\n%s", err, out, diagnostic.String())
				}
				e2eharness.CheckDnsExchange(t, e2eharness.FakeNameserverTruncate, ns, string(out), run.ProcessState.ExitCode())
				if compiler.name == "primary" && target.target != "wasm32-wasi" {
					assertBalancedCensus(t, diagnostic.String())
				}
			})
		}
	}
	t.Run("bootstrap/interpreter", func(t *testing.T) {
		ns := e2eharness.StartFakeNameserver(t, e2eharness.FakeNameserverTruncate)
		src := filepath.Join(t.TempDir(), "lookup.fern")
		if err := os.WriteFile(src, []byte(e2eharness.DnsExchangeSource(ns.Port)), 0o644); err != nil {
			t.Fatal(err)
		}
		run := exec.Command(bootstrap, "-interp", src)
		out, err := run.Output()
		if run.ProcessState == nil {
			t.Fatalf("start lookup: %v", err)
		}
		e2eharness.CheckDnsExchange(t, e2eharness.FakeNameserverTruncate, ns, string(out), run.ProcessState.ExitCode())
	})
}
