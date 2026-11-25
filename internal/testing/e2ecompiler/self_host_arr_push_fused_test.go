package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Chains of byte appends of 2, 3, 5 (across an inlined call) and 9, a byte
// that is read again after its chain, an array with a second holder, and
// arrays that grow from empty. The output is the interpreter's.
const arrPushFusedProg = `function two(own out: u8[], v: i32): u8[] {
  return out.append((v & 255) as u8).append(((v >> 8) & 255) as u8);
}
function three(own out: u8[], v: i32): u8[] {
  return out.append((v & 255) as u8).append(((v >> 8) & 255) as u8).append(((v >> 16) & 255) as u8);
}
function nine(own out: u8[], v: i32): u8[] {
  let a = (v & 255) as u8;
  return out.append(a).append(1u8).append(a).append(3u8).append(((v >> 3) & 255) as u8).append(5u8).append(a).append(7u8).append(8u8);
}
function reuse(own out: u8[], v: i32): (u8[], i32) {
  let a = (v & 127) as u8;
  let b = ((v >> 7) & 127) as u8;
  let next = out.append(a).append(b).append(a);
  return (next, (a as i32) + (b as i32));
}
function sum(xs: u8[]): i64 {
  let h = 1469598103i64;
  for x in xs {
    h = (h * 31i64 + (x as i64)) % 1000000007i64;
  }
  return h;
}
function main(): i32 {
  let out: u8[] = [];
  let extra = 0;
  for i in 0..20000 {
    out = two(out, i * 7);
    out = three(out, i * 13);
    out = nine(out, i);
    let (o, e) = reuse(out, i * 3);
    out = o;
    extra += e;
  }
  print(out.len().to_string());
  print(sum(out).to_string());
  print(extra.to_string());
  let base: u8[] = [9u8, 8u8];
  let keep = base;
  let grown = three(base, 66051);
  print(keep.len().to_string() + " " + grown.len().to_string() + " " + sum(keep).to_string() + " " + sum(grown).to_string());
  let small: u8[] = [];
  for i in 0..7 {
    small = nine(small, i);
  }
  print(sum(small).to_string());
  let pair = two(small, extra);
  print(pair.len().to_string() + " " + sum(pair).to_string());
  return 0;
}
`

const arrPushFusedWant = "340000\n392839761\n2461236\n2 5 283767386 714140101\n14998924\n65 413967620\n"

// A chain of byte appends is one fused append: one room check, one store of
// the packed bytes and one update of the length, with one call on the slow
// path (ssa.fuse_byte_pushes). The bytes of two and three are the low bytes
// of one value, so that value is stored as it is. The bytes of nine are
// packed first.
func TestSelfHostArrPushFused(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "fused.fern")
	if err := os.WriteFile(src, []byte(arrPushFusedProg), 0o644); err != nil {
		t.Fatal(err)
	}
	forms := map[string][]string{
		"x86-64-linux": {"movl %eax, 8(%rdi,%rdx)", "movw %ax, 8(%rdi,%rdx)", "movb %sil, %al", "call __fern_arr_pushn_owned_u8", "__fern_arr_pushn_owned_u8:"},
		"arm64-linux":  {"str w1, [x3, #8]", "strh w1, [x3, #8]", "bfi w1, w2, #8, #8", "bl __fern_arr_pushn_owned_u8", "__fern_arr_pushn_owned_u8:"},
	}
	for target, want := range forms {
		out := filepath.Join(dir, target+".s")
		if combined, err := exec.Command(h.cli, "-target", target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range want {
			if !strings.Contains(string(asm), w) {
				t.Errorf("%s: the listing has no %q", target, w)
			}
		}
	}
	for _, tg := range h.targets {
		for _, slow := range []bool{false, true} {
			bin := filepath.Join(dir, tg.target+".bin")
			cmd := exec.Command(h.cli, "-target", tg.target, "-o", bin, src, h.stdlib)
			if slow {
				cmd.Env = append(os.Environ(), "FERN_RC_FREE_DEBUG=1")
			}
			if combined, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
			}
			run := exec.Command(bin)
			if len(tg.runner) > 0 {
				run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
			}
			got, err := run.Output()
			if err != nil {
				t.Errorf("%s (slow path only %v): %v", tg.target, slow, err)
			}
			if string(got) != arrPushFusedWant {
				t.Errorf("%s (slow path only %v): output\n%s\nwant\n%s", tg.target, slow, got, arrPushFusedWant)
			}
		}
	}
}
