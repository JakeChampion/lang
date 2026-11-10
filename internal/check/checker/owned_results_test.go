package checker

import "testing"

const ownedResultPrelude = ownedMatchPrelude + `function (own box: Box) close(): void {}`

func TestCheckedOwnedResults(t *testing.T) {
	for name, src := range map[string]string{
		"conditional result":      `function pass[T](own x: T, b: boolean): own T { return if (b) { x } else { x }; }`,
		"match result":            `function pass[T](own x: T, b: boolean): own T { return match (b) { true => x, false => x }; }`,
		"bound factory":           `trait Factory { function make(): own Self; } impl Factory for Box { function make(): own Self { return Box { values: [] }; } } function make[T: Factory](): own T { return T.make(); }`,
		"copied array element":    `struct Data { bytes: u8[] } function wrap(data: Data): own Data[] { return [data]; }`,
		"copied tuple field":      `struct Data { bytes: u8[] } function wrap(data: Data): own (Data, i32) { return (data, 1); }`,
		"copied record field":     `struct Data { bytes: u8[] } struct Outer { data: Data } function wrap(data: Data): own Outer { return Outer { data: data }; }`,
		"copied captured element": `struct Data { bytes: u8[] } function wrap(data: Data): void { let f = (): own Data[] => [data]; }`,
		"owned input":             `function pass[T](own x: T): own T { return x; }`,
		"copyable result":         `function pass(x: i32): own i32 { return x; }`,
		"fresh scalar field":      `function fresh(x: Box): own Box { return Box { values: [x.values.len() as u8] }; }`,
		"checked value copy":      `@value struct Key { bytes: u8[] } function (own k: Key) erase(): void {} function copy(k: Key): own Key { return k; }`,
		"method result":           `function (x: Box) fresh(): own Box { return Box { values: [] }; } function pass(x: Box): own Box { return x.fresh(); }`,
		"fresh handle":            `function fresh(): own Box { return Box { values: [] }; }`,
		"owned callback":          `function pass[T](own x: T, f: (own T) => own T): own T { return f(x); }`,
		"explicit lambda":         `function run(): void { let f: (own Box) => own Box = (own x: Box): own Box => x; }`,
		"weaken result":           `function pass(own x: Box): own Box { return x; } function run(): void { let f: (own Box) => Box = pass; }`,
	} {
		wantOK(t, name, ownedResultPrelude+src+` function main(): i32 { return 0; }`)
	}
}

func TestCheckedOwnedResultRefusesBorrow(t *testing.T) {
	for name, src := range map[string]string{
		"borrowed nested aggregate capture": `function run(x: Box): void { let f = (): own Box[] => { let xs = [x]; return xs; }; }`,
		"borrowed data root":                `struct Data { bytes: u8[] } function bad(data: Data): own Data { return data; }`,
		"borrowed receiver":                 `function (x: Box) bad(): own Box[] { return [x]; }`,
		"local function capture":            `function run(own x: Box): void { function f(): own Box { return x; } }`,
		"borrowed conditional":              `function pass(x: Box, b: boolean): own Box { return if (b) { x } else { Box { values: [] } }; }`,
		"borrowed aggregate":                `function pass(x: Box): own Box[] { return [x]; }`,
		"borrowed aggregate alias":          `function pass(x: Box): own Box[] { let xs = [x]; return xs; }`,
		"borrowed helper aggregate":         `function wrap(x: Box): Box[] { return [x]; } function pass(x: Box): own Box[] { return wrap(x); }`,
		"borrowed generic input":            `function pass[T](x: T): own T { return x; }`,
		"borrowed handle":                   `function pass(x: Box): own Box { return x; }`,
		"borrowed lambda input":             `function run(): void { let f = (x: Box): own Box => x; }`,
		"borrowed capture":                  `function run(x: Box): void { let f = (): own Box => x; }`,
		"owned capture is shared":           `function run(own x: Box): void { let f = (): own Box => x; }`,
	} {
		wantE051(t, name, ownedResultPrelude+src+` function main(): i32 { return 0; }`)
	}
}

func TestCheckedOwnedResultRefusesStrengthening(t *testing.T) {
	err := checkSource(t, ownedResultPrelude+`function pass(own x: Box): Box { return x; }
function main(): i32 { let f: (own Box) => own Box = pass; return 0; }`)
	if err == nil || !hasCode(err, "E003") {
		t.Fatalf("expected E003, got %v", err)
	}
}

func TestCheckedOwnedTraitResult(t *testing.T) {
	err := checkSource(t, ownedResultPrelude+`trait Factory { function make(): own Self; } impl Factory for Box { function make(): Self { return Box { values: [] }; } } function main(): i32 { return 0; }`)
	if err == nil || !hasCode(err, "E021") {
		t.Fatalf("expected E021, got %v", err)
	}
}
