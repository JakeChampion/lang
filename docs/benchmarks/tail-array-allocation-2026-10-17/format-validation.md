# Archived source format

Date: 2026-10-22.

The probe and collision fixture now use the required source format.
The historical producers still emit both microbenchmark binaries with their archived hashes.
Both binaries return the expected checksum.

The historical candidate also emits identical collision binaries from the original and formatted source on both native targets.
Both collision binaries exit zero. ARM64 executes under QEMU.

The format-results.json and collision-format-results.json files contain commands, hashes and results.
The earlier performance measurements retain their recorded source scope.
