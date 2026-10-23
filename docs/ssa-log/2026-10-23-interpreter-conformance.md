# Interpreter conformance

The interpreter resolves lexical binding identities before it lowers defer statements.
Sibling bindings and value blocks now retain their correct arrays and scope exits.
Zero arrays also keep their declared runtime shape.

Constructor annotations retain the enum family selected in the lexical module.
A program's Ok variant no longer captures a Result built in the stdlib.
Array values retain their view namespace and declared element type.
Concrete and generic view methods now select the correct receiver.

The interpreter implements __arr_set_len through its array output cell.
The cell keeps both physical storage and logical length.
A shrink followed by host input can restore the initialized bytes.

The fixture leg now uses the shipped CLI, built by the current compiler.
Its runtime includes current code generation instead of the older stage0 cleanup.
The recursive-list exception already passes in that shipped CLI on main.

Recursive enum cleanup also follows an owned array's final child in a loop.
Earlier children are released normally. A shared array keeps its children.

A new 30,000-node case covers empty arrays, shared arrays, and shared nodes.
It exhausts the old Wasm call stack and passes with the new helper.
It also passes on both interpreters, native targets, and the native memory sanitizer.
The complete leak census reports no new live bytes.

The full interpreter corpus passes with an empty exception list.
This includes the seven former exceptions. The list remains subject to its stale-entry gate.
This resolves #12888.
