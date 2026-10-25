package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

const jsonStringScanSrc = `import "std/json";
import "std/i32";

function show(text: string): string {
  match (json.parse(text)) {
    Ok(v) => {
      return json.json_encode(v);
    },
    Err(e) => {
      return "err";
    }
  }
  return "";
}

function main(): i32 {
  let raw: string[] = ["", "plain", "\"", "\\", "a\"b", "\"ab\"", "\n\r\t", "x\x01y\x1fz", "nul\0mid", "end\\", "héllo ✓", "a\"\"\\\\b"];
  for r in raw {
    let e: string = json.json_escape(r);
    print("[" + e + "]");
    print(show("\"" + e + "\""));
  }
  print(show("\"no escapes at all\""));
  print(show("\"tab\\tand \\u00e9 and \\ud83d\\ude00\""));
  print(show("\"unterminated"));
  print(show("\"bad \\x escape\""));
  print(show("[\"a\", \"b\\n\", {\"k\\\"\": \"v\"}]"));
  let big: string = "";
  let i: i32 = 0;
  while (i < 200) {
    big = big + "abcdefgh";
    i = i + 1;
  }
  let parsed: string = show("\"" + big + "\\n\"");
  print(parsed.len().to_string());
  return 0;
}
`

const jsonStringScanWant = `[]
""
[plain]
"plain"
[\"]
"\""
[\\]
"\\"
[a\"b]
"a\"b"
[\"ab\"]
"\"ab\""
[\n\r\t]
"\n\r\t"
[x\u0001y\u001fz]
"x\u0001y\u001fz"
[nul\u0000mid]
"nul\u0000mid"
[end\\]
"end\\"
[héllo ✓]
"héllo ✓"
[a\"\"\\\\b]
"a\"\"\\\\b"
"no escapes at all"
"tab\tand é and 😀"
err
err
["a","b\n",{"k\"":"v"}]
1604
`

func TestInterpJsonStringScan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(jsonStringScanSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != jsonStringScanWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s\nstderr: %s", code, out, jsonStringScanWant, errOut)
	}
}

func TestX86_64JsonStringScan(t *testing.T) {
	out, code := compileAndRunX86_64(t, jsonStringScanSrc)
	if code != 0 || out != jsonStringScanWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, jsonStringScanWant)
	}
}

func TestArm64JsonStringScan(t *testing.T) {
	out, code := compileAndRunArm64(t, jsonStringScanSrc)
	if code != 0 || out != jsonStringScanWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, jsonStringScanWant)
	}
}

func TestWasmJsonStringScan(t *testing.T) {
	if out := compileAndRunWasmCapture(t, jsonStringScanSrc); out != jsonStringScanWant {
		t.Fatalf("printed\n%s\nwant\n%s", out, jsonStringScanWant)
	}
}
