package component

import "strings"

// classify.go turns a wasmbin core module's preview-2 import surface
// (emitted under EmitOptions.Preview2WASI) into a ComposeRequest — the
// single description Compose consumes. It is the importable half of the
// "core bytes → component" operation; the fern CLI driver and the e2e
// test harness both classify through ClassifyCore so they stay in
// lock-step (and neither needs the preview-1 adapter).

// preview2ImportSpec describes one structured preview-2 import the
// composer knows how to route (exit / random / monotonic). Keyed by
// (core-module, core-name) the core wasm module declared.
type preview2ImportSpec struct {
	interfaceName    string
	paramNames       []string
	paramValtypes    []byte
	coreImportModule string
	innerTypes       [][]byte
	resultValtypes   []byte
}

// knownPreview2Imports is the registry of (core-module, core-name) pairs
// that map to a structured component-level WasiImport. Grows as more
// preview-2 imports land in wasmbin (see docs/TOOLCHAIN-SELF-HOSTING.md).
//
// `wasi:cli/exit@0.2.0::exit` takes `func(status: result<_, _>)`, so its
// WasiImport carries InnerTypes = [InnerTypeResultEmpty] and its param
// valtype is byte 0x00 — read by the binary parser as the inner-scope
// typeidx of the result type.
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

// ClassifyCore walks a core module's preview-2 imports and fills a
// ComposeRequest. Every recognised import maps to a request field (CLI
// stdio, io/streams methods + drops, the filesystem open-chain, TCP /
// UDP / HTTP method surfaces, the standalone clock / args / env
// capabilities, and the structured exit/random/monotonic no-opts).
// Anything unrecognised is returned in unsupported so the caller can
// surface a clear error. Because the request fields are independent, ANY
// mix composes — sockets + files, stdout + stderr, args + env, an HTTP
// handler that logs, etc.
func ClassifyCore(bin []byte) (ComposeRequest, []string) {
	var req ComposeRequest
	var unsupported []string
	var getDirs, openAt bool
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
		case m == "wasi:io/streams@0.2.0" && n == "[resource-drop]input-stream":
			req.DropInput = true
		case m == "wasi:io/streams@0.2.0" && n == "[resource-drop]output-stream":
			req.DropOutput = true
		case m == "wasi:filesystem/preopens@0.2.0" && n == "get-directories":
			getDirs = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.open-at":
			openAt = true
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
		case m == "wasi:clocks/wall-clock@0.2.0" && n == "now":
			req.WallNow = true
		case m == "wasi:cli/environment@0.2.0" && n == "get-arguments":
			req.Args = true
		case m == "wasi:cli/environment@0.2.0" && n == "get-environment":
			req.Env = true
		case m == "wasi:sockets/tcp@0.2.0" && n == "[method]tcp-socket.start-connect":
			// Outbound client: pulls in the connect variant of the tcp
			// instance type (start-connect / finish-connect appended).
			req.Tcp = true
			req.TcpConnect = true
		case strings.HasPrefix(m, "wasi:sockets/tcp"):
			req.Tcp = true // wasi:sockets/tcp@ + tcp-create-socket@
		case strings.HasPrefix(m, "wasi:sockets/udp"):
			req.Udp = true
		case m == "wasi:sockets/instance-network@0.2.0":
			// consumed by the TCP / UDP shape; accepted implicitly
		case m == "wasi:clocks/monotonic-clock@0.2.0" && n == "subscribe-duration":
			// wasm reactor timer: returns own<pollable>. Composed
			// standalone (clocks + io/poll) — see WASM-REACTOR-PLAN.md.
			req.Timer = true
		case m == "wasi:io/poll@0.2.0" && n == "poll":
			// wasm reactor multiplexer: poll(list<pollable>) ->
			// list<u32>. Pulls in the heavier io/poll instance type.
			req.Poll = true
		case m == "wasi:io/poll@0.2.0" && n == "[resource-drop]pollable":
			// wasm reactor: drop a consumed pollable. Only the reactor
			// (timer / poll) standalone path needs this lowering added
			// explicitly; the socket paths add their own drop.
			req.PollableDrop = true
		case m == "wasi:io/poll@0.2.0":
			// consumed by the TCP shape / the reactor timer; accepted
			// implicitly (the pollable.block lowering is added
			// explicitly by the Tcp / Udp / Timer request paths).
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
	// Every filesystem method is reached by resolving a preopen and
	// opening a path under it, so get-directories + open-at are the
	// entry to all of them and a method without them is an incomplete
	// chain. Which METHODS follow is a free choice — req.File already
	// carries exactly the set the core imports, and the instance type is
	// built from it — so unlike the old five-way mode there is no
	// combination left to reject.
	if getDirs || openAt || req.File.Any() {
		if !getDirs || !openAt || !req.File.Any() {
			unsupported = append(unsupported, "wasi:filesystem (incomplete open-chain: needs get-directories + open-at + at least one descriptor method)")
		}
	}
	return req, unsupported
}

// RequestEmpty reports whether the request carries no preview-2 imports
// at all — an import-free program that routes to the plain cli/run (or
// lifted-export) builder rather than the composer.
func RequestEmpty(req ComposeRequest) bool {
	return !req.Stdout && !req.Stderr && !req.Stdin &&
		!req.BlockWrite && !req.BlockRead && !req.DropInput && !req.DropOutput &&
		!req.File.Any() &&
		!req.Tcp && !req.Udp && !req.Http && !req.Timer && !req.Poll && !req.PollableDrop && !req.TcpConnect &&
		!req.WallNow && !req.Args && !req.Env && len(req.Structured) == 0
}

// coreModuleImport is one (module, name) pair from the import section.
type coreModuleImport struct{ module, name string }

// coreModuleImportPairs walks a core wasm module's import section and
// returns each (module, name) pair in declaration order. Bails out
// silently on malformed input or no import section.
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
			case 0: // func: typeidx uleb
				_, ks := readULEB(body3)
				body3 = body3[ks:]
			case 1: // table: reftype byte + limits
				if len(body3) >= 2 {
					body3 = body3[2:]
					_, ks := readULEB(body3)
					body3 = body3[ks:]
				}
			case 2: // memory: limits
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
			case 3: // global: valtype byte + mut byte
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

// readULEB decodes a uleb128-encoded uint up to 10 bytes; returns
// (value, bytes consumed). Returns (0, 0) on malformed input.
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

// readName reads a uleb-prefixed UTF-8 name; returns (name, rest) or
// ("", b) when malformed.
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
