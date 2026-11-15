package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The view of s.buf is lent to the call that consumes s. Holding buf for
// the call lets s move, so step updates its record and the inner list in
// place, as it does when the view is of a local holding the same array.
const lentViewProg = `import "std/i64";
struct Inner { xs: i64[], n: i64 }
struct St { buf: u8[], inner: Inner[] }

@noinline function (x: Inner) bumped(d: [u8]): Inner {
  return Inner { ...x, xs: x.xs.with(0, x.xs[0] + d.len() as i64), n: x.n + 1 };
}

@noinline function step(s: St, d: [u8]): St {
  return St { ...s, inner: s.inner.with(0, s.inner[0].bumped(d)) };
}

function main(): i32 {
  let s: St = St { buf: __alloc_u8(64), inner: [Inner { xs: [0 as i64, 0 as i64], n: 0 as i64 }] };
  s = step(s, s.buf[0:8]);
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  while (i < 1000) {
    s = step(s, s.buf[0:8]);
    i = i + 1;
  }
  let field: i64 = __heap_alloc_count() - before;
  let buf: u8[] = s.buf;
  before = __heap_alloc_count();
  i = 0;
  while (i < 1000) {
    s = step(s, buf[0:8]);
    i = i + 1;
  }
  let local: i64 = __heap_alloc_count() - before;
  if (s.inner[0].n != 2001 as i64 || s.inner[0].xs[0] != 16008 as i64 || s.buf.len() != 64) {
    print("wrong " + s.inner[0].n.to_string());
    return 1;
  }
  print(field.to_string() + " " + local.to_string());
  return 0;
}
`

// A lent slice's descriptor read by every kind of consumer a [u8] parameter
// reaches: the decoded reads, a slice of it lent on, the byte-view helpers,
// the byte scans and the expanding push, the bytes runtime, the string copy,
// the AES key schedule and the writer.
// On wasm each slice is a window in the caller's shadow-stack frame, so the
// output must match the native targets and the loop must allocate nothing.
const lentWindowProg = `import "std/i32";
import "std/i64";

@noinline function shape(d: [u8]): i32 {
  return d.len() * 1000000 + (d[0] as i32) * 1000 + (d[d.len() - 1] as i32);
}

@noinline function total(d: [u8]): i32 {
  let t: i32 = 0;
  for x in d {
    t = t + (x as i32);
  }
  return t;
}

@noinline function inner(d: [u8]): i32 {
  return total(d[1:d.len() - 1]);
}

@noinline function handed(d: [u8]): i32 {
  return shape(d) + inner(d);
}

@noinline function checks(d: [u8]): i32 {
  return __crc32_cksum_array(7, d) ^ __sum_bytes_array(d) ^ __bsd_sum_bytes(d, 3);
}

@noinline function differs(a: [u8], b: [u8]): i32 {
  return __mismatch_bytes(a, 0, b, 0, a.len());
}

// A set of the space alone, and the masks of one probe that takes 'F'.
const SPACE: u8[] = [0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8];

@noinline function scans(d: [u8], set: u8[], f: u8[]): i32 {
  return __memchr_bytes(d, 70, 0) * 1000000 + __rmemchr_bytes(d, 70, d.len()) * 10000 + __scan_set_bytes(d, 1, set) * 100 + __scan_masks_bytes(d, 0, f);
}

@noinline function pushed(d: [u8]): string {
  let t = __alloc_u8(256 * 8);
  t = t.with(32 * 8, 1u8).with(32 * 8 + 1, 95u8).with(70 * 8, 2u8).with(70 * 8 + 1, 60u8).with(70 * 8 + 2, 62u8);
  let h = buf_new(16);
  buf_push_bytes_expanded(h, d, t);
  let out = buf_take(h);
  buf_free(h);
  return out;
}

@noinline function copied(d: [u8]): i32 {
  let all: u8[] = __bytes_owned(d);
  let mid: u8[] = __bytes_range(d, 1, 3);
  let into: u8[] = __bytes_into(__alloc_u8(8), 2, d, 0, 4);
  return all.len() * 10000 + (mid[0] as i32) * 100 + (into[2] as i32);
}

@noinline function text(d: [u8]): string {
  return unsafe { string_from_bytes_unchecked(__bytes_owned(d)) };
}

@noinline function expanded(k: [u8]): i32 {
  let r: u8[] = __aes_expand_key(k);
  return r.len() * 1000 + (r[175] as i32);
}

@noinline function put(d: [u8]): i32 {
  match (stdout().write_bytes(d)) {
    Ok(_) => { return 0; },
    Err(_) => { return 1; },
  }
}

function main(): i32 {
  let buf: u8[] = __alloc_u8(64);
  for i in 0..64 {
    buf = buf.with(i, (65 + i % 26) as u8);
  }
  let s: [u8] = "the quick brown fox".as_bytes();
  print(shape(buf[3:9]).to_string() + " " + total(buf[3:9]).to_string() + " " + handed(buf[3:9]).to_string());
  print(shape(s[4:9]).to_string() + " " + handed(s[4:9]).to_string());
  print(checks(buf[2:40]).to_string() + " " + checks(s[1:12]).to_string());
  print(differs(buf[0:10], buf[26:36]).to_string() + " " + differs(buf[0:10], buf[1:11]).to_string() + " " + differs(s[4:9], buf[0:5]).to_string());
  let f = __alloc_u8(68).with(0, 1u8).with(4 + 6, 1u8).with(20 + 4, 1u8);
  print(scans(buf[3:40], SPACE, f).to_string() + " " + scans(s[2:19], SPACE, f).to_string() + " " + scans(s, __alloc_u8(0), f).to_string() + " " + scans(buf, SPACE, f).to_string());
  print(pushed(buf[3:9]) + "|" + pushed(s[10:19]) + "|" + pushed(buf[0:0]));
  print(copied(buf[5:12]).to_string() + " " + copied(s[10:15]).to_string());
  print(text(buf[0:6]) + "|" + text(s[4:9]));
  print(expanded(buf[16:32]).to_string());
  put(buf[20:26]);
  put(s[15:19]);
  print("");
  let before: i64 = __heap_alloc_count();
  let acc: i32 = 0;
  for i in 0..1000 {
    acc = acc + handed(buf[i % 8:i % 8 + 20]) + checks(s[1:12]) + differs(buf[0:10], s[2:12]) + scans(buf[i % 8:i % 8 + 20], SPACE, f) % 1000;
  }
  print((__heap_alloc_count() - before).to_string() + " " + acc.to_string());
  return 0;
}
`

const lentWindowOut = `6068073 423 6068355
5113107 5113428
2022080422 12064917
10 0 0
2283702 -1009901 -1008101 5576405
<>|_|
77170 61498
ABCDEF|quick
176041
UVWXYZ fox
0 2070135803
`

var lentViewLeak = regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)

// runLentView compiles prog for target, sanitized or not, and returns its
// stdout and stderr.
func runLentView(t *testing.T, cli *selfHostCLI, prog, target string, sanitize bool) (string, string) {
	t.Helper()
	var flags []string
	if sanitize {
		flags = append(flags, "-sanitize")
	}
	bin := compileCPUProfile(t, cli, prog, target, "", flags...)
	var cmd *exec.Cmd
	switch target {
	case "x86-64-linux":
		cmd = runX86_64Bin(cli.runner, bin)
	case "arm64-linux":
		_, qemu := arm64Tooling(t)
		cmd = runArm64Bin(qemu, bin)
	default:
		cmd = exec.Command("wasmtime", "run", bin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("sanitize=%v: %v: stdout %q; stderr %s", sanitize, err, &stdout, &stderr)
	}
	if sanitize {
		if m := lentViewLeak.FindStringSubmatch(stderr.String()); len(m) != 3 || m[1] != m[2] {
			t.Fatalf("unbalanced sanitized run: %s", &stderr)
		}
	}
	return stdout.String(), stderr.String()
}

func TestSelfHostLentViewLetsItsRecordMove(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if got, _ := runLentView(t, cli, lentViewProg, target, false); got != "0 0\n" {
				t.Errorf("1000 calls through the field and through a local allocate %q, want %q", got, "0 0\n")
			}
			runLentView(t, cli, lentViewProg, target, true)
		})
	}
}

func TestSelfHostLentWindowConsumers(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				if got, _ := runLentView(t, cli, lentWindowProg, target, sanitize); got != lentWindowOut {
					t.Errorf("sanitize=%v: got\n%s\nwant\n%s", sanitize, got, lentWindowOut)
				}
			}
		})
	}
}

// A function whose parameter is a byte view is spliced into its one caller,
// as one whose parameter is a str is. skip rebinds the view in a loop. After
// the spliced loop, main reads its own view, and each view that the loop
// made is released.
const byteViewSpliceProg = `import "std/i32";

function skip(s: [u8], n: i32): i32 {
  let v = s;
  let k = 0;
  while (k < n && v.len() > 0) {
    v = v[1:v.len()];
    k += 1;
  }
  return v.len() * 1000 + v[0] as i32;
}

function main(): i32 {
  let held: u8[] = ("abcde" + "fghij").bytes();
  let part: [u8] = held[2:9];
  let r = skip(part, 3);
  let churn: string[] = [];
  for i in 0..200 {
    churn = churn.append("c" + i.to_string());
  }
  print(r.to_string() + " " + part.len().to_string() + " " + (part[0] as i32).to_string() + " " + churn.len().to_string());
  return 0;
}
`

func TestSelfHostByteViewParamIsSpliced(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "splice.fern")
	if err := os.WriteFile(src, []byte(byteViewSpliceProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out := filepath.Join(dir, target+".s")
		if combined, err := exec.Command(h.cli, "-target", target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(asm), "__fn_skip:") {
			t.Errorf("%s: skip, which takes a [u8], is not spliced into its one caller", target)
		}
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				if got, _ := runLentView(t, cli, byteViewSpliceProg, target, sanitize); got != "4102 7 99 200\n" {
					t.Errorf("sanitize=%v: got %q, want %q", sanitize, got, "4102 7 99 200\n")
				}
			}
		})
	}
}
