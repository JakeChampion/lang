# Platform implementation status

Status: [record] source evidence for #4414, which is closed. Each remaining item has its own issue and trigger.
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
| 2. Platform descriptors | The compiler and Go registry provide typed descriptions of built-in targets and capabilities. | A loader for user-defined platforms waits for a consumer (#16245). |
| 3. Init and invoke | Native wrappers support init results, server options, state, and shutdown. The checkers diagnose mismatched state shapes. The issue records this foundation as complete. | Preserve the native lifecycle. The proxy target has a separate stateless contract and reports E075 for stateful handlers. |
| 4. Stream-default response | Both host adapters give the handler a lazy `ByteStream` request body. Both write `BodyStream` and `BodyChunks` one chunk at a time and send the trailers. A pull producer replaces the push writer of the research proposal. | Complete. The naming of the body API is #12726. |
| 6. Mock platform | `MockPlatform` implements `platform.Platform`, supplies canned results, and records operations in a shared call log. | Preserve parity as the platform trait changes. |
| 7 and 8. Named and service bindings | The registry has binding metadata. Host configuration and mock values already exist. | Wait for a host that can give a binding to the guest (#16247). |
| 5. Other handler kinds | Target descriptors name `handle` and `main` where supported. | Wait for a host that can call a scheduled or alarm handler (#16246). |
| 10. Effect rows | Effect inference and `fern -effects` ship. E070 and E066 enforce package and target capabilities. The declared `uses [...]` prototype was deliberately removed. | Superseded. Preserve the decision in [Effect rows: prototype and findings](EFFECT-ROWS-BRIEF.md). Do not restore the removed syntax. |

These rows describe the current implementation and its gaps.
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

## Proxy adapter

The proxy adapter (`std/wasi_http`) calls `handle` and does not call `init` or `shutdown`.
The compiler rejects those top-level hooks with E075 for `wasm32-wasi-http`.
The diagnostic names the unsupported hook at its declaration.
It applies to check mode, component output, and core-module output.
Methods and hooks in imported modules remain ordinary functions.

The adapter reads the request body lazily from the host input stream.
A declared `Content-Length` past the body limit gets a 413 before the handler runs.
A body that goes past the limit while it streams sets `StreamTooLarge` on the stream, as `std/serve` does.
The adapter writes each chunk of the response body when the body gives it, and then sends the trailers.
The proxy world has no filesystem, so a `BodyFile` or `BodyFileRanges` response gets a 500.

The [proxy lifecycle tests](../internal/testing/e2ecompiler/self_host_wasi_http_lifecycle_test.go) check the hook boundary and the absence of an output artifact after refusal.
The [proxy body test](../internal/testing/e2ecompiler/self_host_wasi_http_streaming_test.go) checks the duplex body, the trailers, the file refusal, and the body limit.

## Remaining work

Each remaining item waits for a consumer or a host, and has its own issue:

- #16245: a loader for a user-defined platform.
- #16246: scheduled and alarm handler kinds.
- #16247: named bindings and service bindings.

Explicit effect-row syntax is superseded and is outside this work.
