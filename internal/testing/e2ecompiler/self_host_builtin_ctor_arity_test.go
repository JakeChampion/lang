package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostBuiltinConstructorArity(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	for _, tc := range []struct{ name, result, body, code string }{
		{"missing unit", "Result[void, string]", "return Ok();", "E036"},
		{"missing integer", "Result[i32, string]", "return Ok();", "E036"},
		{"missing optional", "Option[i32]", "return Some();", "E036"},
		{"missing failure", "Result[i32, string]", "return Err();", "E036"},
		{"extra success", "Result[i32, string]", "return Ok(1, 2);", "E036"},
		{"extra optional", "Option[i32]", "return Some(1, 2);", "E036"},
		{"extra failure", "Result[i32, string]", "return Err(1, 2);", "E036"},
		{"qualified missing", "Result[void, string]", "return Result.Ok();", "E036"},
		{"qualified extra", "Option[i32]", "return Option.Some(1, 2);", "E036"},
		{"wrong success payload", "Result[string, string]", "return Ok(7);", "E002"},
		{"wrong optional payload", "Option[string]", "return Some(7);", "E002"},
		{"wrong qualified payload", "Result[string, string]", "return Result.Ok(7);", "E002"},
		{"none extra", "Option[i32]", "return None(1);", "E038"},
		{"void success", "Result[void, string]", "return Ok(empty());", "E072"},
		{"void optional", "Option[void]", "return Some(empty());", "E072"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			source := "function empty(): void {}\nfunction probe(): " + tc.result + " { " + tc.body + " }\nfunction main(): i32 { return 0; }\n"
			if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(oracle, "-check", path).CombinedOutput(); err == nil || !strings.Contains(string(out), "error["+tc.code+"]") {
				t.Fatalf("Go oracle expected %s: %v\n%s", tc.code, err, out)
			}
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					out, err := cli.compileCmd(target, filepath.Join(t.TempDir(), "out"), path).CombinedOutput()
					if err == nil || !strings.Contains(string(out), "error["+tc.code+"]") {
						t.Fatalf("expected %s: %v\n%s", tc.code, err, out)
					}
				})
			}
		})
	}
}

func TestSelfHostBuiltinUnitPayloads(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	const source = `function stage(flag: boolean): Result[void, string] {
 if (flag) { return Ok(()); }
 return Err(args()[0] + "-failure");
}
function propagate(flag: boolean): Result[void, string] { stage(flag)?; return Result.Ok(()); }
function main(): i32 {
 match (propagate(true)) {
  Ok(unit) => {
   let option: Option[void] = Some(unit);
   match (option) { Some(_) => {}, None => { return 1; } }
  },
  Err(_) => { return 2; }
 }
 match (propagate(false)) { Err(message) => { if (message.len() == 0) { return 3; } }, Ok(_) => { return 4; } }
 let empty: Option[void] = None;
 match (empty) { None => {}, Some(_) => { return 5; } }
 let Ok = (): i32 => 7;
 if (Ok() != 7) { return 6; }
 return 0;
}`
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(oracle, "-interp", path).CombinedOutput(); err != nil {
		t.Fatalf("Go interpreter: %v\n%s", err, out)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("unit payloads exit %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostJpegHeaderUnitPayloadOracle(t *testing.T) {
	oracle := buildLangBinForInterp(t)
	path, err := filepath.Abs("../../../fern-image/tests/jpeg_header_test.fern")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(oracle, "-check", path).CombinedOutput(); err != nil {
		t.Fatalf("JPEG header oracle check: %v\n%s", err, out)
	}
}
