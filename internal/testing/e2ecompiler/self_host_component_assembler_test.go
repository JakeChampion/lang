package e2ecompiler

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var componentAssemblerCache struct {
	sync.Mutex
	wat []byte
}

// The composer runs as Wasm and connects each import the emitted core declares.
// Fixed binary templates have separate tests with fixed input cores.
func worldComponentAssemblerWAT(t *testing.T) []byte {
	t.Helper()
	componentAssemblerCache.Lock()
	defer componentAssemblerCache.Unlock()
	if componentAssemblerCache.wat != nil {
		return componentAssemblerCache.wat
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "watbin.fern", "wit_compose.fern", "wit_decode.fern", "wit_fern_world.fern")
	src := filepath.Join(dir, "assemble.fern")
	program := `import "./watbin";
import "./wit_compose";
import "./wit_decode";
import "./wit_fern_world";
function main(): i32 {
  let core: i32[] = [];
  match (read_file("core.wat")) {
    Ok(wat) => { core = watbin.wat_to_binary(wat); },
    Err(_) => {
      match (read_file_bytes("core.bin")) {
        Ok(bytes) => { for b in bytes { core = core.append(b as i32); } },
        Err(_) => { return 1; }
      }
    },
  }
  let world: i32[] = wit_decode.wit_section_body(watbin.hex_to_bytes(wit_fern_world.fern_world_payload()), 7);
  let comp: i32[] = wit_compose.component_from_world(world, core);
  for b in comp { print_int(b); write("\n"); }
  return 0;
}
`
	if err := os.WriteFile(src, []byte(withPrintInt(program)), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "assemble.wat")
	cli := buildSelfHostCLI(t)
	if log, err := cli.compileCmd("wasm32-wasi", out, src, "-emit", "asm").CombinedOutput(); err != nil {
		t.Fatalf("build component assembler: %v\n%s", err, log)
	}
	wat, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	componentAssemblerCache.wat = wat
	return componentAssemblerCache.wat
}
