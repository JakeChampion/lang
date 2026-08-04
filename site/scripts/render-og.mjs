// Renders src/assets/og-card.svg → public/og.png, the 1200×630 image
// social platforms show when a Fern link is shared.
//
// The PNG is committed rather than generated during the site build:
// crawlers don't render SVG, and the card changes about as often as the
// logo does. Re-run this after editing the card source.
//
//   node scripts/render-og.mjs
//
// libvips rasterises the SVG's text through fontconfig, so the site's two
// webfonts have to be installed locally or the card silently falls back to
// a system serif. Fetch them from Google Fonts into ~/.fonts first:
//
//   curl -A 'Mozilla/4.0' -o fonts.css \
//     'https://fonts.googleapis.com/css2?family=Fraunces:opsz,wght,SOFT,WONK@144,600,30,1&family=IBM+Plex+Mono:wght@500'
//   grep -o 'https://[^)]*\.ttf' fonts.css | xargs -n1 -I{} sh -c \
//     'curl -o ~/.fonts/$(basename {}) {}'
//   fc-cache -f
//
// The `Mozilla/4.0` user-agent is what makes Google serve TTFs; a modern
// one returns woff2, which fontconfig can't install.

import { fileURLToPath } from "node:url";

import sharp from "sharp";

const here = (p) => fileURLToPath(new URL(p, import.meta.url));

const info = await sharp(here("../src/assets/og-card.svg"), { density: 96 })
  .resize(1200, 630)
  .png({ compressionLevel: 9 })
  .toFile(here("../public/og.png"));

console.log(`wrote public/og.png — ${info.width}×${info.height}, ${info.size} B`);
