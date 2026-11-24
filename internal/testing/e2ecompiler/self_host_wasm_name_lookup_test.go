package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const wasmNameLookupSource = `import "std/dns";
import "std/net";

function show(name: string): string {
  match (dns.resolve(name)) {
    Ok(addrs) => {
      let out = "ok";
      for a in addrs {
        out = out + " " + a.to_string();
      }
      return out;
    },
    Err(e) => {
      return "err " + e.message();
    }
  }
  return "none";
}

function main(): i32 {
  print("localhost " + show("localhost"));
  print("literal " + show("192.0.2.7"));
  print("missing " + show("fern-no-such-name.invalid"));
  return 0;
}
`

// On wasm32-wasi, dns.resolve asks the host through
// wasi:sockets/ip-name-lookup, because a wasi program cannot read
// /etc/hosts or /etc/resolv.conf.
func TestSelfHostWasmNameLookup(t *testing.T) {
	wasmtime := e2eharness.Wasmtime(t)
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "lookup.fern")
	if err := os.WriteFile(src, []byte(wasmNameLookupSource), 0o644); err != nil {
		t.Fatal(err)
	}
	comp := cli.wasmComponent(t, src, "FERN_STRICT_IR=1")
	lines := func(args ...string) map[string]string {
		t.Helper()
		out, err := exec.Command(wasmtime, append(append([]string{"run"}, args...), comp)...).CombinedOutput()
		if err != nil {
			t.Fatalf("wasmtime run %v: %v\n%s", args, err, out)
		}
		got := map[string]string{}
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			k, v, _ := strings.Cut(l, " ")
			got[k] = v
		}
		return got
	}

	got := lines("-S", "inherit-network", "-S", "allow-ip-name-lookup")
	if l := got["localhost"]; !strings.HasPrefix(l, "ok ") || !strings.Contains(l, "127.0.0.1") && !strings.Contains(l, "::1") {
		t.Errorf("localhost: %q, want a loopback address", l)
	}
	if l := got["literal"]; l != "ok 192.0.2.7" {
		t.Errorf("literal: %q, want ok 192.0.2.7", l)
	}
	if l := got["missing"]; !strings.HasPrefix(l, "err ") {
		t.Errorf("missing: %q, want an error", l)
	}

	denied := lines()
	if l := denied["localhost"]; !strings.HasPrefix(l, "err ") {
		t.Errorf("localhost with no name lookup: %q, want an error", l)
	}
	if l := denied["literal"]; l != "ok 192.0.2.7" {
		t.Errorf("literal with no name lookup: %q, want ok 192.0.2.7", l)
	}
}
