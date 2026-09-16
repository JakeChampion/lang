# 2026-09-16 — a void method is a statement too

`plat.log("serving " + req.path);` refused its function as "unsupported
void call": the three method paths — nominal `Type.method`, the folded
generic array and map methods, and a primitive receiver's — refused a
void result outright, where `direct_call` had refused one only in
expression position since the flip. `mock_platform_log` lost five
functions to it, `diag_e073` one.

The three paths now take the statement flag the call carries and refuse
a void result only where a value is wanted. Nothing else changes: a void
call in statement position was already a call with no result, and the
physical call stores the dummy every void callee returns.
`mock_platform_log` produces whole and matches native under the leak
check.

## Pinned

`TestSelfHostSemanticSourceRC` runs `show_pt`, a void method on a record
holding a fresh string, on all four targets under the leak check; the
print golden carries `log_bag`, a void method on a record and one on a
generic receiver, each in statement position.
