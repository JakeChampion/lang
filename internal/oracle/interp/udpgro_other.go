//go:build !linux

package interp

// udpGRO is unset: the host has no UDP_GRO, so socket op 10 answers
// -ENOPROTOOPT.
const udpGRO = -1
