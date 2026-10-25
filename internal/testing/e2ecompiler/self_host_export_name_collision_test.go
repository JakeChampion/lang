package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostExportNameCollision(t *testing.T) {
	cli := newStrictCLI(t)
	const source = `import "std/array";
struct Sample { value: i32 }
function (self: Sample) filter(): i32 { return self.value; }
@export("local:test/audio", "filter")
function filter(value: i32): i32 { return value * 2; }
function main(): i32 {
 let sample = Sample { value: 7 };
 if (sample.filter() != 7 || filter(21) != 42) { return 1; }
 return 0;
}`
	wat := cli.emit(t, "wasm32-wasi", source)
	path := filepath.Join(t.TempDir(), "collision.wat")
	if err := os.WriteFile(path, []byte(wat), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("wasmtime", "run", "--invoke", "local:test/audio#filter", path, "21")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("invoke exported filter: %v", err)
	}
	if strings.TrimSpace(string(output)) != "42" {
		t.Fatalf("wrong export result: %s", output)
	}
	for _, invalid := range []string{
		`struct Sample { value: i32 }
@export("local:test/audio", "filter")
function (self: Sample) filter(): i32 { return self.value; }
function filter(value: i32): i32 { return value; }
function main(): i32 { return 0; }`,
		`@export("local:test/audio", "filter")
function filter[T](value: T): i32 { return 0; }
function main(): i32 { return 0; }`,
	} {
		_, diagnostic, err := cli.tryEmit(t, "wasm32-wasi", invalid)
		if err == nil || !strings.Contains(diagnostic, "E054") {
			t.Fatalf("invalid export escaped validation: %v\n%s", err, diagnostic)
		}
	}
}
