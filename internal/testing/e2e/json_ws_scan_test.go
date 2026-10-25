package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

const jsonWsScanSrc = `import "std/json";

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
  print(show("1"));
  print(show(" 1"));
  print(show("1 "));
  print(show("\t\n\r 1 \r\n\t"));
  print(show("   \n\n\n\t\t\t   [ 1 ,\n 2\t,\r\n3 ]   "));
  print(show("{ \"a\" : [ true , false , null ] ,\n\t\"b\" :\r\n{ } }"));
  print(show("[\n" + "        " + "\"x\"" + "\n" + "    " + "]"));
  print(show(""));
  print(show("   \t\r\n   "));
  print(show("\x0c1"));
  print(show("\x0b1"));
  print(show("1\0"));
  print(show(" 1"));
  let pad: string = "";
  let i: i32 = 0;
  while (i < 300) {
    pad = pad + " \t\n\r";
    i = i + 1;
  }
  print(show(pad + "{" + pad + "\"k\"" + pad + ":" + pad + "7" + pad + "}" + pad));
  return 0;
}
`

const jsonWsScanWant = `1
1
1
1
[1,2,3]
{"a":[true,false,null],"b":{}}
["x"]
err
err
err
err
err
err
{"k":7}
`

func TestInterpJsonWsScan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(jsonWsScanSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != jsonWsScanWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s\nstderr: %s", code, out, jsonWsScanWant, errOut)
	}
}

func TestX86_64JsonWsScan(t *testing.T) {
	out, code := compileAndRunX86_64(t, jsonWsScanSrc)
	if code != 0 || out != jsonWsScanWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, jsonWsScanWant)
	}
}

func TestArm64JsonWsScan(t *testing.T) {
	out, code := compileAndRunArm64(t, jsonWsScanSrc)
	if code != 0 || out != jsonWsScanWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, jsonWsScanWant)
	}
}

func TestWasmJsonWsScan(t *testing.T) {
	if out := compileAndRunWasmCapture(t, jsonWsScanSrc); out != jsonWsScanWant {
		t.Fatalf("printed\n%s\nwant\n%s", out, jsonWsScanWant)
	}
}
