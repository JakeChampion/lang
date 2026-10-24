package component

import "strings"

type preview2ImportSpec struct {
	interfaceName    string
	paramNames       []string
	paramValtypes    []byte
	coreImportModule string
	innerTypes       [][]byte
	resultValtypes   []byte
}

var knownPreview2Imports = map[[2]string]preview2ImportSpec{
	{"wasi:cli/exit@0.2.0", "exit"}: {
		interfaceName:    "wasi:cli/exit@0.2.0",
		paramNames:       []string{"status"},
		paramValtypes:    []byte{0x00},
		coreImportModule: "wasi:cli/exit@0.2.0",
		innerTypes:       [][]byte{InnerTypeResultEmpty},
	},
	{"wasi:random/random@0.2.0", "get-random-u64"}: {
		interfaceName:    "wasi:random/random@0.2.0",
		coreImportModule: "wasi:random/random@0.2.0",
		resultValtypes:   []byte{CValtypeU64},
	},
	{"wasi:clocks/monotonic-clock@0.2.0", "now"}: {
		interfaceName:    "wasi:clocks/monotonic-clock@0.2.0",
		coreImportModule: "wasi:clocks/monotonic-clock@0.2.0",
		resultValtypes:   []byte{CValtypeU64},
	},
}

func ClassifyCore(bin []byte) (ComposeRequest, []string) {
	var req ComposeRequest
	var unsupported []string
	var getDirs bool
	for _, p := range coreModuleImportPairs(bin) {
		m, n := p.module, p.name
		switch {
		case m == "wasi:cli/stdout@0.2.0" && n == "get-stdout":
			req.Stdout = true
		case m == "wasi:cli/stderr@0.2.0" && n == "get-stderr":
			req.Stderr = true
		case m == "wasi:cli/stdin@0.2.0" && n == "get-stdin":
			req.Stdin = true
		case m == "wasi:io/streams@0.2.0" && n == "[method]output-stream.blocking-write-and-flush":
			req.BlockWrite = true
		case m == "wasi:io/streams@0.2.0" && n == "[method]input-stream.blocking-read":
			req.BlockRead = true
		case m == "wasi:io/streams@0.2.0" && (n == "[method]input-stream.subscribe" || n == "[method]output-stream.subscribe" || n == "[method]input-stream.read"):
			req.StreamReactor = true
		case m == "wasi:io/streams@0.2.0" && n == "[resource-drop]input-stream":
			req.DropInput = true
		case m == "wasi:io/streams@0.2.0" && n == "[resource-drop]output-stream":
			req.DropOutput = true
		case m == "wasi:io/error@0.2.0" && n == "[resource-drop]error":
			req.DropError = true
		case m == "wasi:filesystem/preopens@0.2.0" && n == "get-directories":
			getDirs = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.open-at":
			req.File.OpenAt = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[resource-drop]descriptor":
			req.File.DropDesc = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.read-via-stream":
			req.File.Read = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.write-via-stream":
			req.File.Write = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.append-via-stream":
			req.File.Append = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.unlink-file-at":
			req.File.Unlink = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.create-directory-at":
			req.File.Mkdir = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.link-at":
			req.File.Link = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.symlink-at":
			req.File.Symlink = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.readlink-at":
			req.File.Readlink = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.rename-at":
			req.File.Rename = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.set-times-at":
			req.File.SetTimes = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.set-size":
			req.File.SetSize = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.sync":
			req.File.Sync = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.sync-data":
			req.File.SyncData = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.advise":
			req.File.Advise = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.stat-at":
			req.File.Stat = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.stat":
			req.File.StatSelf = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.remove-directory-at":
			req.File.Rmdir = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.read-directory",
			m == "wasi:filesystem/types@0.2.0" && n == "[method]directory-entry-stream.read-directory-entry",
			m == "wasi:filesystem/types@0.2.0" && n == "[resource-drop]directory-entry-stream":
			req.File.ReadDir = true
		case m == "wasi:clocks/wall-clock@0.2.0" && n == "now":
			req.WallNow = true
		case m == "wasi:cli/environment@0.2.0" && n == "get-arguments":
			req.Args = true
		case m == "wasi:cli/environment@0.2.0" && n == "get-environment":
			req.Env = true
		case m == wasiConfigStore && n == "get":
			req.Config = true
		case m == "wasi:sockets/tcp@0.2.0" && (n == "[method]tcp-socket.start-connect" || n == "[method]tcp-socket.finish-connect"):
			req.Tcp = true
			req.TcpConnect = true
		case strings.HasPrefix(m, "wasi:sockets/tcp"):
			req.Tcp = true
		case m == "wasi:sockets/udp@0.2.0" && n == "[method]incoming-datagram-stream.receive":
			req.Udp = true
			req.UdpRecv = true
		case strings.HasPrefix(m, "wasi:sockets/udp"):
			req.Udp = true
		case m == "wasi:sockets/instance-network@0.2.0":
		case m == "wasi:clocks/monotonic-clock@0.2.0" && n == "subscribe-duration":
			req.Timer = true
		case m == "wasi:io/poll@0.2.0" && n == "poll":
			req.Poll = true
		case m == "wasi:io/poll@0.2.0" && n == "[resource-drop]pollable":
			req.PollableDrop = true
		case m == "wasi:io/poll@0.2.0":
		case m == "wasi:http/types@0.2.0":
			req.Http = true
		default:
			if spec, ok := knownPreview2Imports[[2]string{m, n}]; ok {
				req.Structured = append(req.Structured, WasiImport{
					InterfaceName:    spec.interfaceName,
					FuncName:         n,
					ParamNames:       spec.paramNames,
					ParamValtypes:    spec.paramValtypes,
					CoreImportModule: spec.coreImportModule,
					InnerTypes:       spec.innerTypes,
					ResultValtypes:   spec.resultValtypes,
				})
			} else {
				unsupported = append(unsupported, m+"."+n)
			}
		}
	}
	if getDirs && !req.File.Any() {
		unsupported = append(unsupported, "wasi:filesystem (incomplete chain: get-directories with no descriptor method)")
	}
	return req, unsupported
}

func RequestEmpty(req ComposeRequest) bool {
	return !req.Stdout && !req.Stderr && !req.Stdin &&
		!req.BlockWrite && !req.BlockRead && !req.DropInput && !req.DropOutput && !req.DropError &&
		!req.File.Any() &&
		!req.Tcp && !req.Udp && !req.Http && !req.Timer && !req.Poll && !req.PollableDrop && !req.TcpConnect &&
		!req.WallNow && !req.Args && !req.Env && !req.Config && len(req.Structured) == 0
}

type coreModuleImport struct{ module, name string }

func coreModuleImportPairs(bin []byte) []coreModuleImport {
	const preambleLen = 8
	if len(bin) < preambleLen {
		return nil
	}
	off := preambleLen
	for off < len(bin) {
		id := bin[off]
		off++
		size, n := readULEB(bin[off:])
		if n == 0 {
			return nil
		}
		off += n
		if off+int(size) > len(bin) {
			return nil
		}
		body := bin[off : off+int(size)]
		off += int(size)
		if id != 2 {
			continue
		}
		count, m := readULEB(body)
		if m == 0 {
			return nil
		}
		body = body[m:]
		var pairs []coreModuleImport
		for i := uint64(0); i < count && len(body) > 0; i++ {
			mod, body2 := readName(body)
			fld, body3 := readName(body2)
			if len(body3) < 1 {
				break
			}
			kind := body3[0]
			body3 = body3[1:]
			switch kind {
			case 0:
				_, ks := readULEB(body3)
				body3 = body3[ks:]
			case 1:
				if len(body3) >= 2 {
					body3 = body3[2:]
					_, ks := readULEB(body3)
					body3 = body3[ks:]
				}
			case 2:
				if len(body3) >= 1 {
					flag := body3[0]
					body3 = body3[1:]
					_, ks := readULEB(body3)
					body3 = body3[ks:]
					if flag == 1 {
						_, ks2 := readULEB(body3)
						body3 = body3[ks2:]
					}
				}
			case 3:
				if len(body3) >= 2 {
					body3 = body3[2:]
				}
			}
			body = body3
			pairs = append(pairs, coreModuleImport{module: mod, name: fld})
		}
		return pairs
	}
	return nil
}

func readULEB(b []byte) (uint64, int) {
	var v uint64
	var shift uint
	for i := 0; i < 10 && i < len(b); i++ {
		x := b[i]
		v |= uint64(x&0x7f) << shift
		if x&0x80 == 0 {
			return v, i + 1
		}
		shift += 7
	}
	return 0, 0
}

func readName(b []byte) (string, []byte) {
	n, k := readULEB(b)
	if k == 0 {
		return "", b
	}
	b = b[k:]
	if uint64(len(b)) < n {
		return "", b
	}
	return string(b[:n]), b[n:]
}
