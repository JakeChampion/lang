package e2ecompiler

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const selfHostAppendReportSrc = `import "std/i32";

struct Acc { items: i32[], n: i32 }

@noinline
function sole(n: i32): i32[] {
  let xs: i32[] = [];
  let i: i32 = 0;
  while (i < n) {
    xs = xs.append(i);
    i = i + 1;
  }
  return xs;
}

@noinline
function preserved(xs: i32[], n: i32): boolean {
  return xs.len() == n && (n == 0 || xs[n - 1] == n - 1);
}

@noinline
function live(n: i32): i32 {
  let xs: i32[] = [];
  let i: i32 = 0;
  while (i < n) {
    let keep: i32[] = xs;
    xs = xs.append(i);
    if (!preserved(keep, i)) {
      return 7;
    }
    i = i + 1;
  }
  return xs.len();
}

@noinline
function nested(xs: i32[][]): i32[] {
  return xs[0].append(1);
}

@noinline
function chained(xs: i32[]): i32[] {
  let a: i32[] = xs.append(1);
  return a.append(2);
}

@noinline
function from_field(a: Acc): i32[] {
  return a.items.append(1);
}

@noinline
function grow_owned(own a: Acc): Acc {
  return Acc { ...a, items: a.items.append(9) };
}

function main(): i32 {
  let c0: i32 = __arr_push_shared_count();
  let s: i32 = sole(100).len();
  let c1: i32 = __arr_push_shared_count();
  let l: i32 = live(100);
  let c2: i32 = __arr_push_shared_count();
  let m: i32[][] = [sole(10), [2]];
  let k: i32 = nested(m).len();
  let c3: i32 = __arr_push_shared_count();
  if (c1 != c0 || c2 - c1 < 50 || c3 - c2 != 1) {
    print((c1 - c0).to_string() + " " + (c2 - c1).to_string() + " " + (c3 - c2).to_string());
    return 1;
  }
  let a: Acc = Acc { items: [1], n: 0 };
  return s + l + k + chained([2]).len() + from_field(a).len() + grow_owned(a).items.len() - 218;
}
`

func TestSelfHostAppendReport(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(selfHostAppendReportSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	compile := func(report string, flags ...string) (string, string) {
		t.Helper()
		asm := filepath.Join(t.TempDir(), "out.s")
		args := append(append([]string{}, flags...), "-target", "x86-64-linux", "-emit", "asm", "-o", asm, src, cli.stdlib)
		cmd := runX86_64Bin(cli.runner, cli.bin, args...)
		cmd.Env = append(os.Environ(), "FERN_ARRAY_REPORT="+report)
		msg, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("compile %v: %v\n%s", flags, err, msg)
		}
		body, err := os.ReadFile(asm)
		if err != nil {
			t.Fatal(err)
		}
		return string(body), string(msg)
	}
	asm, report := compile("1", "-g")
	quietASM, quiet := compile("0", "-g")
	if quiet != "" || quietASM != asm {
		t.Fatal("reporting changed emitted code or printed while disabled")
	}
	rows := appendReportRows(t, report)
	for _, tc := range []struct {
		owner   string
		nth     int
		reason  string
		storage string
		names   string
	}{
		{"sole", 0, "sole-owner", "in-place", "(xs)"},
		{"live", 0, "receiver-still-live", "copy", "(xs, keep)"},
		{"nested", 0, "receiver-retained", "copy", ""},
		{"chained", 0, "receiver-borrowed", "unique-grow/shared-copy", "(xs)"},
		{"chained", 1, "borrow-linked", "unique-grow/shared-copy", "(a)"},
		{"from_field", 0, "field-grow", "unique-grow/shared-copy", ""},
		{"grow_owned", 0, "guarded", "unique-grow/shared-copy", ""},
	} {
		got := rows[tc.owner]
		if tc.nth >= len(got) {
			t.Fatalf("%s: want append site %d, have %d\n%s", tc.owner, tc.nth, len(got), report)
		}
		row := got[tc.nth]
		line := appendReportSourceLine(t, tc.owner, tc.nth)
		if !strings.Contains(row, " reason="+tc.reason+" storage="+tc.storage) || !strings.Contains(row, " line="+strconv.Itoa(line)+":") {
			t.Errorf("%s site %d: want reason=%s storage=%s with a line, got %q", tc.owner, tc.nth, tc.reason, tc.storage, row)
		}
		if tc.names != "" && !strings.Contains(row, " "+tc.names+" ") {
			t.Errorf("%s site %d: want bindings %s, got %q", tc.owner, tc.nth, tc.names, row)
		}
	}
	if out, err := runX86_64Bin(cli.runner, buildBin(t, cli.gcc, t.TempDir(), "append-report", asm)).CombinedOutput(); err != nil {
		t.Fatalf("reported program: %v\n%s", err, out)
	}
	_, plain := compile("1")
	for owner, got := range appendReportRows(t, plain) {
		for _, row := range got {
			if strings.Contains(row, " line=") || strings.Contains(row, "(") {
				t.Errorf("%s: a build without -g named a position: %q", owner, row)
			}
		}
	}
}

func appendReportSourceLine(t *testing.T, owner string, nth int) int {
	t.Helper()
	inside := false
	for i, line := range strings.Split(selfHostAppendReportSrc, "\n") {
		if strings.HasPrefix(line, "function ") {
			inside = strings.HasPrefix(line, "function "+owner+"(")
		}
		if inside && strings.Contains(line, ".append(") {
			if nth == 0 {
				return i + 1
			}
			nth--
		}
	}
	t.Fatalf("no append source position for %s", owner)
	return 0
}

func appendReportRows(t *testing.T, report string) map[string][]string {
	t.Helper()
	header := "array report: primary Fern compiler, append storage\n"
	start := strings.Index(report, header)
	if start < 0 {
		t.Fatalf("missing append storage section:\n%s", report)
	}
	lines := strings.Split(report[start+len(header):], "\n")
	rows := map[string][]string{}
	for i := 0; i+1 < len(lines); i++ {
		owner, ok := strings.CutSuffix(lines[i], ": append; ownership lowering")
		if !ok {
			continue
		}
		rows[owner] = append(rows[owner], lines[i+1])
	}
	return rows
}
