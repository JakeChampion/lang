// Package componenttype writes the component-type custom section of a
// component.
//
// fern.bin, http.bin and proxy.bin hold the component-type payload that
// `wasm-tools component embed cmd/fern/wit -w WORLD` writes for each world.
// The release is the one that mise.toml pins, and the payload ends in a
// producers record that names it. compiler/wit_fern_world.fern and
// compiler/wit_proxy_world.fern carry the same bytes as hex. When mise.toml
// changes the wasm-tools version, regenerate all five files.
package componenttype
