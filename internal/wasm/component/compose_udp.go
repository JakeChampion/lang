package component

var (
	udpCreateParams     = []byte{0x7f, 0x7f}
	udpSelfRetParams    = []byte{0x7f, 0x7f}
	udpBindStreamParams = repeatI32(15)
	udpSendParams       = repeatI32(4)
)
