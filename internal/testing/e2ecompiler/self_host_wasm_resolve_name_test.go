package e2ecompiler

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	e2eharness "github.com/jakechampion/lang/internal/testing/e2eharness"
)

const resolveNameProbe = `import "std/dns";
import "std/net";

function zeros(n: i32): u8[] {
  let b: u8[] = [];
  for k in 0..n {
    b = b.append(0u8);
  }
  return b;
}

function records(name: string, room: i32): string {
  let out = zeros(room);
  let n = wasm_resolve_name(name, out);
  let text = name + " " + n.to_string() + ":";
  if (n > 0) {
    for k in 0..n * 17 {
      text = text + " " + (out[k] as i32).to_string();
    }
  }
  return text;
}

function loopback(xs: net.IpAddr[]): boolean {
  for x in xs {
    if (x.is_loopback()) {
      return true;
    }
  }
  return false;
}

function main(): i32 {
  print(records("192.0.2.7", 34));
  print(records("2001:db8::102", 17));
  print(records("192.0.2.7", 16));
  if (wasm_resolve_name("no-such-host.invalid", zeros(17)) >= 0) {
    return 1;
  }
  match (dns.resolve("localhost")) {
    Ok(xs) => {
      if (!loopback(xs)) {
        return 2;
      }
    },
    Err(e) => {
      print("localhost: " + e.message());
      return 3;
    }
  }
  if let Ok(_) = dns.resolve("no-such-host.invalid") {
    return 4;
  }
  return 0;
}
`

// wasm_resolve_name gives the addresses of wasi:sockets/ip-name-lookup, and
// dns.resolve uses it on wasm32-wasi.
func TestSelfHostWasmResolveName(t *testing.T) {
	wasmtime := e2eharness.Wasmtime(t)
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(resolveNameProbe), 0o644); err != nil {
		t.Fatal(err)
	}
	component := filepath.Join(dir, "main.wasm")
	compile := exec.Command(h.cli, "-target", "wasm32-wasi", "-o", component, src, h.stdlib)
	compile.Env = append(os.Environ(), "FERN_STRICT_IR=1")
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile to a component: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, wasmtime, "run", "-S", "inherit-network", "-S", "allow-ip-name-lookup", component).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	want := "192.0.2.7 1: 4 192 0 2 7 0 0 0 0 0 0 0 0 0 0 0 0\n" +
		"2001:db8::102 1: 6 32 1 13 184 0 0 0 0 0 0 0 0 0 0 1 2\n" +
		"192.0.2.7 0:\n"
	if string(out) != want {
		t.Errorf("records:\n%s\nwant:\n%s", out, want)
	}

	t.Run("lookup-not-allowed", func(t *testing.T) {
		denied := filepath.Join(dir, "denied.fern")
		body := "function main(): i32 {\n  let out: u8[] = [0u8];\n  if (wasm_resolve_name(\"localhost\", out) < 0) {\n    return 0;\n  }\n  return 1;\n}\n"
		if err := os.WriteFile(denied, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		comp := filepath.Join(dir, "denied.wasm")
		if out, err := exec.Command(h.cli, "-target", "wasm32-wasi", "-o", comp, denied, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, out)
		}
		if out, err := exec.CommandContext(ctx, wasmtime, "run", comp).CombinedOutput(); err != nil {
			t.Fatalf("a host that does not allow the lookup must give an error code: %v\n%s", err, out)
		}
	})

	gated := filepath.Join(dir, "gated.fern")
	body := "function main(): i32 {\n  let out: u8[] = [0u8];\n  return wasm_resolve_name(\"localhost\", out);\n}\n"
	if err := os.WriteFile(gated, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin"} {
		t.Run("refused/"+target, func(t *testing.T) {
			out, err := exec.Command(h.cli, "-target", target, "-o", filepath.Join(dir, "gated"), gated, h.stdlib).CombinedOutput()
			code := -1
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			}
			want := "does not provide `resolve`, required by `wasm_resolve_name`"
			if code != 1 || !strings.Contains(string(out), "E066") || !strings.Contains(string(out), want) {
				t.Fatalf("exit %d, want 1 with E066 %q:\n%s", code, want, out)
			}
		})
	}
}
