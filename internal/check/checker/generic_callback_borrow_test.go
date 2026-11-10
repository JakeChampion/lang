package checker

import "testing"

const genericCallbackPrefix = `
struct Handle { fd: i32 }
function (own h: Handle) close(): i32 { return h.fd; }
function keep(own h: Handle): i32 { return h.close(); }
function call[T](f: () => T): T { return f(); }
`

func TestGenericCallbackResultBorrows(t *testing.T) {
	for name, body := range map[string]string{
		"nested_capture":    `function twice[T](f: () => (() => T)): T { return f()(); } function g(h: Handle): i32 { return keep(twice((): (() => Handle) => (() => h))); }`,
		"aggregate_capture": `struct Box { h: Handle } function take(own box: Box): i32 { return keep(box.h); } function g(h: Handle): i32 { return take(call((): Box => Box { h: h })); }`,
		"aggregate_local":   `struct Box { h: Handle } function take(own box: Box): i32 { return keep(box.h); } function g(h: Handle): i32 { let value=call((): Box => { let box=Box {h:h}; return box; }); return take(value); }`,
		"array_capture":     `function take(own xs: Handle[]): i32 { return xs.len(); } function g(h: Handle): i32 { return take(call((): Handle[] => [h])); }`,
		"capture":           `function g(h: Handle): i32 { return keep(call(() => h)); }`,
		"local":             `function g(h: Handle): i32 { let f = () => h; let value = call(f); return keep(value); }`,
		"forward":           `function forward[T](f: () => T): T { return call(f); } function g(h: Handle): i32 { return keep(forward(() => h)); }`,
		"capture_helper":    `function forward[T](h: T): T { return call(() => h); } function g(h: Handle): i32 { return keep(forward(h)); }`,
		"owned_callback":    `function owned[T](own f: () => T): T { return f(); } function g(h: Handle): i32 { return keep(owned(() => h)); }`,
	} {
		t.Run(name, func(t *testing.T) { wantE051(t, name, genericCallbackPrefix+body) })
	}
	for name, body := range map[string]string{
		"owned_state":          `function advance[T](own state: T, step: (own T) => T): T { return step(state); } function g(own h: Handle): i32 { return keep(advance(h, (own s: Handle): Handle => s)); }`,
		"scalar_field_factory": `function fresh_from(h: Handle): Handle { return call((): Handle => Handle { fd: h.fd }); } function g(h: Handle): i32 { return keep(fresh_from(h)); }`,
		"fresh_callback":       `function main(): i32 { return keep(call((): Handle => Handle { fd: 7 })); }`,
		"handle_free_callback": `function result(f: () => Result[i32, string]): Result[i32, string] { return f(); } function g(h: Handle): i32 { let value = result((): Result[i32, string] => Ok(h.fd)); return 1; }`,
		"scalar":               `function g(h: Handle): i32 { return call(() => h.fd); }`,
		"read":                 `function read(f: () => i32): i32 { return f(); } function g(h: Handle): i32 { return read(() => h.fd); }`,
		"shared":               `function shared[T](value: T): T { return unsafe { share_unchecked(value) }; } function g(h: Handle): i32 { return keep(shared(call(() => h))); }`,
		"fresh":                `function fresh(): Handle { return Handle { fd: 7 }; } function main(): i32 { return keep(fresh()); }`,
	} {
		t.Run(name, func(t *testing.T) { wantOK(t, name, genericCallbackPrefix+body) })
	}
}
