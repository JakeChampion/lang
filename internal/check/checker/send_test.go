package checker

import (
	"strings"
	"testing"
)

const spawnDecl = `function spawn(send body: () => i32): i32 { return body(); }
function spawn_cell(send body: () => Cell[i32]): i32 { return body().get(); }
function post(send msg: [i32]): i32 { return msg.len(); }
struct Box { n: i32 }
struct Pair[T] { a: T, b: T }
struct Holder { c: Cell[i32] }
trait Shape { function area(self: Self): i32; }
impl Shape for Box { function area(self: Self): i32 { return self.n; } }
@must_consume
struct Ticket { id: i32 }
function redeem(own t: Ticket): i32 { return t.id; }
`

func TestSendParameterRefusesWhatCannotCrossAWorkerBoundary(t *testing.T) {
	cases := []struct{ name, src, frag string }{
		{"cell", `function main(): i32 { let c: Cell[i32] = cell_new(1); return spawn((): i32 => c.get()); }`,
			`captures "c" of type Cell[i32], which cannot cross a worker boundary: a Cell is shared by aliasing`},
		{"handle", `function main(): i32 { let w: Writer = stdout(); return spawn((): i32 => w.fd); }`,
			`captures "w" of type Writer, which cannot cross a worker boundary: a built-in handle owns a descriptor`},
		{"function_value", `function main(): i32 { let f: () => i32 = (): i32 => 1; return spawn((): i32 => f()); }`,
			`captures "f" of type () => i32, which cannot cross a worker boundary: a function value carries its environment`},
		{"map_of_cell", `import "core/map"; function main(): i32 { let m: Map[string, Cell[i32]] = map_new(4); return spawn((): i32 => m.len()); }`,
			`captures "m" of type Map[string, Cell[i32]], which cannot cross a worker boundary: a Cell is shared by aliasing`},
		{"dyn", `function main(): i32 { let d: dyn Shape = Box { n: 1 }; return spawn((): i32 => d.area()); }`,
			`captures "d" of type dyn Shape, which cannot cross a worker boundary: a dyn value hides its concrete type`},
		{"must_consume", `function main(): i32 { let t: Ticket = Ticket { id: 1 }; return spawn((): i32 => t.id); }`,
			`captures "t" of type Ticket, which cannot cross a worker boundary: a @must_consume value carries an obligation that a copy would double`},
		{"struct_of_cell", `function main(): i32 { let h: Holder = Holder { c: cell_new(1) }; return spawn((): i32 => h.c.get()); }`,
			`captures "h" of type Holder, which cannot cross a worker boundary: a Cell is shared by aliasing`},
		{"type_parameter", `function g[T](x: T): i32 { return spawn((): i32 => { let y: T = x; return 0; }); }
function main(): i32 { return g(1); }`,
			`captures "x" of type T, which cannot cross a worker boundary: type parameter T is not known to be send-safe`},
		{"closure_value_argument", `function main(): i32 { let body: () => i32 = (): i32 => 1; return spawn(body); }`,
			`argument to ` + "`send`" + ` parameter "body" has type () => i32, which cannot cross a worker boundary: a function value carries its environment`},
		{"result", `function main(): i32 { return spawn_cell((): Cell[i32] => cell_new(1)); }`,
			`returns Cell[i32], which cannot cross a worker boundary: a Cell is shared by aliasing`},
		{"view_argument", `function main(): i32 { let xs: i32[] = [1, 2]; return post(xs[0:1]); }`,
			`has type [i32], which cannot cross a worker boundary: a view borrows another value's storage`},
		{"send_copy_of_cell", `function main(): i32 { let c: Cell[i32] = cell_new(1); let d: Cell[i32] = __send_copy(c); return d.get(); }`,
			`argument to ` + "`send`" + ` parameter "value" has type Cell[i32], which cannot cross a worker boundary: a Cell is shared by aliasing`},
	}
	for _, tc := range cases {
		err := checkSource(t, spawnDecl+tc.src)
		if err == nil {
			t.Errorf("%s: expected E100, got none", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.frag) {
			t.Errorf("%s: expected E100 containing %q, got: %v", tc.name, tc.frag, err)
		}
	}
}

func TestSendParameterAdmitsSendSafeValues(t *testing.T) {
	cases := []struct{ name, src string }{
		{"scalar_and_string", `function main(): i32 { let n: i32 = 1; let s: string = "x"; return spawn((): i32 => n + s.len()); }`},
		{"record_array_option", `function main(): i32 { let b: Box = Box { n: 1 }; let xs: i32[] = [1]; let o: Option[string] = Some("x"); return spawn((): i32 => { match (o) { Some(v) => { return b.n + xs.len() + v.len(); }, None => { return 0; } } }); }`},
		{"tuple", `function main(): i32 { let p: (i32, string) = (1, "x"); return spawn((): i32 => p.0); }`},
		{"generic_struct_of_scalars", `function main(): i32 { let p: Pair[i32] = Pair { a: 1, b: 2 }; return spawn((): i32 => p.a + p.b); }`},
		{"no_captures", `function main(): i32 { return spawn((): i32 => 7); }`},
		{"send_copy_of_record_and_option", `function main(): i32 { let b: Box = Box { n: 1 }; let c: Box = __send_copy(b); let o: Option[string] = __send_copy(Some("x")); return c.n + b.n; }`},
	}
	for _, tc := range cases {
		if err := checkSource(t, spawnDecl+tc.src); err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
		}
	}
}
