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
	formattedPath := filepath.Join(t.TempDir(), "formatted.fern")
	if err := os.WriteFile(formattedPath, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	format := runX86_64Bin(cli.runner, cli.bin, "-fmt", "-w", formattedPath)
	if output, err := format.CombinedOutput(); err != nil {
		t.Fatalf("format export and method: %v\n%s", err, output)
	}
	formatted, err := os.ReadFile(formattedPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(formatted), "@export(") != 1 {
		t.Fatalf("formatter attached the export to another declaration:\n%s", formatted)
	}
	wat := cli.emit(t, "wasm32-wasi", string(formatted))
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
