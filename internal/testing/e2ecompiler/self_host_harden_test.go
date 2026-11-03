package e2ecompiler

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const selfHostHardenSrc = `import "std/i32";

trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
struct Rect { w: i32, h: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } }

@noinline
function apply(f: (i32) => i32, x: i32): i32 {
  return f(x);
}

@noinline
function twice(x: i32): i32 {
  return x * 2;
}

function check_base64_growth(): boolean {
  let b: usize = buf_new(1);
  let source: u8[] = [65u8, 65u8, 65u8];
  let alphabet: u8[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/".bytes();
  for i in 0..8 {
    buf_push_base64(b, source, 0, source.len(), alphabet);
  }
  let encoded: string = buf_take(b);
  buf_free(b);
  return encoded == "QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFB";
}

function main(): i32 {
  let k: i32 = 5;
  let add: (i32) => i32 = (x: i32): i32 => x + k;
  let shapes: dyn Shape[] = [Sq { s: 3 }, Rect { w: 2, h: 4 }];
  let total: i32 = 0;
  for s in shapes {
    total = total + s.area();
  }
  let xs: i32[] = [1, 2, 3].map(twice);
  print(apply(add, 10).to_string() + " " + apply(twice, 7).to_string() + " " + total.to_string() + " " + xs[2].to_string());
  if (!check_base64_growth()) { return 1; }
  return 0;
}
`

const hardenWant = "15 14 17 6"

const hardenFramesSrc = `import "std/string";
import "std/crypto";
import "std/hash/cksum";
import "std/hash/crc32";
import "std/crypto/aes_gcm";

function sealed_len(): u64 {
  let plain: u8[] = "hardened frames".bytes();
  match (aes_gcm.key("0123456789abcdef".bytes())) {
    Ok(k) => {
      match (k.seal("0123456789ab".bytes(), plain, "".bytes())) {
        Ok(sealed) => {
          match (k.open("0123456789ab".bytes(), sealed, "".bytes())) {
            Ok(back) => {
              if (crc32.of(back) == crc32.of(plain)) { return sealed.len() as u64; }
              return 0 as u64;
            },
            Err(e) => { return 1 as u64; }
          }
        },
        Err(e) => { return 2 as u64; }
      }
    },
    Err(e) => { return 3 as u64; }
  }
  return 4 as u64;
}

function main(): i32 {
  let abc: u8[] = "abc".bytes();
  let b: usize = buf_new(4);
  buf_push_decimal(b, cksum.of(abc) as u64);
  buf_push(b, " ");
  buf_push_decimal(b, crc32.of(abc) as u64);
  buf_push(b, " ");
  buf_push_decimal(b, crypto.sha256(abc)[0] as u64);
  buf_push(b, " ");
  buf_push_decimal(b, crypto.sha1(abc)[0] as u64);
  buf_push(b, " ");
  buf_push_decimal(b, sealed_len());
  buf_push(b, " ");
  buf_push_bytes_range(b, "framed bytes".bytes(), 0, 6);
  if (args().len() > 9) {
    let r: ProcessResult = subprocess("/bin/true", [], "");
    buf_push_decimal(b, r.exit_code as u64);
  }
  print(buf_take(b));
  return 0;
}
`

const hardenFramesWant = "1219131554 891568578 186 169 31 framed"

// hardenFrameHelpers are the hand-written runtime helpers that hardenFramesSrc
// links. The ones it also runs have a PAC control each.
var hardenFrameHelpers = []struct {
	label string
	runs  bool
}{
	{"__fern_buf_push_decimal:", true},
	{"__fern_buf_push_bytes_range_u8:", true},
	{"__fern_crc32_cksum_data:", true},
	{"__fern_aes_expand_key:", true},
	{"__fern_aes_ctr32:", false},
	{"__fern_ghash:", false},
	{"__fern_aes_gcm_seal_into:", true},
	{"__fern_aes_gcm_open_into:", true},
	{"__fern_sha256_hw_blocks:", true},
	{"__fern_sha1_hw_blocks:", true},
	{"__fn___fern_subprocess:", false},
	{"__fern_oob_abort_leaf:", false},
}

const hardenAbortSrc = `@noinline
function pick(xs: i32[], i: i32): i32 {
  return xs[i];
}

@noinline
function outer(xs: i32[], i: i32): i32 {
  return pick(xs, i) + 1;
}

function main(): i32 {
  return outer([1, 2, 3], 7);
}
`

func TestSelfHostHardenArm64(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux ELF hardening requires a Linux host")
	}
	cli := e2eharness.SelfHostCLI(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	armgcc, qemu := arm64Tooling(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(selfHostHardenSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	compile := func(out string, flags ...string) {
		t.Helper()
		args := append(append([]string{}, flags...), "-target", "arm64-linux", "-o", out, src, stdlib)
		if msg, err := exec.Command(cli, args...).CombinedOutput(); err != nil {
			t.Fatalf("compile %v: %v\n%s", flags, err, msg)
		}
		if err := os.Chmod(out, 0o755); err != nil && !strings.HasSuffix(out, ".s") {
			t.Fatal(err)
		}
	}
	enforced := cpuEnforces(qemu, " bti")
	authenticated := cpuEnforces(qemu, " paca")
	mode := "native"
	if qemu != "" {
		mode = "QEMU"
	}
	kernel, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		t.Fatalf("read the kernel version: %v", err)
	}
	t.Logf("compiler host=%s/%s; kernel=%s", runtime.GOOS, runtime.GOARCH, strings.TrimSpace(string(kernel)))
	t.Logf("execution=%s; BTI fault control requested=%t; PAC fault control requested=%t", mode, enforced, authenticated)

	t.Run("in-process", func(t *testing.T) {
		plain := filepath.Join(dir, "plain")
		hard := filepath.Join(dir, "hard")
		compile(plain)
		compile(hard, "-harden")
		if btiNote(t, plain) {
			t.Error("an image built without -harden asks for BTI")
		}
		if !btiNote(t, hard) {
			t.Fatal("the -harden image has no PT_GNU_PROPERTY asking for BTI")
		}
		for _, bin := range []string{plain, hard} {
			if out := runHardened(t, qemu, bin); out != hardenWant {
				t.Errorf("%s printed %q, want %q", filepath.Base(bin), out, hardenWant)
			}
		}
		img, err := os.ReadFile(hard)
		if err != nil {
			t.Fatal(err)
		}
		pad := binary.LittleEndian.AppendUint32(nil, 0xd503245f)
		if bytes.Count(img, pad) != 2 {
			t.Fatalf("the -harden image has %d landing pads, want one per address-taken function (the closure and twice)", bytes.Count(img, pad))
		}
		sign := binary.LittleEndian.AppendUint32(nil, 0xd503233f)
		auth := binary.LittleEndian.AppendUint32(nil, 0xd50323bf)
		if bytes.Count(img, sign) == 0 || bytes.Count(img, auth) < bytes.Count(img, sign) {
			t.Fatalf("the -harden image has %d paciasp and %d autiasp, want every framed function to sign and each of its exits to authenticate", bytes.Count(img, sign), bytes.Count(img, auth))
		}
		if authenticated {
			// PACIASP is also a BTI target, so the control keeps a pad in its place.
			unsigned := bytes.ReplaceAll(img, sign, pad)
			unsignedBin := filepath.Join(dir, "direct-unsigned")
			if err := os.WriteFile(unsignedBin, unsigned, 0o755); err != nil {
				t.Fatal(err)
			}
			expectHardenSignal(t, qemu, unsignedBin, syscall.SIGSEGV, syscall.SIGILL)
			unauthenticated := filepath.Join(dir, "direct-unauthenticated")
			if err := os.WriteFile(unauthenticated, bytes.ReplaceAll(unsigned, auth, binary.LittleEndian.AppendUint32(nil, 0xd503201f)), 0o755); err != nil {
				t.Fatal(err)
			}
			if out := runHardened(t, qemu, unauthenticated); out != hardenWant {
				t.Fatalf("the direct-image PAC control without authentication printed %q, want %q", out, hardenWant)
			}
			t.Logf("direct-image PAC fault control passed (%s)", mode)
		}
		if !enforced {
			t.Log("this host does not enforce BTI; the pads are not proved load-bearing here")
			return
		}
		// The indirect-call check refuses a padless stub before the branch,
		// so the BTI control also turns its compare into cmp w16, w16.
		nopad := filepath.Join(dir, "nopad")
		cmp := binary.LittleEndian.AppendUint32(nil, 0x6b11021f)
		cmpSame := binary.LittleEndian.AppendUint32(nil, 0x6b10021f)
		if bytes.Count(img, cmp) == 0 {
			t.Fatal("the -harden image has no indirect-call compare")
		}
		unchecked := bytes.ReplaceAll(bytes.ReplaceAll(img, pad, binary.LittleEndian.AppendUint32(nil, 0xd503201f)), cmp, cmpSame)
		if err := os.WriteFile(nopad, unchecked, 0o755); err != nil {
			t.Fatal(err)
		}
		expectSIGILL(t, qemu, nopad)
		t.Logf("BTI fault control passed (%s)", mode)
	})

	t.Run("emit-asm", func(t *testing.T) {
		asm := filepath.Join(dir, "hard.s")
		compile(asm, "-harden", "-emit", "asm")
		text, err := os.ReadFile(asm)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(text, []byte(".note.gnu.property")) {
			t.Fatal("the -harden text carries no .note.gnu.property")
		}
		link := func(name string, body []byte) string {
			t.Helper()
			s := filepath.Join(dir, name+".s")
			bin := filepath.Join(dir, name)
			if err := os.WriteFile(s, body, 0o644); err != nil {
				t.Fatal(err)
			}
			if msg, err := exec.Command(armgcc, "-nostdlib", "-static", "-o", bin, s).CombinedOutput(); err != nil {
				t.Fatalf("link %s: %v\n%s", name, err, msg)
			}
			return bin
		}
		linked := link("linked", text)
		if !btiNote(t, linked) {
			t.Fatal("the linked -harden image has no PT_GNU_PROPERTY asking for BTI")
		}
		if out := runHardened(t, qemu, linked); out != hardenWant {
			t.Errorf("printed %q, want %q", out, hardenWant)
		}
		if !bytes.Contains(text, []byte("__fern_alloc:\n    paciasp\n    stp x29, x30, [sp, #-16]!\n")) {
			t.Fatal("the -harden text does not sign the return address of the hand-written __fern_alloc frame")
		}
		const growSignature = ".Lb64_grow:\n    paciasp\n"
		if !bytes.Contains(text, []byte(growSignature)) {
			t.Fatal("the -harden base64 grow frame does not sign its return address")
		}
		if enforced {
			var kept []string
			for _, line := range strings.Split(string(text), "\n") {
				if strings.TrimSpace(line) == "cmp w16, w17" {
					line = "    cmp w16, w16"
				}
				if strings.TrimSpace(line) != "bti c" {
					kept = append(kept, line)
				}
			}
			expectSIGILL(t, qemu, link("unpadded", []byte(strings.Join(kept, "\n"))))
			t.Logf("BTI fault control passed (%s)", mode)
		} else {
			t.Log("BTI enforcement remains unverified on this host")
		}
		if !authenticated {
			t.Log("PAC enforcement remains unverified on this host")
			return
		}
		var kept []string
		for _, line := range strings.Split(string(text), "\n") {
			if strings.TrimSpace(line) == "paciasp" {
				// PACIASP is also a BTI target. Preserve BTI in the PAC control.
				line = "    bti c"
			}
			kept = append(kept, line)
		}
		expectHardenSignal(t, qemu, link("unsigned", []byte(strings.Join(kept, "\n"))), syscall.SIGSEGV, syscall.SIGILL)
		for i, line := range kept {
			if strings.TrimSpace(line) == "autiasp" {
				kept[i] = "    nop"
			}
		}
		if out := runHardened(t, qemu, link("unauthenticated", []byte(strings.Join(kept, "\n")))); out != hardenWant {
			t.Fatalf("the PAC control without authentication printed %q, want %q", out, hardenWant)
		}
		t.Logf("PAC fault control passed (%s)", mode)
		// The runtime frame control removes one signature only, the one of
		// the hand-written __fern_alloc frame, and keeps its authentication.
		unsignedRuntime := strings.Replace(string(text), "__fern_alloc:\n    paciasp\n", "__fern_alloc:\n", 1)
		expectHardenSignal(t, qemu, link("unsigned-runtime", []byte(unsignedRuntime)), syscall.SIGSEGV, syscall.SIGILL)
		t.Logf("PAC runtime frame control passed (%s)", mode)
		unsignedGrowth := strings.Replace(string(text), growSignature, ".Lb64_grow:\n", 1)
		expectHardenSignal(t, qemu, link("unsigned-base64-growth", []byte(unsignedGrowth)), syscall.SIGSEGV, syscall.SIGILL)
		t.Logf("PAC base64 grow frame control passed (%s)", mode)
	})

	t.Run("mixed-link", func(t *testing.T) {
		// The linker keeps the BTI property only when every input carries it,
		// so a C object built without branch protection drops it silently.
		asm := filepath.Join(dir, "mixed.s")
		compile(asm, "-harden", "-emit", "asm")
		csrc := filepath.Join(dir, "helper.c")
		if err := os.WriteFile(csrc, []byte("int fern_c_helper(int x) { return x + 1; }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name, protection string
			note             bool
		}{{"protected", "standard", true}, {"unprotected", "none", false}} {
			obj := filepath.Join(dir, tc.name+".o")
			if msg, err := exec.Command(armgcc, "-c", "-O2", "-mbranch-protection="+tc.protection, "-o", obj, csrc).CombinedOutput(); err != nil {
				t.Fatalf("compile the %s helper: %v\n%s", tc.name, err, msg)
			}
			bin := filepath.Join(dir, "mixed-"+tc.name)
			if msg, err := exec.Command(armgcc, "-nostdlib", "-static", "-o", bin, asm, obj).CombinedOutput(); err != nil {
				t.Fatalf("link with the %s helper: %v\n%s", tc.name, err, msg)
			}
			if got := btiNote(t, bin); got != tc.note {
				t.Errorf("linked with the %s helper, the image asks for BTI: %t, want %t", tc.name, got, tc.note)
			}
			if out := runHardened(t, qemu, bin); out != hardenWant {
				t.Errorf("linked with the %s helper, printed %q, want %q", tc.name, out, hardenWant)
			}
		}
	})

	t.Run("backtrace", func(t *testing.T) {
		abort := filepath.Join(dir, "abort.fern")
		if err := os.WriteFile(abort, []byte(hardenAbortSrc), 0o644); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(dir, "abort")
		args := []string{"-harden", "-backtrace", "-target", "arm64-linux", "-o", bin, abort, stdlib}
		if msg, err := exec.Command(cli, args...).CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, msg)
		}
		out, _ := runArm64Bin(qemu, bin).CombinedOutput()
		frames := 0
		for _, line := range strings.Split(string(out), "\n") {
			hex, ok := strings.CutPrefix(strings.TrimSpace(line), "0x")
			if !ok {
				continue
			}
			frames++
			if v, err := strconv.ParseUint(hex, 16, 64); err != nil || v >= 1<<32 {
				t.Errorf("backtrace frame %q is not a plain code address", line)
			}
		}
		if frames < 2 {
			t.Fatalf("want a backtrace through pick and outer, got:\n%s", out)
		}
	})

	t.Run("runtime-frames", func(t *testing.T) {
		frames := filepath.Join(dir, "frames.fern")
		if err := os.WriteFile(frames, []byte(hardenFramesSrc), 0o644); err != nil {
			t.Fatal(err)
		}
		build := func(out string, flags ...string) {
			t.Helper()
			args := append(append([]string{}, flags...), "-target", "arm64-linux", "-o", out, frames, stdlib)
			if msg, err := exec.Command(cli, args...).CombinedOutput(); err != nil {
				t.Fatalf("compile %v: %v\n%s", flags, err, msg)
			}
		}
		asm := filepath.Join(dir, "frames.s")
		bin := filepath.Join(dir, "frames")
		build(asm, "-harden", "-emit", "asm")
		build(bin, "-harden")
		if out := runHardened(t, qemu, bin); out != hardenFramesWant {
			t.Fatalf("the -harden frames image printed %q, want %q", out, hardenFramesWant)
		}
		raw, err := os.ReadFile(asm)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, h := range hardenFrameHelpers {
			if !strings.Contains(text, "\n"+h.label+"\n") {
				t.Errorf("the frames program does not link %s", strings.TrimSuffix(h.label, ":"))
			}
		}
		for _, fault := range unsignedFrames(text) {
			t.Errorf("-harden frame without a signature: %s", fault)
		}
		if !authenticated {
			t.Log("PAC enforcement of the runtime frames remains unverified on this host")
			return
		}
		for _, h := range hardenFrameHelpers {
			if !h.runs {
				continue
			}
			at := strings.Index(text, "\n"+h.label+"\n")
			sig := strings.Index(text[at:], "    paciasp\n")
			if at < 0 || sig < 0 {
				t.Fatalf("%s has no paciasp", h.label)
			}
			name := strings.TrimSuffix(h.label, ":")
			unsigned := text[:at+sig] + text[at+sig+len("    paciasp\n"):]
			s := filepath.Join(dir, "unsigned"+name+".s")
			out := filepath.Join(dir, "unsigned"+name)
			if err := os.WriteFile(s, []byte(unsigned), 0o644); err != nil {
				t.Fatal(err)
			}
			if msg, err := exec.Command(armgcc, "-nostdlib", "-static", "-o", out, s).CombinedOutput(); err != nil {
				t.Fatalf("link %s: %v\n%s", name, err, msg)
			}
			expectHardenSignal(t, qemu, out, syscall.SIGSEGV, syscall.SIGILL)
		}
		compact := regexp.MustCompile(`(?m)^    paciasp\n(    (stp x[0-9]+, x30|str x30), \[sp, #-16\]!\n)`)
		if !compact.MatchString(text) {
			t.Fatal("the frames program has no signed compact save of x30")
		}
		s := filepath.Join(dir, "unsigned-compact.s")
		out := filepath.Join(dir, "unsigned-compact")
		if err := os.WriteFile(s, []byte(compact.ReplaceAllString(text, "$1")), 0o644); err != nil {
			t.Fatal(err)
		}
		if msg, err := exec.Command(armgcc, "-nostdlib", "-static", "-o", out, s).CombinedOutput(); err != nil {
			t.Fatalf("link unsigned-compact: %v\n%s", err, msg)
		}
		expectHardenSignal(t, qemu, out, syscall.SIGSEGV, syscall.SIGILL)
		t.Logf("PAC runtime frame controls passed (%s)", mode)
	})

	t.Run("refusals", func(t *testing.T) {
		for _, args := range [][]string{
			{"-harden", "-target", "arm64-darwin", "-emit", "asm", "-o", filepath.Join(dir, "x.s"), src, stdlib},
			{"-harden", "-target", "arm64-android", "-o", filepath.Join(dir, "android"), src, stdlib},
		} {
			msg, err := exec.Command(cli, args...).CombinedOutput()
			if err == nil || !strings.Contains(string(msg), "-harden is only supported for -target arm64-linux and x86-64-linux (got") {
				t.Errorf("%v: want the -harden refusal, got err=%v\n%s", args, err, msg)
			}
		}
	})
}

// unsignedFrames lists each stack save of x30 in arm64 -harden text with no
// paciasp before it, and each restore with no autiasp before the next exit.
// It also lists a paciasp that signs x30 again while it is still signed.
// The startup frame never returns, so it is exempt.
func unsignedFrames(text string) []string {
	lines := strings.Split(text, "\n")
	var faults []string
	fn := ""
	for i, line := range lines {
		op := strings.TrimSpace(line)
		if strings.HasSuffix(op, ":") && !strings.HasPrefix(op, ".L") {
			fn = strings.TrimSuffix(op, ":")
		}
		switch {
		case savesLR(op, "stp", "str"):
			if fn != "_start" && !signedBefore(lines, i) {
				faults = append(faults, fn+": "+op)
			}
		case savesLR(op, "ldp", "ldr"):
			if !authenticatedAfter(lines, i) {
				faults = append(faults, fn+": "+op)
			}
		case op == "paciasp":
			if signedBefore(lines, i) {
				faults = append(faults, fn+": paciasp of a signed x30")
			}
		}
	}
	return faults
}

// savesLR matches a frame record of x29 and x30, and a push or pop of x30
// alone or in a pair. A plain offset store of x30 is a spill of a value.
func savesLR(op, pair, single string) bool {
	if strings.HasPrefix(op, pair+" x29, x30,") {
		return true
	}
	if !strings.HasPrefix(op, pair+" ") && !strings.HasPrefix(op, single+" x30,") {
		return false
	}
	push := strings.Contains(op, "x30, [sp, #-") && strings.HasSuffix(op, "]!")
	return push || strings.Contains(op, "x30, [sp], #")
}

// signedBefore reports whether x30 still holds a signature from a paciasp
// earlier in the same block.
func signedBefore(lines []string, at int) bool {
	for j := at - 1; j >= 0; j-- {
		op := strings.TrimSpace(lines[j])
		if op == "paciasp" {
			return true
		}
		if op == "ret" || op == "autiasp" || op == "xpaclri" || strings.HasSuffix(op, ":") {
			return false
		}
		if strings.HasPrefix(op, "bl ") || strings.HasPrefix(op, "blr ") || strings.HasPrefix(op, "mov x30,") || savesLR(op, "ldp", "ldr") {
			return false
		}
	}
	return false
}

func authenticatedAfter(lines []string, at int) bool {
	for _, line := range lines[at+1:] {
		op := strings.TrimSpace(line)
		if op == "autiasp" {
			return true
		}
		if op == "ret" || strings.HasPrefix(op, "b ") || strings.HasPrefix(op, "br ") || strings.HasSuffix(op, ":") {
			return false
		}
	}
	return false
}

func TestHardenUnsignedFrames(t *testing.T) {
	cases := []struct {
		name, text string
		faults     int
	}{
		{"signed", "f:\n    paciasp\n    stp x29, x30, [sp, #-48]!\n    ldp x29, x30, [sp], #48\n    autiasp\n    ret\n", 0},
		{"signed-after-sub", "f:\n    paciasp\n    sub sp, sp, #224\n    stp x29, x30, [sp, #0]\n    ldp x29, x30, [sp, #0]\n    add sp, sp, #224\n    autiasp\n    ret\n", 0},
		{"local-entry", "f:\n    ret\n.Lgrow:\n    paciasp\n    stp x29, x30, [sp, #-64]!\n    ldp x29, x30, [sp], #64\n    autiasp\n    b .Lback\n", 0},
		{"startup", "_start:\n.Lkept:\n    stp x29, x30, [sp, #-16]!\n    bl __fn_main\n", 0},
		{"unsigned-save", "f:\n    stp x29, x30, [sp, #-48]!\n    ldp x29, x30, [sp], #48\n    autiasp\n    ret\n", 1},
		{"signature-of-another-function", "f:\n    paciasp\n    ret\ng:\n    stp x29, x30, [sp, #-16]!\n    ldp x29, x30, [sp], #16\n    autiasp\n    ret\n", 1},
		{"unauthenticated-ret", "f:\n    paciasp\n    stp x29, x30, [sp, #-48]!\n    ldp x29, x30, [sp], #48\n    ret\n", 1},
		{"unauthenticated-tail-call", "f:\n    paciasp\n    stp x29, x30, [sp, #-16]!\n    ldp x29, x30, [sp], #16\n    b g\n", 1},
		{"signed-pair", "f:\n    paciasp\n    stp x0, x30, [sp, #-16]!\n    bl g\n    ldp x0, x30, [sp], #16\n    autiasp\n    ret\n", 0},
		{"signed-single", "f:\n    paciasp\n    str x30, [sp, #-16]!\n    bl g\n    ldr x30, [sp], #16\n    autiasp\n    ret\n", 0},
		{"unsigned-pair", "f:\n    stp x19, x30, [sp, #-16]!\n    bl g\n    ldp x19, x30, [sp], #16\n    ret\n", 2},
		{"unsigned-single", "f:\n    str x30, [sp, #-16]!\n    bl g\n    ldr x30, [sp], #16\n    ret\n", 2},
		{"other-register-pair", "f:\n    stp x0, x1, [sp, #-16]!\n    ldp x0, x1, [sp], #16\n    ret\n", 0},
		{"signed-twice", "f:\n    paciasp\n    stp x29, x30, [sp, #-16]!\n    paciasp\n    stp x6, x30, [sp, #-16]!\n    bl g\n    ldp x6, x30, [sp], #16\n    autiasp\n    ldp x29, x30, [sp], #16\n    autiasp\n    ret\n", 1},
		{"stripped-then-signed", "f:\n    paciasp\n    stp x29, x30, [sp, #-16]!\n    xpaclri\n    paciasp\n    stp x6, x30, [sp, #-16]!\n    bl g\n    ldp x6, x30, [sp], #16\n    autiasp\n    ldp x29, x30, [sp], #16\n    autiasp\n    ret\n", 0},
		{"signed-after-call", "f:\n    paciasp\n    stp x29, x30, [sp, #-16]!\n    bl g\n    paciasp\n    str x30, [sp, #-16]!\n    bl h\n    ldr x30, [sp], #16\n    autiasp\n    ldp x29, x30, [sp], #16\n    autiasp\n    ret\n", 0},
		{"spilled-value", "f:\n    str x30, [sp, #112]\n    ldr x30, [sp, #112]\n    ret\n", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := unsignedFrames(tc.text); len(got) != tc.faults {
				t.Errorf("unsignedFrames = %q, want %d faults", got, tc.faults)
			}
		})
	}
}

func cpuEnforces(qemu, flag string) bool {
	if qemu != "" {
		return true
	}
	info, err := os.ReadFile("/proc/cpuinfo")
	return err == nil && bytes.Contains(info, []byte(flag))
}

func btiNote(t *testing.T, path string) bool {
	t.Helper()
	f, err := elf.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, p := range f.Progs {
		if p.Type != elf.ProgType(0x6474e553) {
			continue
		}
		b := make([]byte, p.Filesz)
		if _, err := p.ReadAt(b, 0); err != nil {
			t.Fatal(err)
		}
		for off := 16; off+12 <= len(b); off += 8 + int((binary.LittleEndian.Uint32(b[off+4:])+7)&^7) {
			if binary.LittleEndian.Uint32(b[off:]) == 0xc0000000 && binary.LittleEndian.Uint32(b[off+8:])&1 != 0 {
				return true
			}
		}
	}
	return false
}

func runHardened(t *testing.T, qemu, bin string) string {
	t.Helper()
	out, err := runArm64Bin(qemu, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", filepath.Base(bin), err, out)
	}
	return strings.TrimSpace(string(out))
}

func expectSIGILL(t *testing.T, qemu, bin string) {
	t.Helper()
	expectHardenSignal(t, qemu, bin, syscall.SIGILL)
}

func expectHardenSignal(t *testing.T, qemu, bin string, signals ...syscall.Signal) {
	t.Helper()
	out, err := withoutCoreDump(runArm64Bin(qemu, bin)).CombinedOutput()
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("%s: err=%v, want signals %v\n%s", filepath.Base(bin), err, signals, out)
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if ok && ws.Signaled() {
		for _, signal := range signals {
			if ws.Signal() == signal {
				return
			}
		}
	}
	t.Fatalf("%s: %v, want signals %v\n%s", filepath.Base(bin), err, signals, out)
}
