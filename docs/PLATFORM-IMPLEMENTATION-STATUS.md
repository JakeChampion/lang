# Platform implementation status

Status: [tracker] current source evidence and remaining work for #4414.

This baseline covers issue #4414 at revision `6bed61dd7f1b8ee631fc0fcdb153a00b68c1260f` on 2026-10-31.
The [research survey](PLATFORM-RESEARCH.md) preserves the earlier proposals.
Future changes start from the interfaces below.

## Present interfaces

A stateless handler takes a request and a platform value.
A native stateful handler also takes the state from `init` and returns the next state with its response.
The platform parameter can use the `Platform` trait or the concrete `Host` type.

```fern
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse
function init(): State
function handle(state: State, req: HttpRequest, plat: platform.Host): (State, HttpResponse)
```

The signatures illustrate separate stateless and stateful programs.
The native wrapper calls `init`, supplies its result to the server, and supports a `shutdown` hook.
An explicit user `main` controls its own lifecycle.
The current memory model uses reference counts for this state.
The historical permanent-arena proposal requires a new design assessment.

| Recommendation | Current source evidence | Remaining scope |
|---|---|---|
| 1. Platform parameter | `std/platform` defines `Platform` and `Host`. Native wrappers and stateless proxy handlers accept the platform parameter. | Preserve the current interface and target-specific tests. |
| 2. Platform descriptors | The compiler and Go registry provide typed descriptions of built-in targets and capabilities. | Define the external descriptor contract, signatures, glue, and named bindings. |
| 3. Init and invoke | Native wrappers support init results, server options, state, and shutdown. The checkers diagnose mismatched state shapes. | Stateful `wasm32-wasi-http` handlers remain unsupported. The Go checker reports E075. |
| 4. Stream-default response | Streamed response forms and `ByteStream` request bodies already exist. | Specify the remaining stream-default contract. Coordinate body API work with #12726. |
| 6. Mock platform | `MockPlatform` implements `platform.Platform`, supplies canned results, and records operations in a shared call log. | Preserve parity as the platform trait changes. |
| 7 and 8. Named and service bindings | The registry has binding metadata. Host configuration and mock values already exist. | Implement deploy-time named and service bindings when their deployment trigger applies. |
| 5. Other handler kinds | Target descriptors name `handle` and `main` where supported. | Scheduled, alarm, and other handler entries retain their separate triggers. |
| 10. Effect rows | Target capability checks reject unavailable builtins through reachable calls. | A source-level effect row remains open work. |

These rows describe the current implementation and its gaps.
They do not establish complete acceptance of issue #4414.

## Source and test evidence

- [Platform trait and host](../internal/stdlib/std/platform.fern.md)
- [Mock platform and call log](../internal/stdlib/std/mock_platform.fern.md)
- [Native handler wrapper](../compiler/flatten.fern)
- [Self-host handler state diagnostics](../compiler/checker.fern)
- [Compiler target registry](../compiler/platforms.fern)
- [Go target registry](../internal/pkg/platforms/platforms.go)
- [Go handler and lifecycle checks](../internal/check/checker/checker_test.go)
- [Self-host handler state checks](../internal/testing/e2ecompiler/self_host_handler_state_test.go)
- [Self-host mock checks](../internal/testing/e2ecompiler/self_host_mock_platform_ir_test.go)

The registry suite passes 332 results at this revision.
The selected Go handler and lifecycle checks pass 43 results.
Both suites report zero skips.
The proxy-state diagnostic test verifies E075 for the stateful shape and accepts the stateless shape.
These results cover the Go registry and checker.
Self-host and target runtime execution remain separate validation steps for future code changes.

## Remaining acceptance criteria

The remaining descriptor and lifecycle contracts require implementation and target-specific evidence.
A complete lifecycle change must define state ownership, startup failure, cleanup, and the behavior of each target.
It must cover native execution and proxy components.
The descriptor work must connect its declared capabilities and bindings to the compiler's current capability checks.
The stream, bindings, handler-kind, and effect-row work must retain their stated scope and triggers.

The handler parameter, native init, and mock platform already form part of the baseline.
Their migration must not repeat as new work.
Issue #4414 remains open while its applicable acceptance criteria remain incomplete.
