package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const accountEntryProgram = `// Every kind of __account_entry (#9815): getpwuid of the uid in argv[1] (0
// without one) and of one no
// account has, getpwnam("root")'s home, getgrgid(0)'s name, getgrnam("staff")'s
// gid, getlogin(), the first gid getgrouplist gives argv[1]'s user,
// strerror(EINVAL) and whether __error() answers. Off Darwin every answer is 0
// and the exit status says whether one was not.
import "std/i32";

function c_string(p: usize): string {
  if (p == 0 as usize) { return "none"; }
  let bs: u8[] = [];
  let at: usize = p;
  while (__load_u8(at) != 0) {
    bs = bs.append(__load_u8(at) as u8);
    at = at + 1 as usize;
  }
  return string_from_bytes_unchecked(bs);
}

function field(p: usize, off: i32): string {
  if (p == 0 as usize) { return "none"; }
  return c_string(__load_ptr(p + off as usize));
}

function key(s: string): usize {
  let p: usize = __alloc(s.len() + 1);
  let i: i32 = 0;
  while (i < s.len()) {
    __store_u8(p + i as usize, s[i] as i32);
    i = i + 1;
  }
  __store_u8(p + s.len() as usize, 0);
  return p;
}

function argv_uid(): usize {
  let a: string[] = args();
  let v: usize = 0 as usize;
  if (a.len() < 2) { return v; }
  let i: i32 = 0;
  while (i < a[1].len()) {
    v = v * 10 as usize + (a[1][i] as i32 - 48) as usize;
    i = i + 1;
  }
  return v;
}

function first_group(name: string, gid: i32): string {
  let block: usize = __alloc(32);
  let buf: usize = __alloc(256);
  let count: usize = __alloc(8);
  __store_ptr(block, key(name));
  __store_i64(block + 8 as usize, gid as i64);
  __store_ptr(block + 16 as usize, buf);
  __store_ptr(block + 24 as usize, count);
  __store_i64(count, 64 as i64);
  let rc: i64 = __account_entry(5, block) as i64 & 4294967295 as i64;
  if (rc == 4294967295 as i64 || __load_i32(count) < 1) { return "none"; }
  return __load_i32(buf).to_string();
}

function main(): i32 {
  let me: usize = __account_entry(0, argv_uid());
  let nobody: usize = __account_entry(0, 3999999999 as usize);
  let root: usize = __account_entry(1, key("root"));
  let wheel: usize = __account_entry(2, 0 as usize);
  let staff: usize = __account_entry(3, key("staff"));
  let login: usize = __account_entry(4, 0 as usize);
  let einval: usize = __account_entry(6, 22 as usize);
  let errno_at: usize = __account_entry(7, 0 as usize);
  if (target_os() != "darwin") {
    if (me == 0 as usize && nobody == 0 as usize && root == 0 as usize && wheel == 0 as usize && staff == 0 as usize && login == 0 as usize && einval == 0 as usize && errno_at == 0 as usize && __account_entry(5, 0 as usize) == 0 as usize) {
      return 0;
    }
    return 1;
  }
  print(field(me, 0));
  print(field(nobody, 0));
  print(field(root, 48));
  print(field(wheel, 0));
  if (staff == 0 as usize) {
    print("none");
  } else {
    print(__load_i32(staff + 16 as usize).to_string());
  }
  print(c_string(login));
  print(first_group(field(me, 0), __load_i32(me + 20 as usize)));
  print(c_string(einval));
  if (errno_at == 0 as usize) { print("none"); } else { print("errno"); }
  return 0;
}
`

// TestSelfHostAccountEntry pins `__account_entry` under the self-host. Linux
// and WASI keep every account in the files a caller reads, so the builtin
// answers 0 there and the program exits 0. An arm64-darwin image binds the
// five libSystem functions its helper calls through.
func TestSelfHostAccountEntry(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, accountEntryProgram, target, "FERN_STRICT_IR=1")
			if code != 0 {
				t.Fatalf("exit = %d, want 0 (every lookup answers 0 here)\n%s", code, stderr)
			}
		})
	}
	t.Run("arm64-darwin-image", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "main.fern")
		if err := os.WriteFile(src, []byte(accountEntryProgram), 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, "prog")
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "arm64-darwin", "-o", out, src, cli.stdlib)
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("self-host -target arm64-darwin: %v\n%s", err, msg)
		}
		img, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		for _, sym := range []string{"_getpwuid", "_getpwnam", "_getgrgid", "_getgrnam", "_getlogin", "_getgrouplist", "_strerror", "___error"} {
			if !e2eharness.MachOBindsSymbol(img, sym) {
				t.Errorf("the image's LC_DYLD_INFO_ONLY bind stream does not name %s", sym)
			}
		}
	})
}

// TestSelfHostArm64DarwinAccountEntry runs the same program as an Apple
// Silicon binary the self-host built natively, against what Go's os/user
// reports for the same questions.
func TestSelfHostArm64DarwinAccountEntry(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	src := filepath.Join(dir, "names.fern")
	if err := os.WriteFile(src, []byte(accountEntryProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "names")
	if out, err := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, stdlib).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	got, _ := exec.Command(bin, strconv.Itoa(os.Getuid())).Output()
	if want := e2eharness.DarwinAccountAnswers(t); string(got) != want {
		t.Errorf("__account_entry answered %q, want %q", got, want)
	}
}
