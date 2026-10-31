# Platform implementation status

Status: [tracker] current source evidence and remaining work for #4414.

This baseline covers issue #4414 at revision `6bed61dd7f1b8ee631fc0fcdb153a00b68c1260f` on 2026-10-31.
The [research survey](PLATFORM-RESEARCH.md) preserves the earlier proposals.
Future changes start from the interfaces below.

## Present interfaces

A stateless handler takes a request and a platform value.
A native stateful handler also takes the state from `init` and returns the next state with its response.
The platform parameter can use the `Platform` trait or the concrete `Host` type.
The trait is shared across targets.
It is not generated from an external target descriptor.

```fern
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse
function init(): State
function handle(state: State, req: HttpRequest, plat: platform.Host): (State, HttpResponse)
```

The signatures illustrate separate stateless and stateful programs.
The native wrapper calls `init`, supplies its result to the server, and supports a `shutdown` hook.
An explicit user `main` controls its own lifecycle.
The current memory model uses reference counts for this state.
The permanent-arena proposal is historical.

The current proxy wrapper assumes a fresh instance for each request and rejects stateful handlers.
Any proxy init contract must respect that instance lifetime.

| Recommendation | Current source evidence | Remaining scope |
|---|---|---|
| 1. Platform parameter | `std/platform` defines `Platform` and `Host`. Native wrappers and stateless proxy handlers accept the platform parameter. | Preserve the shared ABI and E066 checks after tree-shaking. The per-target generated struct proposal is superseded. |
| 2. Platform descriptors | The compiler and Go registry provide typed descriptions of built-in targets and capabilities. | Implement a loader for user-defined platforms with capability signatures and glue. Preserve the shared Platform surface and existing capability enforcement. |
| 3. Init and invoke | Native wrappers support init results, server options, state, and shutdown. The checkers diagnose mismatched state shapes. The issue records this foundation as complete. | Preserve the native lifecycle. The proxy target has a separate stateless contract and reports E075 for stateful handlers. |
| 4. Stream-default response | Streamed response forms and `ByteStream` request bodies already exist. | Specify the remaining stream-default contract. Coordinate body API work with #12726. |
| 6. Mock platform | `MockPlatform` implements `platform.Platform`, supplies canned results, and records operations in a shared call log. | Preserve parity as the platform trait changes. |
| 7 and 8. Named and service bindings | The registry has binding metadata. Host configuration and mock values already exist. | A host must supply the bindings before the deployment trigger applies. Preserve that prerequisite. |
| 5. Other handler kinds | Target descriptors name `handle` and `main` where supported. | Scheduled, alarm, and other handler entries retain their separate triggers. |
| 10. Effect rows | Effect inference and `fern -effects` ship. E070 and E066 enforce package and target capabilities. The declared `uses [...]` prototype was deliberately removed. | Superseded. Preserve the decision in [Effect rows: prototype and findings](EFFECT-ROWS-BRIEF.md). Do not restore the removed syntax. |

These rows describe the current implementation and its gaps.
They do not establish complete acceptance of issue #4414.
The issue discussion records the [completed foundation](https://github.com/JakeChampion/lang/issues/4414#issuecomment-5509146958) and the [superseded designs and remaining triggers](https://github.com/JakeChampion/lang/issues/4414#issuecomment-5519144454).

## Source and test evidence

- [Platform trait and host](../internal/stdlib/std/platform.fern.md)
- [Mock platform and call log](../internal/stdlib/std/mock_platform.fern.md)
- [Native handler wrapper](../compiler/flatten.fern)
- [Proxy handler boundary and refusal](../compiler/emitforms.fern)
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

## Proxy hook guard in this draft

The proxy adapter calls `handle` and does not call `init` or `shutdown`.
This draft rejects those top-level hooks with E075 for `wasm32-wasi-http`.
The diagnostic names the unsupported hook at its declaration.
It applies to check mode, component output, and core-module output.
Methods and hooks in imported modules remain ordinary functions.

The [proxy lifecycle tests](../internal/testing/e2ecompiler/self_host_wasi_http_lifecycle_test.go) check this boundary and the absence of an output artifact after refusal.
The native init and shutdown contracts retain their current behavior.
The guard prevents silent omission of a declared proxy lifecycle hook.
It does not implement proxy initialization or complete issue #4414.

## Remaining acceptance criteria

The user-defined platform loader and remaining stream contract require implementation and target-specific evidence.
The loader must connect its declared capabilities to the compiler's current capability checks.
The stream work must cover the proxy resource ABI and coordinate with #12726.
Named bindings, service bindings, and other handler kinds retain their host prerequisites and stated triggers.
Explicit effect-row syntax is superseded and is outside this remaining work.

The handler parameter, native init, and mock platform already form part of the baseline.
Their migration must not repeat as new work.
Issue #4414 remains open while its applicable acceptance criteria remain incomplete.
