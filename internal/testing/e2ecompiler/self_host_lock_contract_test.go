package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/mvs"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostLockParseParity(t *testing.T) {
	const header = "[[package]]\n"
	const fields = "name = \"lib\"\nversion = \"1.2.3\"\n"
	const valid = header + fields + "path = \"../lib\"\n"
	cases := []struct{ name, source string }{
		{"empty", ""},
		{"comments", "# lock\n\n# no packages\n"},
		{"path", valid},
		{"archive", header + fields + "url = \"https://example.invalid/lib\"\nhash = \"sha256:" + strings.Repeat("a", 64) + "\"\n"},
		{"zero-version", header + "name = \"lib\"\nversion = \"0.0.0\"\npath = \"lib\"\n"},
		{"leading-zero-version", header + "name = \"lib\"\nversion = \"001.02.0003\"\n"},
		{"largest-version", header + "name = \"lib\"\nversion = \"9223372036854775807.0.0\"\n"},
		{"crlf", strings.ReplaceAll(valid, "\n", "\r\n")},
		{"trailing-comments", header + "name = \"lib#x\" # name\nversion = \"1.2.3\" # version\n"},
		{"unicode-path", header + fields + "path = \"café\"\n"},
		{"empty-entry", header},
		{"missing-name", header + "version = \"1.2.3\"\n"},
		{"empty-name", header + "name = \"\"\nversion = \"1.2.3\"\n"},
		{"missing-version", header + "name = \"lib\"\n"},
		{"missing-version-after-valid", valid + header + "name = \"other\"\n"},
		{"incomplete-before-next-header", header + "name = \"lib\"\n" + valid},
		{"outside-entry", fields},
		{"malformed-header", "[[package]\n" + fields},
		{"missing-equals", header + fields + "path\n"},
		{"unknown-key", header + fields + "other = \"x\"\n"},
		{"quoted-key", header + fields + "\"path\" = \"x\"\n"},
		{"unquoted", header + fields + "path = lib\n"},
		{"unterminated", header + fields + "path = \"lib\n"},
		{"escaped", header + fields + "path = \"a\\b\"\n"},
		{"embedded-quote", header + fields + "path = \"a\"b\"\n"},
		{"trailing-value", header + fields + "path = \"a\" junk\n"},
		{"short-version", header + "name = \"lib\"\nversion = \"1.2\"\n"},
		{"negative-version", header + "name = \"lib\"\nversion = \"-1.2.3\"\n"},
		{"empty-version", header + "name = \"lib\"\nversion = \"\"\n"},
		{"non-numeric-version", header + "name = \"lib\"\nversion = \"1.x.3\"\n"},
		{"overflow-version", header + "name = \"lib\"\nversion = \"9223372036854775808.0.0\"\n"},
		{"invalid-hash-after-valid", valid + header + fields + "hash = \"sha256:bad\"\n"},
		{"duplicate-name-last-wins", valid + header + fields + "url = \"https://example.invalid/new\"\n"},
		{"duplicate-field-last-wins", header + fields + "path = \"old\"\npath = \"new\"\n"},
		{"incomplete-duplicate", valid + header + "name = \"lib\"\n"},
		{"index-serial", "index_serial = 42\n" + valid},
		{"index-serial-zero", "index_serial = 0 # first\n" + valid},
		{"index-serial-largest", "index_serial = 999999999999999999\n" + valid},
		{"index-serial-only", "index_serial = 7\n"},
		{"index-serial-leading-zero", "index_serial = 07\n" + valid},
		{"index-serial-quoted", "index_serial = \"7\"\n" + valid},
		{"index-serial-negative", "index_serial = -7\n" + valid},
		{"index-serial-too-long", "index_serial = 1000000000000000000\n" + valid},
		{"index-serial-empty", "index_serial =\n" + valid},
		{"index-serial-twice", "index_serial = 1\nindex_serial = 2\n" + valid},
		{"index-serial-in-entry", valid + "index_serial = 7\n"},
	}
	var large strings.Builder
	for i := range 200 {
		fmt.Fprintf(&large, "[[package]]\nname = \"lib%d\"\nversion = \"1.0.0\"\npath = \"old%d\"\n", i, i)
	}
	cases = append(cases, struct{ name, source string }{"many-entries", large.String()})
	large.WriteString(header + "name = \"lib42\"\nversion = \"2.0.0\"\npath = \"new\"\n")
	cases = append(cases, struct{ name, source string }{"nonadjacent-duplicate", large.String()})
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern_toml.fern")
	var src strings.Builder
	src.WriteString(`import "./fern_toml";
import "./docsummary";
import "./util";
function after(a: fern_toml.LockEntry, b: fern_toml.LockEntry): boolean { return util.str_less(b.name, a.name); }
function render(src: string): string {
  match (fern_toml.parse_lock(src)) {
    Ok(lock) => {
      let out: string = "OK|" + docsummary.quote(lock.index_serial);
      for e in util.sorted_by(lock.entries, after) {
        out = out + "|" + docsummary.quote(e.name) + "|" + docsummary.quote(e.version) + "|" + docsummary.quote(e.path) + "|" + docsummary.quote(e.url) + "|" + docsummary.quote(e.hash);
      }
      return out;
    },
    Err(message) => { return "ERR|" + message; }
  }
}
function main(): i32 {
`)
	want := make([]string, len(cases))
	for i, tc := range cases {
		fixture := filepath.Join(dir, fmt.Sprintf("case%d", i))
		if err := os.MkdirAll(fixture, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixture, mvs.LockFileName), []byte(tc.source), 0o644); err != nil {
			t.Fatal(err)
		}
		lock, err := mvs.ReadLock(fixture)
		if err != nil {
			want[i] = "ERR|" + strings.TrimPrefix(err.Error(), fixture+string(filepath.Separator))
		} else {
			want[i] = "OK|" + strconv.Quote(lock.IndexSerial)
			var names []string
			for name := range lock.Packages {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				e := lock.Packages[name]
				want[i] += "|" + strconv.Quote(name) + "|" + strconv.Quote(e.Version.String()) + "|" + strconv.Quote(e.Source.Path) + "|" + strconv.Quote(e.Source.URL) + "|" + strconv.Quote(e.Source.Hash)
			}
		}
		fmt.Fprintf(&src, "print(render(%s));\n", strconv.Quote(tc.source))
	}
	src.WriteString("return 0;\n}\n")
	if err := os.WriteFile(filepath.Join(dir, "lock_contract.fern"), []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "lock_contract.fern", "lock_contract", target)
			out, err := mvsDriverCommand(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("lock parser: %v\n%s", err, out)
			}
			lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
			if len(lines) != len(cases) {
				t.Fatalf("got %d rows, want %d: %q", len(lines), len(cases), out)
			}
			for i, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					if lines[i] != want[i] {
						t.Errorf("self-host = %q, Go reader = %q", lines[i], want[i])
					}
				})
			}
		})
	}
}
