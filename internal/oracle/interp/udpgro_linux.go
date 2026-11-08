//go:build linux

package interp

// udpGRO is Linux's UDP_GRO option: the kernel coalesces datagrams of one
// size from one sender into a message with a UDP_GRO control message.
const udpGRO = 104
