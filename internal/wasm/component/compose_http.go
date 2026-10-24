package component

import "github.com/jakechampion/lang/internal/wasm/leb128"

func handleFunctypeBody(ownReqIdx, ownOutParamIdx uint32) []byte {
	body := []byte{0x01, 0x40, 0x02}
	body = putName(body, "request")
	body = leb128.UlebU64(body, uint64(ownReqIdx))
	body = putName(body, "response-out")
	body = leb128.UlebU64(body, uint64(ownOutParamIdx))
	body = append(body, 0x01, 0x00)
	return body
}

func (c *p2composer) emitIncomingHandlerExport(reqResourceT, outparamResourceT, handleCoreFunc uint32) {
	ownReqT := c.typeRaw([]byte{0x01, 0x69, byte(reqResourceT)})
	ownOutT := c.typeRaw([]byte{0x01, 0x69, byte(outparamResourceT)})
	handleFuncT := c.typeRaw(handleFunctypeBody(ownReqT, ownOutT))

	c.buf = PutCanonSectionLiftNoOpts(c.buf, handleCoreFunc, handleFuncT)
	liftedCFunc := c.nCFunc
	c.nCFunc++
	c.buf = PutInstanceSectionOnePackagedFunc(c.buf, "handle", liftedCFunc)
	hInst := c.nInst
	c.nInst++
	c.buf = PutExportSectionOneInstance(c.buf, "wasi:http/incoming-handler@0.2.0", hInst)
}

func BuildHttpIncomingHandlerComponent(coreBytes []byte, coreExportName string) []byte {
	c := &p2composer{buf: PutComponentHeader(nil)}

	tErr := c.typeRaw(WasiIoErrorInstanceTypeBody())
	errInst := c.importInstance("wasi:io/error@0.2.0", tErr)
	tErrAlias := c.aliasType(errInst, "error")

	tStreams := c.typeRaw(WasiIoStreamsReadWriteInstanceTypeBody(tErrAlias))
	streamsInst := c.importInstance("wasi:io/streams@0.2.0", tStreams)
	tOut := c.aliasType(streamsInst, "output-stream")
	tIn := c.aliasType(streamsInst, "input-stream")

	tHttp := c.typeRaw(WasiHttpTypesInstanceTypeBody(tIn, tOut))
	httpInst := c.importInstance("wasi:http/types@0.2.0", tHttp)
	tReq := c.aliasType(httpInst, "incoming-request")
	tOutparam := c.aliasType(httpInst, "response-outparam")

	coreInst := c.instantiate(c.coreModule(coreBytes))
	handleCoreF := c.aliasCoreFunc(coreInst, coreExportName)

	c.emitIncomingHandlerExport(tReq, tOutparam, handleCoreF)
	return c.buf
}

var httpSelfRetParams = []byte{0x7f, 0x7f}

var httpOutparamSetParams = []byte{0x7f, 0x7f, 0x7f, 0x7f, 0x7e, 0x7f, 0x7f, 0x7f, 0x7f}
