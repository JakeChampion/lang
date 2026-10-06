// Package bootstrap holds the tests for bootstrap/bootstrap.sh, the driver
// behind `make bootstrap`. The script is exercised with a fake compiler and a
// fake curl so the lock parsing, download verification and the stage2 == stage3
// check run in well under a second; the real chain is the bootstrap CI job.
package bootstrap
