package e2eharness

import (
	"os"
	"path/filepath"
	"testing"
)

func WriteDateParserBytesFixture(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"datetime", "timefmt", "gnu"} {
		lib, err := os.ReadFile(RepoPath("coreutils", "lib", name+".fern"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".fern"), lib, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	source := `import "./datetime";
import "std/tz";
import "std/utf8";
import "std/string";

function main(): i32 {
  let zone: tz.Zone = tz.utc_zone();
  let text: datetime.Result = datetime.parse_full("é", zone, "UTC0", true, 0, 0, 0, true);
  if (text.ok || !utf8.is_valid_utf8(text.dbg)) { return 1; }
  let original: u8[] = [120 as u8, 195 as u8, 169 as u8, 121 as u8];
  let raw: datetime.ByteResult = datetime.parse_full_bytes(original[1:3], zone, "UTC0", true, 0, 0, 0, true);
  original = [];
  if (raw.result.ok || raw.tail.len() == 0 || !utf8.is_valid_utf8(raw.result.dbg)) { return 2; }
  let i: i32 = 0;
  let found: boolean = false;
  while (i < raw.tail.len()) {
    if (raw.tail[i] == 169 as u8) { found = true; }
    i = i + 1;
  }
  if (!found || text.dbg != raw.result.dbg + utf8.from_bytes_lossy(raw.tail)) { return 3; }
  let good: u8[] = "x2024-01-02y".bytes();
  let parsed: datetime.ByteResult = datetime.parse_full_bytes(good[1:good.len()-1], zone, "UTC0", true, 0, 0, 0, false);
  if (!parsed.result.ok || parsed.result.sec != 1704153600 as i64 || parsed.tail.len() != 0) { return 4; }
  if (good[0] != 120 as u8 || good[good.len()-1] != 121 as u8) { return 5; }
  return 0;
}
`
	path := filepath.Join(dir, "date-parser-bytes.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
