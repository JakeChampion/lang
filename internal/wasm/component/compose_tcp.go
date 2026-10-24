package component

var (
	composeTcpCreateParams     = []byte{0x7f, 0x7f}
	composeTcpSelfRetParams    = []byte{0x7f, 0x7f}
	composeTcpSelfI32RetParams = []byte{0x7f, 0x7f, 0x7f}
	composeTcpSelfI64RetParams = []byte{0x7f, 0x7e, 0x7f}
	composeTcpStartBindParams  = repeatI32(15)
)

func repeatI32(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = 0x7f
	}
	return b
}
