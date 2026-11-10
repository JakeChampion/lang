package checker

import "testing"

const ownedResultPrelude = ownedMatchPrelude + `function (own box: Box) close(): void {}`

func TestCheckedOwnedResults(t *testing.T) {
	for name, src := range map[string]string{
		"owned enum state":        `enum State { Plain, Secured(Box), Awaiting(Option[Box]) } function awaiting(own state: State): own State { match (state) { Secured(box) => { return Awaiting(Some(box)); }, _ => { return Awaiting(None); } } return Awaiting(None); }`,
		"fresh match result":      `enum State { Plain, Secured(Box) } function fresh(state: State): own Box { match (state) { Secured(box) => { return Box { values: [box.values.len() as u8] }; }, _ => { return Box { values: [] }; } } return Box { values: [] }; }`,
		"fresh match shadow":      `enum State { Plain, Secured(Box) } function fresh(state: State): own Box { match (state) { Secured(box) => { let box = Box { values: [] }; return box; }, _ => { return Box { values: [] }; } } return Box { values: [] }; }`,
		"conditional result":      `function pass[T](own x: T, b: boolean): own T { return if (b) { x } else { x }; }`,
		"match result":            `function pass[T](own x: T, b: boolean): own T { return match (b) { true => x, false => x }; }`,
		"bound factory":           `trait Factory { function make(): own Self; } impl Factory for Box { function make(): own Self { return Box { values: [] }; } } function make[T: Factory](): own T { return T.make(); }`,
		"copied array element":    `struct Data { bytes: u8[] } function wrap(data: Data): own Data[] { return [data]; }`,
		"copied tuple field":      `struct Data { bytes: u8[] } function wrap(data: Data): own (Data, i32) { return (data, 1); }`,
		"copied record field":     `struct Data { bytes: u8[] } struct Outer { data: Data } function wrap(data: Data): own Outer { return Outer { data: data }; }`,
		"copied captured element": `struct Data { bytes: u8[] } function wrap(data: Data): void { let f = (): own Data[] => [data]; }`,
		"fresh local":             `function make(): own Box { let box = Box { values: [] }; return box; }`,
		"fresh lambda local":      `function run(): void { let f = (): own Box => { let box = Box { values: [] }; return box; }; }`,
		"owned field":             `struct Outer { box: Box } function get(own outer: Outer): own Box { return outer.box; }`,
		"copied f64 lanes":        `function copy(x: f64x2): own f64x2 { return x; }`,
		"copied f32 lanes":        `function copy(x: f32x4): own f32x4 { return x; }`,
		"copied map root":         `function copy(m: Map[string, boolean]): own Map[string, boolean] { return m; }`,
		"copied map update":       `function update(own m: Map[string, boolean]): own Map[string, boolean] { return m.insert("k", true); }`,
		"copied nested map":       `struct Data { entries: Map[string, boolean] } function copy(d: Data): own Data { return d; }`,
		"copied data root":        `struct Data { bytes: u8[] } function copy(data: Data): own Data { return data; }`,
		"deferred sibling":        `struct Pair { first: Box, second: Box } function get(own p: Pair): own Box { defer p.first.close(); return p.second; }`,
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
		"borrowed enum state":               `enum State { Plain, Secured(Box), Awaiting(Option[Box]) } function awaiting(state: State): own State { match (state) { Secured(box) => { return Awaiting(Some(box)); }, _ => { return Awaiting(None); } } return Awaiting(None); }`,
		"borrowed match alias":              `enum State { Plain, Secured(Box) } function unwrap(state: State): own Box { match (state) { Secured(box) => { let local = box; return local; }, _ => { return Box { values: [] }; } } return Box { values: [] }; }`,
		"borrowed nested match":             `enum State { Plain, Secured(Box) } function unwrap(state: Option[State]): own Box { match (state) { Some(s) => { match (s) { Secured(box) => { return box; }, _ => {} } }, _ => {} } return Box { values: [] }; }`,
		"borrowed match shadow":             `enum State { Plain, Secured(Box) } function unwrap(state: State, own box: Box): own Box { match (state) { Secured(box) => { return box; }, _ => { return box; } } return box; }`,
		"borrowed match capture":            `enum State { Plain, Secured(Box) } function outer(state: State): void { let get = (): own Box => { match (state) { Secured(box) => { return box; }, _ => { return Box { values: [] }; } } return Box { values: [] }; }; }`,
		"borrowed nested aggregate capture": `function run(x: Box): void { let f = (): own Box[] => { let xs = [x]; return xs; }; }`,
		"borrowed local alias":              `function bad(box: Box): own Box { let local = box; return local; }`,
		"captured local":                    `function make(): own Box { let box = Box { values: [] }; let f = (): Box => box; return box; }`,
		"deferred field":                    `struct Outer { box: Box } function (own o: Outer) close(): void { o.box.close(); } function get(own o: Outer): own Box { defer o.close(); return o.box; }`,
		"borrowed map resource":             `function bad(m: Map[string, Box]): own Map[string, Box] { return m; }`,
		"borrowed map cell":                 `function bad(m: Map[string, Cell[i32]]): own Map[string, Cell[i32]] { return m; }`,
		"borrowed map callback":             `function bad(m: Map[string, () => i32]): own Map[string, () => i32] { return m; }`,
		"borrowed mutable root":             `function bad(data: Cell[i32]): own Cell[i32] { return data; }`,
		"deferred local root":               `function get(): own Box { let b = Box { values: [] }; defer b.close(); return b; }`,
		"deferred local field":              `struct Outer { box: Box } function (own o: Outer) close(): void { o.box.close(); } function get(): own Box { let o = Outer { box: Box { values: [] } }; defer o.close(); return o.box; }`,
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
