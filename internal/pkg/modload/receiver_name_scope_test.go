package modload_test

import (
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
)

func TestMethodReceiverShadowsModuleFunction(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"state.fern": `pub struct State { values: i32[] }
pub function decoder(): State { return State { values: [1, 2] }; }
pub function (decoder: State) count(): i32 { return decoder.values.len(); }
pub function (own decoder: State) consume(): i32 {
 let State { values } = decoder;
 return values.len();
}`,
		"main.fern": `import "./state";
function main(): i32 {
 let value = state.decoder();
 let count = value.count();
 return count + value.consume();
}`,
	})
	program, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(program); err != nil {
		t.Fatalf("receiver binding must shadow the function: %v", err)
	}
}
