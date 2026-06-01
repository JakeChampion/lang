package e2e

import "testing"

// TestWASMModuleBuildersMinimal builds the same "function returns
// 42" core module as TestWASMModuleMinimal, but constructs the
// Module through the immutable builder methods
// (module_new().with_types(...).with_functions(...)...) instead of
// in-place field assignment. The byte-level assertions are identical
// to TestWASMModuleMinimal, so this proves the builder chain produces
// byte-for-byte the same module as the field-set form it replaces —
// the contract that lets the wasm test suite migrate off field
// mutation (immutable-data-structures migration).
func TestWASMModuleBuildersMinimal(t *testing.T) {
	src := `import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var p0: u8[] = [];
    var r0: u8[] = [encode.valtype_i32()];
    var bodyExpr: u8[] = inst.inst_i32_const([], 42);
    var localsBytes: u8[] = inst.put_locals_empty([]);
    var fn: u8[] = inst.put_function_body([], localsBytes, bodyExpr);

    // Whole module in one immutable builder chain.
    var m: module.Module = module.module_new()
        .with_types([p0], [r0])
        .with_functions([0u32])
        .with_exports(["main"], [sections.export_func()], [0u32])
        .with_code([fn]);

    var bytes: u8[] = module.build(m);

    // Preamble.
    if (bytes[0] != 0u8) { return 10; }
    if (bytes[3] != 109u8) { return 11; }   // 'm'
    if (bytes[4] != 1u8) { return 12; }     // version

    // Type section at offset 8.
    if (bytes[8] != 1u8) { return 20; }     // section_type id
    if (bytes[11] != 96u8) { return 23; }   // functype tag
    if (bytes[14] != 127u8) { return 26; }  // i32

    // Function section at offset 15.
    if (bytes[15] != 3u8) { return 30; }    // section_function id
    if (bytes[18] != 0u8) { return 33; }    // typeidx 0

    // Export section at offset 19.
    if (bytes[19] != 7u8) { return 40; }    // section_export id
    if (bytes[21] != 1u8) { return 42; }    // count 1
    if (bytes[23] != 109u8) { return 44; }  // 'm'
    if (bytes[27] != 0u8) { return 48; }    // kind func
    if (bytes[28] != 0u8) { return 49; }    // idx 0

    // Code section at offset 29.
    if (bytes[29] != 10u8) { return 50; }   // section_code id
    if (bytes[34] != 65u8) { return 55; }   // i32.const
    if (bytes[35] != 42u8) { return 56; }   // 42
    if (bytes[36] != 11u8) { return 57; }   // end

    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("builder minimal: exit = %d, want 0", got)
	}
}

// TestWASMModuleBuildersSingletonFlags pins that the flag-folding
// builders (with_memory / with_table / with_start) set their
// `*_present` / `has_start` gate, so build() actually emits the
// section. Builds a module with a memory and a start function and
// checks both sections appear.
func TestWASMModuleBuildersSingletonFlags(t *testing.T) {
	src := `import "std/wasm/module";
import "std/wasm/inst";
import "std/wasm/encode";
import "std/wasm/sections";
function main(): i32 {
    var p0: u8[] = [];
    var r0: u8[] = [];
    var bodyExpr: u8[] = inst.put_function_body([], inst.put_locals_empty([]), inst.inst_nop([]));
    // Type () -> (); function 0; a memory; start = func 0.
    var m: module.Module = module.module_new()
        .with_types([p0], [r0])
        .with_functions([0u32])
        .with_memory(1u32, 0 - 1)
        .with_start(0u32)
        .with_code([bodyExpr]);
    var bytes: u8[] = module.build(m);

    // Scan section IDs; require a memory (5) and a start (8) section
    // to be present — proves with_memory/with_start set their gates.
    var saw_mem: boolean = false;
    var saw_start: boolean = false;
    var i: i32 = 8; // past the 8-byte preamble
    // Cheap structural scan: section id byte then LEB body size.
    // Bodies here are small (<128), so the size is a single byte.
    while (i < bytes.len()) {
        var id: i32 = bytes[i] as i32;
        if (id == 5) { saw_mem = true; }
        if (id == 8) { saw_start = true; }
        var size: i32 = bytes[i + 1] as i32;
        i = i + 2 + size;
    }
    if (!saw_mem) { return 1; }
    if (!saw_start) { return 2; }
    return 0;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("builder singleton flags: exit = %d, want 0", got)
	}
}
