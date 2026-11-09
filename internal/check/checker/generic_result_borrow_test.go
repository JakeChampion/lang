package checker

import "testing"

func TestOwnGuardRejectsAHandleBorrowThroughAGenericParameter(t *testing.T) {
	wantE051(t, "generic-handle-result", `
struct Handle { fd: i32 }
function (own h: Handle) close(): i32 { return h.fd; }
function identity[T](value: T): T { return value; }
function take(own h: Handle): i32 { return h.close(); }
function main(): i32 {
    let h = Handle { fd: 7 };
    let alias = identity(h);
    let n = take(alias);
    return n + h.close();
}`)
}

func TestOwnGuardRejectsAHandleBorrowThroughAGenericElement(t *testing.T) {
	wantE051(t, "generic-element-result", `
struct Handle { fd: i32 }
function (own h: Handle) close(): i32 { return h.fd; }
function first[T](values: T[]): T { return values[0]; }
function take(own h: Handle): i32 { return h.close(); }
function main(): i32 {
    let values = [Handle { fd: 7 }];
    let alias = first(values);
    let n = take(alias);
    return n + values[0].fd;
}`)
}

func TestOwnGuardRejectsAHandleBorrowThroughAGenericLoop(t *testing.T) {
	wantE051(t, "generic-loop-result", `
struct Handle { fd: i32 }
function (own h: Handle) close(): i32 { return h.fd; }
function first[T](values: T[]): T {
    for value in values { return value; }
    return first(values);
}
function take(own h: Handle): i32 { return h.close(); }
function main(): i32 {
    let values = [Handle { fd: 7 }];
    let alias = first(values);
    let n = take(alias);
    return n + values[0].fd;
}`)
}

func TestOwnGuardAcceptsGenericOwnedAndHandleFreeResults(t *testing.T) {
	wantOK(t, "generic-owned-and-handle-free-results", `
struct Handle { fd: i32 }
function (own h: Handle) close(): i32 { return h.fd; }
function transfer[T](own value: T): T { return value; }
function identity[T](value: T): T { return value; }
function first[T](values: T[]): T { return values[0]; }
function take(own h: Handle): i32 { return h.close(); }
function count(own values: i32[]): i32 { return values.len(); }
function main(): i32 {
    let source = [3, 4];
    let copy = identity(source);
    let n = count(copy);
    let texts = ["ok"];
    let text = first(texts);
    let owned = transfer(Handle { fd: 7 });
    return take(owned) + n + source[0] + text.len() + texts.len();
}`)
}
