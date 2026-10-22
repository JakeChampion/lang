# Validation after the main update

Base: d669f557c1e75db6baa7473c4e8323b77400df95, 2026-10-22.
Source: f81984b7f4f5cf26b48cccfa953cf9e1a4399ac5.
The Go test runner is the current pin, Go 1.27.3.

The 80 donor ABI probes and ARM64 hardening suite pass in 27.799 seconds.
The PAC mutation and signature-strip control pass.
The broader reuse, WASI, certification, call ABI, sanitizer, heap-class,
inventory and complexity checks pass in 108.195 seconds.
All changed Fern sources pass the formatter check.

Bootstrap stages 2 and 3 match at 14,058,336 bytes, SHA-256
a4043ca9118bbf8059e26a877a18915bb6d955ed5611615fcab0f02ead69d6d6.
Stage 1 differs. The bootstrap record contains all stages and the source fingerprint.
All eleven strict size checks pass with complete coverage and unchanged limits.
The driver build takes 94.530 seconds.

An independent bootstrap of the unchanged base reaches 14,058,416 bytes,
SHA-256 3be44999ff7041c0a240210726a73b854f5d3b9d419e1f34d222d3d6442df8d8.
The base snapshot remains fixed for later certification comparisons.

The main-d669/ directory contains the complete logs, hashes and size report.
The earlier measurements retain their recorded source scope.
