package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostRuntimeNeedRegistration(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	const source = `import "./asmcore";
function main(): i32 {
  let empty: asmcore.EmitState = asmcore.new_state();
  let scalar: asmcore.EmitState = empty.need("str_eq");
  if (!scalar.has_need("str_eq") || scalar.has_need("heap")) { return 1; }
  let text: asmcore.EmitState = scalar.need("str_trim");
  if (!text.has_need("str_concat") || !text.has_need("heap")) { return 2; }
  if (scalar.has_need("str_trim") || scalar.has_need("heap")) { return 3; }
  let buffer: asmcore.EmitState = empty.need("buf_text");
  if (!buffer.has_need("buf") || !buffer.has_need("heap")) { return 4; }
  let split: asmcore.EmitState = text.need("str_split");
  if (!split.has_need("arr_push") || !split.has_need("arr_push_owned")) { return 5; }
  let repeated: asmcore.EmitState = split.need("str_split").need("arr_push_owned").need("heap");
  if (repeated.needed.len() != split.needed.len()) { return 6; }
  let marker: asmcore.EmitState = empty.need("field_reclaim:Example");
  if (!marker.has_need("field_reclaim:Example") || marker.has_need("heap")) { return 7; }
  if (empty.needed.len() != 0) { return 8; }
  let packed: asmcore.EmitState = empty.need("arr_push_u8");
  if (!packed.has_need("arr_push") || !packed.has_need("heap")) { return 10; }
  let array: asmcore.EmitState = empty.need_array_helper("__fern_bytes_owned");
  if (!array.has_need("bytes_owned") || !array.has_need("heap")) { return 11; }
  let i: i32 = 0;
  while (i < repeated.needed.len()) {
    let j: i32 = i + 1;
    while (j < repeated.needed.len()) {
      if (repeated.needed[i] == repeated.needed[j]) { return 9; }
      j = j + 1;
    }
    i = i + 1;
  }
  empty.release();
  return 0;
}`
	if err := os.WriteFile(filepath.Join(dir, "need_registration.fern"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "need_registration.fern", "need_registration")
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("runtime need registration: %v\n%s", err, out)
	}
}
