package component

// compose_tcp.go holds the core-import signature constants for the
// TCP-server surface (tcp_listen / tcp_accept / tcp_recv / tcp_send /
// tcp_close) and the shared repeatI32 helper. The unified composer
// (compose_unified.go) declares each tcp method as a gImport lowering
// over these; ensureTcp (compose_general.go) surfaces the shared types.

var (
	composeTcpCreateParams    = []byte{0x7f, 0x7f} // (family, retptr)
	composeTcpSelfRetParams   = []byte{0x7f, 0x7f} // (self, retptr)
	composeTcpStartBindParams = repeatI32(15)      // self, network, disc, 11 flat, retptr
)

func repeatI32(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = 0x7f
	}
	return b
}
