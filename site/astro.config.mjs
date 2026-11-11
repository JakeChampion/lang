// Astro + Starlight config for the Fern documentation site.
//
// The site is at the root of fern-lang.netlify.app, with the playground
// bundle at /playground/.
//
// Four top-level sections in the sidebar:
//   - Tutorial: narrative learn-the-language pages, ordered.
//   - HTTP guide: serving and calling HTTP, basic to advanced, ordered.
//   - Reference: syntax + types + tooling.
//   - Stdlib: auto-generated from internal/stdlib/*.fern by
//     `fern -doc`; pages exist under src/content/docs/stdlib/
//     after that build step, and `stdlibSidebar()` groups whatever
//     it finds there by purpose. A clean checkout where it
//     hasn't run keeps the section, with just its overview link.

import { existsSync, readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { defineConfig } from "astro/config";
import mdx from "@astrojs/mdx";
import starlight from "@astrojs/starlight";

import { playgroundLinks } from "./src/lib/ec-playground-link.mjs";
import { stdlibItems } from "./src/lib/stdlib-sidebar.mjs";

// A Netlify deploy preview sets SITE_URL to its own origin.
const siteUrl = process.env.SITE_URL ?? "https://fern-lang.netlify.app";

// One-line summary reused for the meta description, the Open Graph
// card, and Starlight's own `description` — so search results, chat
// unfurls, and the site itself all say the same thing.
const tagline =
  "Fern is a small statically typed language that compiles to a fast standalone binary — or to WebAssembly, from the same source. No runtime, no garbage collector, nothing else to install.";

// Social-card image. Crawlers won't resolve a relative URL, so this has to
// be absolute, on the origin of this deploy. Rendered by `npm run og` from
// src/assets/og-card.svg.
const ogImage = `${siteUrl.replace(/\/$/, "")}/og.png`;

// Load the real Fern TextMate grammar (the same one the VS Code
// extension ships) so ```fern code fences highlight as actual Fern
// rather than borrowing TypeScript's grammar. Read from the repo
// source so the grammar has a single source of truth — editing the
// extension's grammar updates the docs colours too.
// Standard-library sidebar, grouped by what a module is for.
// `autogenerate` gives one flat alphabetical run of ~70 pages, which is
// a list to scroll rather than a thing to browse. Membership is keyed by
// module slug; anything `fern -doc` emits that isn't listed here still shows
// up, under "Other", so a new stdlib module is never invisible — it just
// wants a home.
const STDLIB_GROUPS = [
  ["Text", ["ansi", "format", "glob", "peg", "regex", "strdist", "string",
    "table", "textwrap", "unicode", "utf8"]],
  ["Data & encoding", ["base32", "base64", "binary", "brotli", "brotli_dictionary", "brotli_encoder", "codec", "crypto", "crypto_aead", "crypto_aes_gcm", "crypto_chacha20poly1305",
    "crypto_ecdsa", "crypto_ed25519", "crypto_field25519", "crypto_mlkem768", "crypto_montgomery", "crypto_p256", "crypto_rsa", "crypto_sha2", "crypto_x25519", "csv", "deflate", "fdt", "hash", "hash_bsd_sum", "hash_cksum", "hash_crc32", "hash_sysv_sum", "hex", "jpeg", "json", "png", "protobuf", "pvh", "semver", "truetype", "url", "uuid", "zip", "zstd"]],
  ["Audio", ["audio_aiff", "audio_au", "audio_linear", "audio_caf", "audio_caf_chunks", "audio_caf_file", "audio_caf_metadata", "audio_caf_source_chunks", "audio_caf_linear", "audio_alac", "audio_alac_layout", "audio_alac_bits", "audio_alac_config", "audio_alac_entropy", "audio_alac_predict", "audio_vorbisfile", "audio_vorbisfile_seek", "audio_vorbisfile_input", "audio_vorbisfile_timing", "audio_vorbis", "audio_vorbis_bits", "audio_vorbis_codebook", "audio_vorbis_dsp", "audio_vorbis_floor0", "audio_vorbis_floor1", "audio_vorbis_headers", "audio_vorbis_packet", "audio_vorbis_residue", "audio_vorbis_setup", "audio_vorbis_tables", "audio_vorbis_types", "audio_aac", "audio_aac_bits", "audio_aac_config", "audio_aac_dsp", "audio_aac_ics", "audio_aac_stereo", "audio_aac_tables", "audio_aac_tns", "audio_ogg", "audio_ogg_tables", "audio_opusfile", "audio_opusfile_headers", "audio_opusfile_input", "audio_opusfile_packets", "audio_opusfile_timing", "audio_file", "audio_flac", "audio_flac_encoder", "audio_flac_plan", "audio_flac_lpc", "audio_flac_bitwriter", "audio_flac_checksum", "audio_flac_file", "audio_flac_input", "audio_flac_seek", "audio_mp4", "audio_mp4_alac", "audio_mp4_index", "audio_mp4_entries", "audio_mp4_presentation", "audio_mp4_boxes", "audio_mp4_config", "audio_mp4_tables", "audio_mp4_time", "audio_mp2", "audio_mp2_header", "audio_mp2_layer", "audio_mp2_tables", "audio_mp3", "audio_mp3_file", "audio_mp3_input", "audio_mp3_metadata", "audio_mp3_seek", "audio_mp3_bits", "audio_mp3_header", "audio_mp3_framing", "audio_mp3_spectral", "audio_mp3_synthesis", "audio_mp3_tables", "audio_pcm", "audio_source", "audio_tags", "audio_qoa", "audio_wavpack", "audio_wavpack_bits", "audio_wavpack_block", "audio_wavpack_config", "audio_wavpack_entropy", "audio_wavpack_extend", "audio_wavpack_packet", "audio_wavpack_predict", "audio_wavpack_tables", "audio_wavpack_tags", "audio_wavpack_tags_input", "audio_wavpack_input", "audio_wavpack_file", "audio_wav", "audio_wire", "opus", "opus_bands", "opus_celt", "opus_celt_encoder", "opus_energy", "opus_encoder_modes", "opus_mdct", "opus_modes", "opus_multistream", "opus_packet", "opus_pitch", "opus_playback", "opus_pvq", "opus_pvq_table", "opus_range", "opus_silk", "opus_silk_core", "opus_silk_entropy", "opus_silk_loss", "opus_silk_math", "opus_silk_noise", "opus_silk_output", "opus_silk_predict", "opus_silk_resample", "opus_silk_stereo", "opus_silk_tables", "opus_rate", "opus_vq"]],
  ["Collections & errors", ["array", "constmap", "error", "iter", "map", "ndarray", "option",
    "result", "ordmap", "ordset", "pmap", "pset", "pvec"]],
  ["Video", ["video_avc", "video_hevc", "video_hevc_pps", "video_vp9", "video_av1", "video_av1_sequence", "video_frame", "video_playback_clock", "video_ivf", "video_ivf_input", "video_nal", "video_nal_input", "video_qtrle", "video_mp4", "video_mp4_file", "video_mp4_timing"]],
  ["Traits", ["cmp", "convert", "mem", "ops"]],
  ["Numbers", ["bigint", "f32", "f64", "f64_text", "i32", "i64", "math", "num", "number_parse", "rand", "simd", "u32",
    "u64"]],
  ["Files, I/O & time", ["async", "cli", "dotenv", "envflag", "errno", "cpio", "dma", "elf", "filetime", "frames", "io", "io_buffered", "ip", "pagetable", "raw", "tcp", "tcp_conn", "user_process", "virtio", "virtio_blk", "virtio_net", "virtio_rng",
    "log", "mitigations", "path", "signal", "stream", "time", "clock", "tz", "worker"]],
  ["Networking", ["dns", "fetch", "headers", "http", "http_accesslog", "http_auth", "http_cache", "http_compress", "http_cookie", "http_cors", "http_form", "http_forwarded", "http_grpc", "http_h2", "http_h2_frame", "http_h3", "http_h3q", "http_hpack", "http_huffman", "http_multipart", "http_qpack", "http_requestid", "http_router", "http_sse", "http_static", "http_trace", "http_websocket", "metrics", "net", "platform", "publicsuffix", "quic_conn", "quic_frames", "quic_packet", "quic_params", "quic_recovery", "quic_streams", "serve", "tcp_serve",
    "tls", "tls_client", "tls_der", "tls_handshake", "tls_identity", "tls_keyschedule", "tls_keyshare", "tls_ktls", "tls_message", "tls_mozilla", "tls_pem", "tls_record", "tls_server", "tls_tls12", "tls_verify", "tls_x509", "user_net", "wasi_http"]],
  ["Testing", ["bench", "fuzz", "mock_platform", "sim", "sim_fetch", "sim_platform", "test"]],
  ["WebAssembly", ["wasm_component", "wasm_convert", "wasm_encode",
    "wasm_imports", "wasm_inst", "wasm_leb128", "wasm_memory",
    "wasm_module", "wasm_numeric", "wasm_sections"]],
  ["Interop", ["android", "android_access", "android_audio", "android_draw", "android_focus", "android_font", "android_fonts", "android_gl", "android_image", "android_keyboard", "android_motion", "android_p2p", "android_player", "android_property", "android_service", "android_session", "android_testkit", "android_text", "android_trace", "android_ui", "android_ui_app", "android_ui_painter", "android_ui_scene", "android_ui_sim", "android_video", "android_video_capabilities", "android_video_extractor", "android_video_muxer", "android_video_player", "android_video_source", "classfile", "ffi", "jni", "jni_bind"]],
];

// Reads what `fern -doc` actually emitted rather than trusting the table
// above, so a clean checkout (pages gitignored, generator not yet run)
// degrades to just the overview link instead of a sidebar full of 404s.
function stdlibSidebar() {
  const dir = fileURLToPath(new URL("./src/content/docs/stdlib", import.meta.url));
  const present = new Set(
    (existsSync(dir) ? readdirSync(dir) : [])
      .filter((f) => f.endsWith(".md") && f !== "index.md")
      .map((f) => f.slice(0, -3)),
  );

  const items = [
    { label: "Overview", link: "/stdlib/" },
    { label: "All functions", link: "/stdlib/functions/" },
  ];
  const grouped = new Set();

  for (const [label, slugs] of STDLIB_GROUPS) {
    const found = slugs.filter((s) => present.has(s));
    found.forEach((s) => grouped.add(s));
    if (found.length === 0) continue;
    items.push({ label, collapsed: true, items: stdlibItems(found) });
  }

  const ungrouped = [...present].filter((s) => !grouped.has(s)).sort();
  if (ungrouped.length > 0) {
    items.push({
      label: "Other",
      collapsed: true,
      items: stdlibItems(ungrouped),
    });
  }
  return items;
}

// The token colours, shared with the playground editor so a snippet and a
// code fence colour the same token the same way.
const fernThemes = JSON.parse(
  readFileSync(
    fileURLToPath(new URL("../web/editor/fern-themes.json", import.meta.url)),
    "utf8",
  ),
);

const fernGrammar = JSON.parse(
  readFileSync(
    fileURLToPath(
      new URL(
        "../editors/vscode/syntaxes/fern.tmLanguage.json",
        import.meta.url,
      ),
    ),
    "utf8",
  ),
);

export default defineConfig({
  site: siteUrl,
  trailingSlash: "ignore",
  integrations: [
    starlight({
      title: "Fern",
      // The site title "Fern" is next to the logo, so the image is decoration.
      logo: {
        src: "./src/assets/fern-logo.svg",
        alt: "",
      },
      description: tagline,
      // The Open Graph / Twitter card tags Starlight doesn't emit itself.
      // (The two webfonts are self-hosted — see ./src/styles/fonts.css in
      // `customCss` below.)
      head: [
        {
          tag: "meta",
          attrs: { property: "og:type", content: "website" },
        },
        {
          tag: "meta",
          attrs: { property: "og:site_name", content: "Fern" },
        },
        {
          tag: "meta",
          attrs: { property: "og:description", content: tagline },
        },
        {
          tag: "meta",
          attrs: { property: "og:image", content: ogImage },
        },
        {
          tag: "meta",
          attrs: { property: "og:image:width", content: "1200" },
        },
        {
          tag: "meta",
          attrs: { property: "og:image:height", content: "630" },
        },
        {
          tag: "meta",
          attrs: { property: "og:image:alt", content: tagline },
        },
        {
          tag: "meta",
          attrs: { name: "twitter:card", content: "summary_large_image" },
        },
        {
          tag: "meta",
          attrs: { name: "twitter:image", content: ogImage },
        },
        {
          tag: "meta",
          attrs: { name: "theme-color", content: "#143026" },
        },
      ],
      // Register Fern's own TextMate grammar (loaded above) so ```fern
      // fences get language-accurate highlighting — method receivers,
      // `match`, sized integer types, generics, f-strings, and the
      // pipe operator all colour correctly instead of being approximated
      // by TypeScript's grammar.
      expressiveCode: {
        themes: fernThemes,
        shiki: {
          langs: [fernGrammar],
        },
        // Match the site's own chrome: the same hairline radius the
        // specimen panels use, and code set in IBM Plex Mono via the
        // shared `--sl-font-mono` token rather than a second stack.
        styleOverrides: {
          borderRadius: "0.4rem",
          codeFontFamily: "var(--sl-font-mono)",
          uiFontFamily: "var(--sl-font-mono)",
          frames: {
            frameBoxShadowCssValue: "none",
          },
        },
      },
      social: [
        {
          icon: "github",
          label: "GitHub",
          href: "https://github.com/JakeChampion/lang",
        },
      ],
      sidebar: [
        { label: "Overview", link: "/" },
        { label: "Why Fern", link: "/why/" },
        { label: "Coming from Go, Rust or TypeScript", link: "/compare/" },
        // Starlight 0.39 removed the `{label, autogenerate}`
        // shorthand — groups now wrap autogenerate inside their
        // items list. Same end result; one extra layer.
        {
          label: "Tutorial",
          items: [{ autogenerate: { directory: "tutorial" } }],
        },
        {
          label: "HTTP guide",
          items: [{ autogenerate: { directory: "http" } }],
        },
        {
          label: "Android guide",
          items: [{ autogenerate: { directory: "android" } }],
        },
        { label: "Cookbook", link: "/cookbook/" },
        {
          label: "Reference",
          items: [{ autogenerate: { directory: "reference" } }],
        },
        { label: "Error codes", link: "/errors/" },
        {
          label: "Standard library",
          items: stdlibSidebar(),
        },
        { label: "Coreutils", link: "/coreutils/" },
        { label: "Benchmarks", link: "/benchmarks/" },
        { label: "Releases", link: "/releases/" },
        { label: "Contributing", link: "/contributing/" },
        {
          label: "Playground",
          link: "/playground/",
          attrs: { target: "_blank" },
        },
      ],
      // fonts.css first: the @font-face rules it generates are what
      // fern.css's `--fern-font-display` / `--fern-font-mono` stacks
      // resolve to. Both are bundled into the same stylesheet, so this is
      // one request rather than the three (two preconnects + a
      // cross-origin stylesheet) the Google Fonts link used to cost.
      customCss: ["./src/styles/fonts.css", "./src/styles/fern.css"],
      routeMiddleware: "./src/routeData.ts",
      lastUpdated: true,
      editLink: {
        baseUrl:
          "https://github.com/JakeChampion/lang/edit/main/site/",
      },
    }),
    // Starlight adds MDX itself unless the config has it. In Astro 6 the
    // MDX integration takes `gfm` from `markdown.gfm`, which is now unset,
    // so without this an .mdx page renders no tables. After Starlight, as
    // its docs require.
    // After Starlight, so its plugin runs after Expressive Code, and before
    // MDX, which takes the Markdown plugins when it starts.
    playgroundLinks(),
    mdx({ gfm: true, optimize: true }),
  ],
});
