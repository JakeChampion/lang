package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
	"x86-64-kernel": {regexp.MustCompile(`(?m)^\s+movslq \(%rcx\), %rax$`), regexp.MustCompile(`(?m)^\s+movzbl \(%rcx\), %eax$`)},
	"arm64-kernel":  {regexp.MustCompile(`(?m)^\s+ldrsw x0, \[x9\]$`), regexp.MustCompile(`(?m)^\s+ldrb w0, \[x9\]$`)},
}

// The kernel targets grant the mmio capability, and their -ram argument.
var mmioKernelRAM = map[string]string{"x86-64-kernel": kernelX86RAM, "arm64-kernel": kernelRAM}

// No pass removes an MMIO load whose value nothing reads, or merges two MMIO
// loads of one address, because each read has an effect on the device.
func TestSelfHostMMIOAccessesSurvive(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-kernel", "arm64-kernel"} {
		t.Run(target, func(t *testing.T) {
			asm := emitKernelAsm(t, cli, target, mmioKernelRAM[target], mmioPollSrc)
			body := mmioFunctionBody(t, asm, "__fn___entry__poll:")
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
	"x86-64-kernel": {"movl %eax, (%rcx)", "mfence"},
	"arm64-kernel":  {"str w0, [x9]", "dsb sy"},
}

// No pass removes a fence or moves an MMIO store across it.
func TestSelfHostFenceOrdersMMIO(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-kernel", "arm64-kernel"} {
		t.Run(target, func(t *testing.T) {
			asm := emitKernelAsm(t, cli, target, mmioKernelRAM[target], fenceRingSrc)
			body := mmioFunctionBody(t, asm, "__fn_ring:")
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

// Each width stores and loads back through ordinary memory in both
// interpreters, which run the accesses as plain loads and stores. The 32-bit
// load sign-extends, as __load_i32 does. The kernel raw tests run the same
// round trip on a frame of the VM.
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

// The mmio capability gates the intrinsics: a hosted target refuses them
// with E066 in both compilers, and the freestanding targets grant it, as the
// kernel targets do.
func TestSelfHostMMIONeedsAKernelOrFreestandingTarget(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "poll.fern")
	if err := os.WriteFile(src, []byte(mmioPollSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin", "wasm32-wasi"} {
		out, err := cli.compileCmd(target, filepath.Join(t.TempDir(), "program"), src).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "E066") || !strings.Contains(string(out), "__mmio_load32") || !strings.Contains(string(out), "`mmio`") {
			t.Errorf("%s: want E066 naming __mmio_load32 and mmio, got err=%v\n%s", target, err, out)
		}
	}
	oracle, err := exec.Command(buildFernCLIBin(t), "-check", "-target", "x86-64-linux", src).CombinedOutput()
	if err == nil || !strings.Contains(string(oracle), "E066") || !strings.Contains(string(oracle), "__mmio_load32") {
		t.Errorf("Go checker: want E066 naming __mmio_load32, got err=%v\n%s", err, oracle)
	}
	for _, target := range []string{"arm64-kernel", "x86-64-freestanding", "arm64-freestanding"} {
		if out, err := exec.Command(buildFernCLIBin(t), "-check", "-target", target, src).CombinedOutput(); err != nil {
			t.Errorf("Go checker on %s: %v\n%s", target, err, out)
		}
	}
	for _, target := range []string{"x86-64-freestanding", "arm64-freestanding"} {
		out := filepath.Join(t.TempDir(), "lib.s")
		if msg, err := cli.compileCmd(target, out, src, "-export", "poll", "-emit", "asm").CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s", target, err, msg)
		}
	}
}
