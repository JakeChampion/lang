package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// poll reads a status register and drops the value, then reads one byte
// register twice. A plain load in either place is free to go.
const mmioPollSrc = `function poll(addr: usize): i32 {
  unsafe {
    __mmio_load32(addr);
    let a = __mmio_load8(addr);
    let b = __mmio_load8(addr);
    return a + b;
  }
}

function main(): i32 {
  unsafe {
    let p: usize = __alloc(16);
    __mmio_store32(p, 0);
    let r = poll(p);
    __free(p, 16);
    return r;
  }
}
`

var mmioPollLoads = map[string][2]*regexp.Regexp{
	"x86-64-linux": {regexp.MustCompile(`(?m)^\s+movslq \(%rcx\), %rax$`), regexp.MustCompile(`(?m)^\s+movzbl \(%rcx\), %eax$`)},
	"arm64-linux":  {regexp.MustCompile(`(?m)^\s+ldrsw x0, \[x9\]$`), regexp.MustCompile(`(?m)^\s+ldrb w0, \[x9\]$`)},
}

// No pass removes an MMIO load whose value nothing reads, or merges two MMIO
// loads of one address, because each read has an effect on the device.
func TestSelfHostMMIOAccessesSurvive(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "poll.fern")
	if err := os.WriteFile(src, []byte(mmioPollSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			body := mmioFunctionBody(t, string(asm), "__fn___entry__poll:")
			if n := len(mmioPollLoads[target][0].FindAllString(body, -1)); n != 1 {
				t.Errorf("poll has %d 32-bit MMIO loads, want 1: the unread status read must stay\n%s", n, body)
			}
			if n := len(mmioPollLoads[target][1].FindAllString(body, -1)); n != 2 {
				t.Errorf("poll has %d byte MMIO loads, want 2: two reads of one register must stay two\n%s", n, body)
			}
		})
	}
}

// ring writes a descriptor, then rings the doorbell. The fence keeps the order of
// the two stores for the device.
const fenceRingSrc = `@noinline function ring(p: usize): void {
  unsafe {
    __mmio_store32(p, 7);
  }
  __fence();
  unsafe {
    __mmio_store32(p + 4, 1);
  }
}

function main(): i32 {
  unsafe {
    let p: usize = __alloc(16);
    ring(p);
    __free(p, 16);
  }
  return 0;
}
`

var fenceRing = map[string][2]string{
	"x86-64-linux": {"movl %eax, (%rcx)", "mfence"},
	"arm64-linux":  {"str w0, [x9]", "dsb sy"},
}

// No pass removes a fence or moves an MMIO store across it.
func TestSelfHostFenceOrdersMMIO(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "ring.fern")
	if err := os.WriteFile(src, []byte(fenceRingSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			body := mmioFunctionBody(t, string(asm), "__fn_ring:")
			store, fence := fenceRing[target][0], fenceRing[target][1]
			first := strings.Index(body, store)
			at := strings.Index(body, fence)
			last := strings.LastIndex(body, store)
			if first < 0 || at < 0 || first == last || !(first < at && at < last) {
				t.Errorf("ring must be store, %s, store; got the store at %d and %d and the fence at %d\n%s", fence, first, last, at, body)
			}
		})
	}
}

func mmioFunctionBody(t *testing.T, asm, label string) string {
	t.Helper()
	at := strings.Index(asm, "\n"+label+"\n")
	if at < 0 {
		t.Fatalf("no %s in the assembly", label)
	}
	body := asm[at:]
	if end := strings.Index(body, ".cfi_endproc"); end >= 0 {
		body = body[:end]
	}
	return body
}

// Each width stores and loads back through ordinary memory, on every backend and
// in both interpreters. The 32-bit load sign-extends, as __load_i32 does.
const mmioRoundTripSrc = `function main(): i32 {
  unsafe {
    let p: usize = __alloc(64);
    __mmio_store8(p, 200);
    __mmio_store16(p + 2, 40000);
    __mmio_store32(p + 4, -5);
    __mmio_store64(p + 8, 1234567890123i64);
    let ok = __mmio_load8(p) == 200 && __mmio_load16(p + 2) == 40000 && __mmio_load32(p + 4) == -5 && __mmio_load64(p + 8) == 1234567890123i64;
    let bytes = __load_u8(p + 2) == 64 && __load_u8(p + 3) == 156;
    __fence();
    __free(p, 64);
    if (ok && bytes) {
      RESULT_OK
    }
    return 1;
  }
}
`

func TestSelfHostMMIORoundTrip(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	native := filepath.Join(dir, "native.fern")
	if err := os.WriteFile(native, []byte(strings.Replace(mmioRoundTripSrc, "RESULT_OK", "return 42;", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	wasi := filepath.Join(dir, "wasi.fern")
	if err := os.WriteFile(wasi, []byte(strings.Replace(mmioRoundTripSrc, "RESULT_OK", `print("ok"); return 0;`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("x86-64-linux", func(t *testing.T) {
		cmd := runX86_64Bin(cli.runner, cli.x86Binary(t, native))
		out, _ := cmd.CombinedOutput()
		if code := cmd.ProcessState.ExitCode(); code != 42 {
			t.Errorf("exit %d, want 42\n%s", code, out)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		qemu := e2eharness.Arm64Runner(t)
		cmd := e2eharness.RunArm64Bin(qemu, cli.arm64Binary(t, native))
		out, _ := cmd.CombinedOutput()
		if code := cmd.ProcessState.ExitCode(); code != 42 {
			t.Errorf("exit %d, want 42\n%s", code, out)
		}
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		cmd := exec.Command(e2eharness.Wasmtime(t), "run", cli.wasmComponent(t, wasi))
		out, err := cmd.CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != "ok" {
			t.Errorf("wasmtime: %v, output %q, want ok", err, out)
		}
	})
	t.Run("interpreters", func(t *testing.T) {
		self := runX86_64Bin(cli.runner, cli.bin, "-interp", native, cli.stdlib)
		out, _ := self.CombinedOutput()
		if code := self.ProcessState.ExitCode(); code != 42 {
			t.Errorf("self-host -interp exit %d, want 42\n%s", code, out)
		}
		oracle := exec.Command(buildFernCLIBin(t), "-interp", native)
		out, _ = oracle.CombinedOutput()
		if code := oracle.ProcessState.ExitCode(); code != 42 {
			t.Errorf("Go -interp exit %d, want 42\n%s", code, out)
		}
	})
}
