package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// On wasm32-wasi, dns.resolve asks the host through
// wasi:sockets/ip-name-lookup, because a WASI program cannot reach /etc/hosts
// or /etc/resolv.conf. A host that does not allow name lookup gives an error,
// not a trap.
func TestSelfHostWasmNameLookup(t *testing.T) {
	requireWasmTools(t)
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(`import "std/dns";
import "std/net";
function main(): i32 {
    for name in ["localhost", "192.0.2.7"] {
        match (dns.resolve(name)) {
            Ok(xs) => {
                let s = "";
                for x in xs { s = s + " " + x.to_string(); }
                print(name + ":" + s);
            },
            Err(e) => { print(name + ": error " + e.message()); }
        }
    }
    return 0;
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "main.wasm")
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-o", out, src, cli.stdlib)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host CLI: %v\n%s", err, msg)
	}
	run := func(t *testing.T, flags ...string) []string {
		t.Helper()
		got, err := exec.Command("wasmtime", append(append([]string{"run"}, flags...), out)...).Output()
		if err != nil {
			t.Fatalf("wasmtime run: %v\n%s", err, got)
		}
		return strings.Split(strings.TrimSuffix(string(got), "\n"), "\n")
	}
	t.Run("allowed", func(t *testing.T) {
		lines := run(t, "-S", "allow-ip-name-lookup")
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "localhost:") || !strings.Contains(lines[0], " 127.0.0.1") {
			t.Errorf("localhost did not resolve to 127.0.0.1: %q", lines)
		}
		if len(lines) == 2 && lines[1] != "192.0.2.7: 192.0.2.7" {
			t.Errorf("a literal address = %q, want itself", lines[1])
		}
	})
	t.Run("refused", func(t *testing.T) {
		lines := run(t)
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "localhost: error ") {
			t.Errorf("a host without name lookup gave %q, want an error for localhost", lines)
		}
		if len(lines) == 2 && lines[1] != "192.0.2.7: 192.0.2.7" {
			t.Errorf("a literal address = %q, want itself without a lookup", lines[1])
		}
	})
}
