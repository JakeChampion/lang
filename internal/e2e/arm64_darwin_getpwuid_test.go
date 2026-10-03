package e2e

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"testing"
)

// TestArm64DarwinGetpwuidName asserts that `__getpwuid_name` reaches
// libSystem's getpwuid(3) on arm64-darwin (#9815). Darwin keeps regular
// accounts in Directory Services, not /etc/passwd, so the files alone answer
// a uid with no name and `ls -l` printed `501` where GNU prints the user.
//
// On every host the image must bind `_getpwuid` from libSystem: that is the
// slot the helper calls through. On Apple Silicon the program runs, and the
// current uid's name must be the one the OS reports, while a uid no account
// has must answer 0, which the program prints as `none`.
func TestArm64DarwinGetpwuidName(t *testing.T) {
	const prog = `function name_of(uid: u32): string {
  let p: usize = __getpwuid_name(uid);
  if (p == 0 as usize) { return "none"; }
  let bs: u8[] = [];
  let at: usize = p;
  while (__load_u8(at) != 0) {
    bs = bs.append(__load_u8(at) as u8);
    at = at + 1 as usize;
  }
  return string_from_bytes_unchecked(bs);
}

function main(): i32 {
  print(name_of(getuid()));
  print(name_of(3999999999 as u32));
  return 0;
}
`
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
	}
	img, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bindsSymbol(img, "_getpwuid") {
		t.Fatalf("the image's LC_DYLD_INFO_ONLY bind stream does not name _getpwuid")
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("execution check only runs on Apple Silicon")
	}
	me, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current: %v", err)
	}
	got, err := exec.Command(out).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := me.Username + "\nnone\n"; string(got) != want {
		t.Errorf("getpwuid answered %q, want %q", got, want)
	}
}

// bindsSymbol reports whether a Mach-O image's LC_DYLD_INFO_ONLY bind stream
// names `sym` (BIND_OPCODE_SET_SYMBOL_TRAILING_FLAGS_IMM carries it inline).
func bindsSymbol(img []byte, sym string) bool {
	if len(img) < 32 {
		return false
	}
	ncmds := binary.LittleEndian.Uint32(img[16:])
	off := 32
	for i := uint32(0); i < ncmds && off+8 <= len(img); i++ {
		cmd := binary.LittleEndian.Uint32(img[off:])
		size := int(binary.LittleEndian.Uint32(img[off+4:]))
		if cmd == 0x80000022 && off+24 <= len(img) {
			bindOff := int(binary.LittleEndian.Uint32(img[off+16:]))
			bindLen := int(binary.LittleEndian.Uint32(img[off+20:]))
			if bindLen == 0 || bindOff+bindLen > len(img) {
				return false
			}
			return bytes.Contains(img[bindOff:bindOff+bindLen], append([]byte(sym), 0))
		}
		off += size
	}
	return false
}
