package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Each mode runs one primitive with a key of 0x41 bytes and then scans the
// arena the primitive used for the key material it copied: the HMAC pads
// (the key XOR 0x36 and 0x5c), the clamped X25519 scalar, the ChaCha20
// one-time Poly1305 key, the Ed25519 nonce hash, and the round keys of an
// AES-GCM key that was erased. The scan starts at a probe block taken before
// the primitive ran and covers every byte the bump pointer moved past, so a
// released copy anywhere in that window is found. The plant mode releases a
// block of the HMAC inner pad pattern without erasing it, which shows that
// the scan sees a stale copy.
const cryptoEraseSrc = `import "std/crypto";
import "std/crypto/aes_gcm";
import "std/crypto/chacha20poly1305";
import "std/crypto/ed25519";
import "std/crypto/ecdsa";
import "std/crypto/x25519";
function filled(n: i32, v: u8): u8[] {
  let b: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    b = b.with(i, v);
    i = i + 1;
  }
  return b;
}
function count_run(from: usize, len: i64, v: u8, run: i32): i32 {
  unsafe {
    let found: i32 = 0;
    let streak: i32 = 0;
    let i: i64 = 0;
    while (i < len) {
      if (__load_u8(from + (i as usize)) == v) {
        streak = streak + 1;
        if (streak == run) { found = found + 1; }
      } else {
        streak = 0;
      }
      i = i + 1;
    }
    return found;
  }
}
function count_seq(from: usize, len: i64, needle: [u8]): i32 {
  unsafe {
    let found: i32 = 0;
    let n: i32 = needle.len();
    let i: i64 = 0;
    while (i + (n as i64) <= len) {
      let j: i32 = 0;
      while (j < n && __load_u8(from + (i as usize) + (j as usize)) == needle[j]) {
        j = j + 1;
      }
      if (j == n) { found = found + 1; }
      i = i + 1;
    }
    return found;
  }
}
// The bytes of a raw block of one size class that are not zero. After a
// release the class hands the last released block back first, so the block
// holds what the erased or dropped box left.
function nonzero_in_class(size: i32): i32 {
  unsafe {
    let p: usize = __alloc(size);
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < size) {
      if (__load_u8(p + (i as usize)) != 0 as u8) { n = n + 1; }
      i = i + 1;
    }
    return n;
  }
}
function aes_key(erase: boolean): i32 {
  let Ok(k) = aes_gcm.key(filled(16, 65 as u8)) else { return -1; };
  if (erase) { k.erase(); }
  return 0;
}
function ed25519_key(erase: boolean): i32 {
  let Ok(k) = ed25519.private_key(filled(32, 65 as u8)) else { return -1; };
  if (erase) { k.erase(); }
  return 0;
}
function ecdsa_key(erase: boolean): i32 {
  // A read-only input avoids a separate caller-owned copy in the scan.
  let Ok(k) = ecdsa.private_key(ecdsa.P256, "DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD".as_bytes()) else { return -1; };
  if (erase) { k.erase(); }
  return 0;
}
// Runs the mode's primitive. The result is 0 when the primitive answered
// what the mode expects.
function run(mode: string, key: [u8], msg: [u8], nonce12: [u8]): i32 {
  if (mode == "plant") {
    let pad: u8[] = filled(32, 119 as u8);
    return pad.len() - 32;
  }
  if (mode == "hmac") {
    return crypto.hmac_sha256(key, msg).len() - 32;
  }
  if (mode == "hkdf") {
    let okm: u8[] = crypto.hkdf_sha256_expand(key, msg, 80);
    let n: i32 = okm.len();
    crypto.erase(okm);
    return n - 80;
  }
  if (mode == "x25519") {
    let Ok(pk) = x25519.public_key(key) else { return 9; };
    return pk.len() - 32;
  }
  if (mode == "chacha") {
    let Ok(k) = chacha20poly1305.key(key) else { return 9; };
    let Ok(sealed) = k.seal(nonce12, msg, []) else { return 9; };
    return sealed.len() - 56;
  }
  if (mode == "ed25519") {
    let Ok(k) = ed25519.private_key(key) else { return 9; };
    return k.sign(msg).len() - 64;
  }
  if (mode == "aes-key") { return aes_key(true); }
  if (mode == "aes-key-drop") { return aes_key(false); }
  if (mode == "ed25519-key") { return ed25519_key(true); }
  if (mode == "ed25519-key-drop") { return ed25519_key(false); }
  if (mode == "ecdsa-key") { return ecdsa_key(true); }
  if (mode == "ecdsa-key-drop") { return ecdsa_key(false); }
  return 7;
}
function copy_of(b: [u8], from: i32, n: i32): u8[] {
  return __bytes_into(__alloc_u8(n), 0, b, from, n);
}
// The byte strings the mode's primitive copies, computed before the primitive
// runs so that no needle takes a block the primitive released.
function needles_of(mode: string, key: [u8], msg: [u8], nonce12: [u8]): u8[][] {
  if (mode == "hkdf") {
    let okm: u8[] = crypto.hkdf_sha256_expand(key, msg, 80);
    let out: u8[][] = [copy_of(okm, 0, 32), copy_of(okm, 32, 32)];
    crypto.erase(okm);
    return out;
  }
  if (mode == "x25519") {
    return [filled(32, 65 as u8).with(0, 64 as u8)];
  }
  if (mode == "chacha") {
    let Ok(k) = chacha20poly1305.key(key) else { return []; };
    let Ok(otk) = k.chacha20_xor(nonce12, 0, filled(32, 0 as u8)) else { return []; };
    return [otk];
  }
  if (mode == "ed25519") {
    let expanded: u8[] = crypto.sha512(key);
    let nonce: u8[] = crypto.sha512_new().update(expanded[32:64]).update(msg).finish();
    crypto.erase(expanded);
    return [nonce];
  }
  if (mode == "aes-key" || mode == "aes-key-drop") {
    return [__aes_expand_key(filled(16, 65 as u8))];
  }
  if (mode == "ed25519-key" || mode == "ed25519-key-drop") {
    return [crypto.sha512(key)];
  }
  if (mode == "ecdsa-key" || mode == "ecdsa-key-drop") {
    return [filled(32, 68 as u8)];
  }
  return [];
}
function main(): i32 {
  unsafe {
    let probe: usize = __alloc(8);
    let before: i64 = __heap_bump_bytes();
    let mode: string = "plant";
    if (args().len() > 1) { mode = args()[1]; }
    let key: u8[] = filled(32, 65 as u8);
    let msg: u8[] = filled(40, 66 as u8);
    let nonce12: u8[] = filled(12, 67 as u8);
    let needles: u8[][] = needles_of(mode, key, msg, nonce12);
    if (run(mode, key, msg, nonce12) != 0) { return 8; }
    // The window covers every byte the bump pointer moved past since the
    // probe, so it holds the blocks the primitive used and released. Each
    // needle is live and is found once in its own block.
    let len: i64 = __heap_bump_bytes() - before + 64;
    let stale: i32 = 0;
    if (mode == "plant" || mode == "hmac") {
      stale = count_run(probe, len, 119 as u8, 32) + count_run(probe, len, 29 as u8, 32);
    } else {
      for n in needles {
        stale = stale + count_seq(probe, len, n) - 1;
      }
    }
    print(stale.to_string());
    return 0;
  }
}
`

func TestSelfHostPrimitivesEraseKeyMaterial(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(src, []byte(cryptoEraseSrc), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "main")
			args := []string{"-target", target, "-o", bin, src, cli.stdlib}
			cmd := runX86_64Bin(cli.runner, cli.bin, args...)
			cmd.Env = childEnv()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("self-host CLI %v: %v\n%s", args, err, out)
			}
			run := func(mode string) int {
				t.Helper()
				var c *exec.Cmd
				if target == "arm64-linux" {
					c = runArm64Bin(qemu, bin, mode)
				} else {
					c = runX86_64Bin(cli.runner, bin, mode)
				}
				out, err := c.CombinedOutput()
				if err != nil {
					t.Fatalf("%s: %v\n%s", mode, err, out)
				}
				n, err := strconv.Atoi(strings.TrimSpace(string(out)))
				if err != nil {
					t.Fatalf("%s printed %q", mode, out)
				}
				return n
			}
			if got := run("plant"); got < 1 {
				t.Fatalf("plant: the scan found %d stale copies of a released block, want at least 1: the scan does not see the arena", got)
			}
			for _, mode := range []string{"hmac", "hkdf", "x25519", "chacha", "ed25519"} {
				if got := run(mode); got != 0 {
					t.Errorf("%s: %d stale copies of key material in the arena after the primitive returned, want 0", mode, got)
				}
			}
			for _, mode := range []string{"aes-key", "ed25519-key", "ecdsa-key"} {
				if got := run(mode + "-drop"); got < 1 {
					t.Fatalf("%s-drop: a dropped key left %d copies of its secret in the arena, want at least 1: the control does not see the dropped key", mode, got)
				}
				if got := run(mode); got != 0 {
					t.Errorf("%s: %d copies of the erased key's secret in the arena, want 0", mode, got)
				}
			}
		})
	}
}
