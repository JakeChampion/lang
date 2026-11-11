package modulecases

type ReexportCase struct {
	Name      string
	Files     map[string]string
	ErrorFile string
}

func Reexports() []ReexportCase {
	const choice = `pub enum Choice { One, Wrap(i32) }`
	const union = `pub struct A { n: i32 } pub struct B { n: i32 } pub type Choice = A | B;`
	const unionMain = `function main(): i32 {
		let value: m.Choice = m.A(m.A { n: 7 });
		match (value) { m.A(a) => { assert(a.n == 7); }, m.B(b) => { assert(false); } }
		return 0;
	}`
	return []ReexportCase{
		{"bare-payload-variant", map[string]string{
			"lib.fern":    choice,
			"facade.fern": `pub use "./lib".{ Choice, Wrap };`,
			"main.fern":   `import "./facade"; function main(): i32 { let n = facade.Wrap(42); return 0; }`,
		}, "facade.fern"},
		{"unused-unit-variant", map[string]string{
			"lib.fern":    choice,
			"facade.fern": `pub use "./lib".{ One };`,
			"main.fern":   `import "./facade"; function main(): i32 { return 0; }`,
		}, "facade.fern"},
		{"unused-missing-name", map[string]string{
			"lib.fern":    choice,
			"facade.fern": `pub use "./lib".{ Missing };`,
			"main.fern":   `import "./facade"; function main(): i32 { return 0; }`,
		}, "facade.fern"},
		{"private-function", map[string]string{
			"lib.fern":    `function hidden(): i32 { return 42; }`,
			"facade.fern": `pub use "./lib".{ hidden };`,
			"main.fern":   `import "./facade"; function main(): i32 { return 0; }`,
		}, "facade.fern"},
		{"package-function", map[string]string{
			"lib.fern":    `pub(package) function hidden(): i32 { return 42; }`,
			"facade.fern": `pub use "./lib".{ hidden };`,
			"main.fern":   `import "./facade"; function main(): i32 { return 0; }`,
		}, "facade.fern"},
		{"invalid-chain", map[string]string{
			"lib.fern":    choice,
			"inner.fern":  `pub use "./lib".{ Wrap };`,
			"facade.fern": `pub use "./inner".{ Wrap };`,
			"main.fern":   `import "./facade"; function main(): i32 { return 0; }`,
		}, "inner.fern"},
		{"enum-through-owner", map[string]string{
			"lib.fern":    choice,
			"facade.fern": `pub use "./lib".{ Choice };`,
			"main.fern":   `import "./facade"; function main(): i32 { match (facade.Choice.Wrap(42)) { facade.Choice.Wrap(n) => { assert(n == 42); }, _ => { assert(false); } } return 0; }`,
		}, ""},
		{"valid-chain", map[string]string{
			"lib.fern":    choice,
			"inner.fern":  `pub use "./lib".{ Choice };`,
			"facade.fern": `pub use "./inner".{ Choice };`,
			"main.fern":   `import "./facade"; function main(): i32 { match (facade.Choice.Wrap(42)) { facade.Choice.Wrap(n) => { assert(n == 42); }, _ => { assert(false); } } return 0; }`,
		}, ""},
		{"public-function-precedence", map[string]string{
			"lib.fern":    choice + ` pub function Wrap(n: i32): i32 { return n; }`,
			"facade.fern": `pub use "./lib".{ Wrap };`,
			"main.fern":   `import "./facade"; function main(): i32 { assert(facade.Wrap(42) == 42); return 0; }`,
		}, ""},
		{"public-declarations", map[string]string{
			"lib.fern":    `pub struct Box { n: i32 } pub const ANSWER: i32 = 42; pub trait Marker {}`,
			"facade.fern": `pub use "./lib".{ Box, ANSWER, Marker };`,
			"main.fern":   `import "./facade"; function main(): i32 { let b = facade.Box { n: facade.ANSWER }; assert(b.n == 42); return 0; }`,
		}, ""},
		{"union-member-constructor", map[string]string{
			"lib.fern":  union,
			"main.fern": `import "./lib" as m;` + unionMain,
		}, ""},
		{"union-member-reexport", map[string]string{
			"lib.fern":    union,
			"facade.fern": `pub use "./lib".{ A, B, Choice };`,
			"main.fern":   `import "./facade" as m;` + unionMain,
		}, ""},
		{"union-member-reexport-chain", map[string]string{
			"lib.fern":    union,
			"inner.fern":  `pub use "./lib".{ A, B, Choice };`,
			"facade.fern": `pub use "./inner".{ A, B, Choice };`,
			"main.fern":   `import "./facade" as m;` + unionMain,
		}, ""},
		{"union-member-keeps-private-type", map[string]string{
			"lib.fern":  `struct A { n: i32 } pub struct B { n: i32 } pub type Choice = A | B;`,
			"main.fern": `import "./lib" as m; function run(a: m.A): i32 { let value = m.A(a); return 0; } function main(): i32 { return 0; }`,
		}, "main.fern"},
		{"union-member-keeps-private-function", map[string]string{
			"lib.fern":  union + ` function A(a: A): i32 { return a.n; }`,
			"main.fern": `import "./lib" as m; function run(a: m.A): i32 { let value = m.A(a); return 0; } function main(): i32 { return 0; }`,
		}, "main.fern"},
	}
}
