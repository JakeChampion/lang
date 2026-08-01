package e2e

// The byte-identical self-hosting fixpoint is proven by
// TestSelfHostPerModuleEmitAllFixpointBatch4X86_64, which compiles the whole
// compiler PER MODULE and asserts gen0 == gen1. Every merged-bundle fixpoint
// that came before it — the marker-bundle TestSelfHostFixpoint over the retired
// bundle_run.fern, then the file-based TestSelfHostModloadFixpointX86_64 — is
// gone: a merged whole-compiler bundle is past the 512-function IR budget, so
// once #3457 slice 5 deleted the AST emitters there was nothing left to compile
// one. This file retains buildBin, the shared asm→binary linker used across the
// self-host suite.
