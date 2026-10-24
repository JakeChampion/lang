package e2ecompiler

import "testing"

const builtinShadowProgram = `function seven(): i32 { return 7; }
@noinline function apply_shadow(write: () => i32): i32 { return write(); }
function local_write(): i32 { let write: () => i32 = (): i32 => 7; return write(); }
function local_print(): i32 { let print: () => i32 = (): i32 => 7; return print(); }
function local_args(): i32 { let args: () => i32 = (): i32 => 7; return args(); }
function named_value(): i32 { let write: () => i32 = seven; return write(); }
function nested_value(): i32 { function write(): i32 { return 7; } return write(); }
function main(): i32 {
  if (local_write() != 7) { return 1; }
  if (local_print() != 7) { return 2; }
  if (local_args() != 7) { return 3; }
  if (named_value() != 7) { return 4; }
  if (nested_value() != 7) { return 5; }
  if (apply_shadow((): i32 => 7) != 7) { return 6; }
  print("ok");
  return 0;
}`

func TestSelfHostBuiltinShadowCalls(t *testing.T) {
	runSemanticProgram(t, "builtin-shadow", builtinShadowProgram,
		[]string{"seven", "apply_shadow", "local_write", "local_print", "local_args", "named_value", "nested_value"},
		semInlineWants("ok\n"), "apply_shadow")
}
