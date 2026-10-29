package e2ecompiler

import "testing"

// The QUIC interop programs of scripts/net-quic-interop build on both native
// targets, where the reference-count certifier checks every function. The
// nightly lane is the only other place that compiles them. The memo is off, so
// an entry from an earlier compile does not skip a function.
func TestSelfHostQuicInteropProgramsCertify(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, name := range []string{"client", "server", "h3_client", "h3_server"} {
		src := langSrcAbs(t, "scripts/net-quic-interop.d/"+name+".fern")
		for _, target := range []string{"x86-64-linux", "arm64-linux"} {
			t.Run(name+"/"+target, func(t *testing.T) {
				cli.emit(t, src, target, "FERN_RC_CERTIFY_MEMO=0")
			})
		}
	}
}
