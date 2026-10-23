# Validation on main cebda6528

Base: cebda6528fdc0fd368a42d5061c86490df04fee5, 2026-10-22.
Source: 62fec1eda, before later main updates.

The raw-word, call ABI, IR registry and complexity checks pass in 62.069 seconds.
The raw-word tests execute both native targets through GAS and the native writer.
They include sanitizer modes and independent byte access at every word offset.

Bootstrap stages 2 and 3 match at 13,305,792 bytes, SHA-256
13cba772f37dc25a9920833ff937707aa3fba92524563f77ade7c2d9086736ce.
Stage 1 differs. The bootstrap record contains all stages and the source fingerprint.
All eleven strict size checks pass with complete coverage and unchanged limits.
The driver build takes 91.756 seconds. These Go tests used Go 1.27.1.

The main-ceb/ directory contains the complete logs and size report.
The earlier measurements retain their recorded source scope.

The archived probe now uses the required source format.
Both historical producers still emit binaries with the archived hashes and the expected checksum.
The format-results.json file records the fresh commands and source hash.
