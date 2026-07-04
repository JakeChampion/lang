package e2e

// The byte-identical self-hosting fixpoint is now proven file-based by
// TestSelfHostModloadFixpointX86_64 (compiling asm_modload_run's own
// source graph through the import-driven loader). The former marker-bundle
// TestSelfHostFixpoint — which drove the retired bundle_run.fern — was
// removed once the file-based twin covered the same mmc == gen2 == gen3
// guarantee. This file retains buildBin, the shared asm→binary linker used
// across the self-host suite.
