package checker

import "testing"

func TestAssociatedResultOwnership(t *testing.T) {
	wantOK(t, "fresh_associated_result", `
trait Make { function make(): Self; }
function take[T](own value: T): T { return value; }
function fresh[T: Make](): T { return take(T.make()); }
function local[T: Make](): T { let value = T.make(); return take(value); }
`)
	wantE051(t, "associated_borrowed_argument", `
trait Echo { function echo(value: Self): Self; }
function take[T](own value: T): T { return value; }
function borrowed[T: Echo](value: T): T { return take(T.echo(value)); }
`)
	wantE051(t, "local_shadows_type_parameter", `
trait Make { function make(): Self; }
struct Handle { fd: i32 }
function (own h: Handle) close(): i32 { return h.fd; }
struct Factory { make: () => Handle }
function take(own value: Handle): i32 { return value.close(); }
function shadowed[T: Make](T: Factory): i32 { return take(T.make()); }
`)
}
