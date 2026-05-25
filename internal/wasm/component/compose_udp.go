package component

// compose_udp.go holds the core-import signature constants for the
// send-only UDP client (udp_send) surface. The unified composer
// (compose_unified.go) declares each udp method as a gImport lowering
// over these. Every udp method lowers as a memory trampoline (retptr
// result / a list param the host reads) with no realloc; the datagram
// path is its own resources (not wasi:io/streams).

var (
	udpCreateParams     = []byte{0x7f, 0x7f} // (family, retptr)
	udpSelfRetParams    = []byte{0x7f, 0x7f} // (self, retptr)
	udpBindStreamParams = repeatI32(15)      // self, (option<ip-socket-address> flat = 13), retptr
	udpSendParams       = repeatI32(4)       // self, list_ptr, list_len, retptr
)
