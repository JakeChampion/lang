package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// TestArm64DarwinAccountEntry asserts that `__account_entry` reaches
// libSystem's account database on arm64-darwin (#9815). Darwin keeps regular
// accounts and groups in Directory Services, not the files, so the files alone
// answered a uid with no name and `ls -l` printed `501` where GNU prints the
// user.
//
// On every host the image must bind the five libSystem functions the helper
// calls through. On Apple Silicon the program runs, and each answer must be
// the one Go's os/user gives for the same question.
func TestArm64DarwinAccountEntry(t *testing.T) {
	const prog = `// Every kind of __account_entry (#9815): getpwuid of the uid in argv[1] (0
// without one) and of one no
// account has, getpwnam("root")'s home, getgrgid(0)'s name, getgrnam("staff")'s
// gid, and getlogin(). Off Darwin every answer is 0 and the exit status says
// whether one was not.
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

function main(): i32 {
  let me: usize = __account_entry(0, argv_uid());
  let nobody: usize = __account_entry(0, 3999999999 as usize);
  let root: usize = __account_entry(1, key("root"));
  let wheel: usize = __account_entry(2, 0 as usize);
  let staff: usize = __account_entry(3, key("staff"));
  let login: usize = __account_entry(4, 0 as usize);
  if (target_os() != "darwin") {
    if (me == 0 as usize && nobody == 0 as usize && root == 0 as usize && wheel == 0 as usize && staff == 0 as usize && login == 0 as usize) {
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
	for _, sym := range []string{"_getpwuid", "_getpwnam", "_getgrgid", "_getgrnam", "_getlogin"} {
		if !e2eharness.MachOBindsSymbol(img, sym) {
			t.Errorf("the image's LC_DYLD_INFO_ONLY bind stream does not name %s", sym)
		}
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("execution check only runs on Apple Silicon")
	}
	got, err := exec.Command(out, strconv.Itoa(os.Getuid())).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := e2eharness.DarwinAccountAnswers(t); string(got) != want {
		t.Errorf("__account_entry answered %q, want %q", got, want)
	}
}
