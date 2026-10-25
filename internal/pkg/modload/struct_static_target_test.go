package modload_test

import (
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
)

func TestSameModuleStructStaticTargets(t *testing.T) {
	for name, source := range map[string]string{
		"associated-function": `
struct Input { value: i32 }
trait Make { function make(): Input; }
impl Make for Input { function make(): Input { return Input { value: 42 }; } }
pub function run(): i32 { return Input.make().value; }`,
		"derived-json-reader": `
import "std/json";
@derive(json.FromJson) struct Input { value: i32 }
pub function run(): i32 { match (Input.from_json("{\"value\":42}")) { Ok(n) => { return n.value; }, Err(e) => { return 1; } } }`,
		"local-shadow": `
struct Input { value: i32 }
struct Other { value: i32 }
function (o: Other) make(): i32 { return o.value; }
pub function run(): i32 { let Input = Other { value: 42 }; return Input.make(); }`,
		"type-parameter-shadow": `
struct Input { value: i32 }
trait Make { function make(): i32; }
function invoke[Input: Make](): i32 { return Input.make(); }
pub function run(): i32 { return 42; }`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeFiles(t, map[string]string{
				"lib.fern":  source,
				"main.fern": `import "./lib"; function main(): i32 { return lib.run(); }`,
			})
			prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := checker.Check(prog); err != nil {
				t.Fatal(err)
			}
		})
	}
}
