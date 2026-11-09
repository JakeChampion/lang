package checker

import "testing"

const sharedHandlePrelude = `struct Handle { fd: i32 }
function (own h: Handle) close(): i32 { return h.fd; }
function read_owned(own h: Handle): i32 { return h.fd; }
`

func TestSharedOwnerResultSurvivesBindingsAndReturns(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{"local", `function f(h: Handle): i32 { let value = unsafe { share_unchecked(h) }; return read_owned(value); }`},
		{"helper", `function shared(h: Handle): Handle { return unsafe { share_unchecked(h) }; }
function f(h: Handle): i32 { return read_owned(shared(h)); }`},
		{"generic local", `function shared[T](value: T): T { return unsafe { share_unchecked(value) }; }
function f(h: Handle): i32 { let value = shared(h); return read_owned(value); }`},
		{"generic direct", `function shared[T](value: T): T { return unsafe { share_unchecked(value) }; }
function f(h: Handle): i32 { return read_owned(shared(h)); }`},
		{"generic method", `struct Box[T] { value: T }
function (box: Box[T]) shared(): T { return unsafe { share_unchecked(box.value) }; }
function f(box: Box[Handle]): i32 { return read_owned(box.shared()); }`},
		{"method type parameter", `struct Box { n: i32 }
function (box: Box) shared[T](value: T): T { return unsafe { share_unchecked(value) }; }
function f(h: Handle): i32 { let box = Box { n: 1 }; return read_owned(box.shared(h)); }`},
	} {
		t.Run(tc.name, func(t *testing.T) { wantOK(t, tc.name, sharedHandlePrelude+tc.source) })
	}
}

func TestUnsafeBlockDoesNotGrantAnOwnedAlias(t *testing.T) {
	for _, source := range []string{
		`function f(h: Handle): i32 { let value = unsafe { h }; return read_owned(value); }`,
		`function borrowed(h: Handle): Handle { return unsafe { h }; }
function f(h: Handle): i32 { return read_owned(borrowed(h)); }`,
	} {
		wantE051(t, "ordinary borrowed value", sharedHandlePrelude+source)
	}
}
