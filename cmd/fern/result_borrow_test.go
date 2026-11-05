package main

import (
	"strings"
	"testing"
)

// core/cmp rewrites method calls before the owned-argument check. A read
// of a cell still copies its value after that rewrite (#14234).
func TestCheckIgnoresAMethodReceiverAfterTheRewrite(t *testing.T) {
	entry := writeFern(t, `import "core/cmp";
struct Keys { a: Cell[i32[]] }
function take(slot: Cell[i32[]]): i32[] {
    let got = slot.get();
    slot.set([]);
    return got;
}
function consume(own xs: i32[]): i32 { return xs.len(); }
function f(k: Keys): i32 { return consume(take(k.a)); }
function main(): i32 { return 0; }
`)
	if err := runCheck(entry, ""); err != nil {
		t.Fatalf("check: %v", err)
	}
}

func TestCheckBorrowedMethodResultCannotBeConsumedAfterRewrite(t *testing.T) {
	entry := writeFern(t, `import "core/cmp";
struct Handle { fd: i32 }
function (own h: Handle) close(): i32 { return h.fd; }
struct Box { h: Handle }
function (b: Box) get(): Handle { return b.h; }
function borrow(b: Box): Handle { return b.get(); }
function consume(own h: Handle): i32 { return h.close(); }
function f(b: Box): i32 { return consume(borrow(b)); }
function main(): i32 { return 0; }
`)
	if err := runCheck(entry, ""); err == nil || !strings.Contains(err.Error(), "E051") {
		t.Fatalf("borrowed method result: want E051, got %v", err)
	}
}

func TestCheckBorrowedMethodResultCannotBeConsumedWithoutImports(t *testing.T) {
	entry := writeFern(t, `struct Handle { fd: i32 }
function (own h: Handle) close(): i32 { return h.fd; }
struct Box { h: Handle }
function (b: Box) get(): Handle { return b.h; }
function borrow(b: Box): Handle { return b.get(); }
function consume(own h: Handle): i32 { return h.close(); }
function f(b: Box): i32 { return consume(borrow(b)); }
function main(): i32 { return 0; }
`)
	if err := runCheck(entry, ""); err == nil || !strings.Contains(err.Error(), "E051") {
		t.Fatalf("borrowed method result: want E051, got %v", err)
	}
}
