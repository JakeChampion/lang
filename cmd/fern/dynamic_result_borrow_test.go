package main

import (
	"strings"
	"testing"
)

func TestCheckDynamicMethodCannotLendAHandleToConsume(t *testing.T) {
	entry := writeFern(t, `import "core/cmp";
struct Handle { fd: i32 }
function (own h: Handle) close(): i32 { return h.fd; }
trait Access { function get(self: Self): Handle; }
struct Box { h: Handle }
function (b: Box) get(): Handle { return b.h; }
impl Access for Box {}
function borrow(b: dyn Access): Handle { return b.get(); }
function consume(own h: Handle): i32 { return h.close(); }
function f(b: dyn Access): i32 { return consume(borrow(b)); }
function main(): i32 { return 0; }
`)
	if err := runCheck(entry, ""); err == nil || !strings.Contains(err.Error(), "E051") {
		t.Fatalf("dynamic method result: want E051, got %v", err)
	}
}
