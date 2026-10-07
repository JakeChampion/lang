package e2ecompiler

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The constant-time gate (docs/NET-P4-TLS-PLAN.md slice 2). `__ct_secret`
// marks bytes undefined for valgrind's memcheck and `__ct_public` marks them
// defined again, so memcheck reports a branch or an address computed from a
// secret byte. Each program answers 0 when it computed the right thing, and
// a flagged one makes `valgrind --error-exitcode=3` exit 3.
type ctGateCase struct {
	name    string
	src     string
	flagged bool
}

var ctGateCases = []ctGateCase{
	{"secret-branch", `function main(): i32 {
  let k: u8[] = __alloc_u8(16);
  k = k.with(0, 7 as u8);
  __ct_secret(k);
  if (k[0] == 7 as u8) {
    return 0;
  }
  return 1;
}
`, true},
	{"secret-index", `function main(): i32 {
  let k: u8[] = __alloc_u8(4);
  k = k.with(0, 2 as u8);
  __ct_secret(k);
  let table: i32[] = [10, 20, 30, 40];
  return table[k[0] as i32] - 30;
}
`, true},
	{"branch-free-then-public", `function main(): i32 {
  let k: u8[] = __alloc_u8(16);
  k = k.with(0, 7 as u8).with(1, 9 as u8);
  __ct_secret(k);
  let out: u8[] = __alloc_u8(1);
  out = out.with(0, k[0] ^ k[1]);
  __ct_public(out);
  if (out[0] == 14 as u8) {
    return 0;
  }
  return 1;
}
`, false},
	// A string's byte view is a tagged descriptor, not a packed array: the
	// mark must land on the string's own bytes.
	{"string-view-secret", `function main(): i32 {
  let s: string = "pass" + "word";
  let v: [u8] = s.as_bytes();
  __ct_secret(v);
  if (v[4] == 119 as u8) {
    return 0;
  }
  return 1;
}
`, true},
	{"string-view-public", `function main(): i32 {
  let s: string = "pass" + "word";
  let v: [u8] = s.as_bytes();
  __ct_secret(v);
  __ct_public(v);
  if (v[4] == 119 as u8) {
    return 0;
  }
  return 1;
}
`, false},
	// The AEAD over a secret key and plaintext: open's tag verdict is the one
	// secret-derived value it branches on, and it declassifies exactly that.
	{"chacha20poly1305", `import "std/crypto/chacha20poly1305";

function filled(n: i32, seed: i32): u8[] {
  let b: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    b = b.with(i, ((i * 7 + seed) & 255) as u8);
    i = i + 1;
  }
  return b;
}

function same(a: u8[], b: u8[]): boolean {
  if (a.len() != b.len()) {
    return false;
  }
  let i: i32 = 0;
  while (i < a.len()) {
    if (a[i] != b[i]) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function main(): i32 {
  let key: u8[] = filled(32, 1);
  let nonce: u8[] = filled(12, 2);
  let plaintext: u8[] = filled(200, 3);
  let aad: u8[] = filled(17, 4);
  __ct_secret(key);
  __ct_secret(plaintext);
  match (chacha20poly1305.seal(key, nonce, plaintext, aad)) {
    Ok(sealed) => {
      __ct_public(sealed);
      match (chacha20poly1305.open(key, nonce, sealed, aad)) {
        Ok(opened) => {
          __ct_public(opened);
          __ct_public(plaintext);
          if (!same(opened, plaintext)) {
            return 2;
          }
        },
        Err(e) => {
          return 3;
        }
      }
      let forged: u8[] = sealed.with(0, sealed[0] ^ 1 as u8);
      match (chacha20poly1305.open(key, nonce, forged, aad)) {
        Ok(opened) => {
          return 4;
        },
        Err(e) => {
          return 0;
        }
      }
    },
    Err(e) => {
      return 5;
    }
  }
  return 6;
}
`, false},
	// The key exchange over two secret scalars: the ladder swaps by mask, and
	// the low-order refusal declassifies only its all-zero verdict.
	{"x25519", `import "std/crypto/x25519";

function filled(seed: i32): u8[] {
  let b: u8[] = __alloc_u8(32);
  let i: i32 = 0;
  while (i < 32) {
    b = b.with(i, ((i * 13 + seed) & 255) as u8);
    i = i + 1;
  }
  return b;
}

function main(): i32 {
  let a: u8[] = filled(5);
  let b: u8[] = filled(9);
  __ct_secret(a);
  __ct_secret(b);
  match (x25519.public_key(a)) {
    Ok(pa) => {
      __ct_public(pa);
      match (x25519.public_key(b)) {
        Ok(pb) => {
          __ct_public(pb);
          match (x25519.x25519(a, pb)) {
            Ok(sa) => {
              match (x25519.x25519(b, pa)) {
                Ok(sb) => {
                  __ct_public(sa);
                  __ct_public(sb);
                  let i: i32 = 0;
                  while (i < 32) {
                    if (sa[i] != sb[i]) {
                      return 1;
                    }
                    i = i + 1;
                  }
                  return 0;
                },
                Err(e) => {
                  return 2;
                }
              }
            },
            Err(e) => {
              return 3;
            }
          }
        },
        Err(e) => {
          return 4;
        }
      }
    },
    Err(e) => {
      return 5;
    }
  }
  return 6;
}
`, false},
}

const ctUninitialised = "depends on uninitialised value"

// ctCompile writes each case's source and compiles it for target with the
// self-host CLI, through the compiler's own assembler and linker as `fern -o`
// does, returning the binaries by case name.
func ctCompile(t *testing.T, runner []string, cli, stdlib, target string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	bins := map[string]string{}
	for _, c := range ctGateCases {
		src := filepath.Join(dir, c.name+".fern")
		if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(dir, c.name)
		cmd := runX86_64Bin(runner, cli, "-target", target, "-o", bin, src, stdlib)
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("self-host CLI -target %s %s: %v\n%s", target, c.name, err, msg)
		}
		if err := os.Chmod(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		bins[c.name] = bin
	}
	return bins
}

// exitOf runs cmd and answers its exit status and stderr.
func exitOf(t *testing.T, cmd *exec.Cmd) (int, string) {
	t.Helper()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("run %s: %v", cmd.Path, err)
	}
	return cmd.ProcessState.ExitCode(), stderr.String()
}

// ctGate runs every case natively, where the marks change nothing, and then
// under memcheck, where a flagged case must exit 3 naming the uninitialised
// value and every other case must be clean.
func ctGate(t *testing.T, valgrind string, bins map[string]string) {
	t.Helper()
	for _, c := range ctGateCases {
		t.Run(c.name, func(t *testing.T) {
			if code, stderr := exitOf(t, exec.Command(bins[c.name])); code != 0 {
				t.Fatalf("native run exited %d, want 0\n%s", code, stderr)
			}
			code, stderr := exitOf(t, exec.Command(valgrind, "--error-exitcode=3", "-q", bins[c.name]))
			if c.flagged {
				if code != 3 || !strings.Contains(stderr, ctUninitialised) {
					t.Fatalf("memcheck exited %d, want 3 with %q: a secret byte steered the program unreported\n%s", code, ctUninitialised, stderr)
				}
				return
			}
			if code != 0 || stderr != "" {
				t.Fatalf("memcheck exited %d, want a clean 0\n%s", code, stderr)
			}
		})
	}
}

func TestSelfHostCtGateX86_64(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("memcheck runs x86-64 binaries only on an x86-64 Linux host")
	}
	valgrind := e2eharness.Valgrind(t)
	cli := buildSelfHostCLI(t)
	ctGate(t, valgrind, ctCompile(t, cli.runner, cli.bin, cli.stdlib, "x86-64-linux"))
}

// The arm64 leg needs an arm64 host: memcheck cannot run an aarch64 binary
// under qemu. There the self-host CLI is built for the host itself.
func TestSelfHostCtGateArm64(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" {
		t.Skip("memcheck runs arm64 binaries only on an arm64 Linux host; TestSelfHostCtMarksArm64 runs the marks under qemu")
	}
	valgrind := e2eharness.Valgrind(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := e2eharness.BuildSelfHostBinFor(t, dir, "fern.fern", "fern", e2eharness.TargetArm64Linux)
	ctGate(t, valgrind, ctCompile(t, nil, cli, langSrcAbs(t, "internal/stdlib"), "arm64-linux"))
}

// wasm has no valgrind, so the marks compile to nothing there: every case
// runs to its own answer under wasmtime.
func TestSelfHostCtMarksWasm(t *testing.T) {
	wasmtime := e2eharness.Wasmtime(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	for _, c := range ctGateCases {
		t.Run(c.name, func(t *testing.T) {
			src := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			if code, stderr := exitOf(t, exec.Command(wasmtime, "run", cli.wasmComponent(t, src, "FERN_STRICT_IR=1"))); code != 0 {
				t.Fatalf("wasmtime run exited %d, want 0\n%s", code, stderr)
			}
		})
	}
}

// Both interpreters treat the marks as no-ops: every single-file case runs to
// its own answer under the Go interpreter and the self-host one.
func TestSelfHostInterpCtMarks(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/interp_run.fern")
	gcc, runner := x86_64Tooling(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run")
	oracle := buildLangBinForInterp(t)
	for _, c := range ctGateCases {
		if strings.HasPrefix(c.src, "import") {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			if got := interpExitStdin(t, oracle, c.src, ""); got != 0 {
				t.Fatalf("Go interpreter exited %d, want 0", got)
			}
			if got := runDriverExit(t, runner, driver, []byte(c.src)); got != 0 {
				t.Fatalf("self-host interpreter exited %d, want 0", got)
			}
		})
	}
}

// Outside valgrind the arm64 client request is a no-op: every case, the
// flagged ones included, runs to its own answer under qemu.
func TestSelfHostCtMarksArm64(t *testing.T) {
	qemu := e2eharness.Arm64Runner(t)
	cli := buildSelfHostCLI(t)
	for name, bin := range ctCompile(t, cli.runner, cli.bin, cli.stdlib, "arm64-linux") {
		if code, stderr := exitOf(t, e2eharness.RunArm64Bin(qemu, bin)); code != 0 {
			t.Errorf("%s exited %d, want 0\n%s", name, code, stderr)
		}
	}
}
