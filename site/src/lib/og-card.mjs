// The social card of one docs page: the page title on the site's paper
// background, the section above it, and the frond. Text becomes SVG paths
// through opentype.js and the static TTFs in src/assets/fonts/card/, so
// the card looks the same on every machine, with or without the fonts
// installed.

import { readFileSync } from "node:fs";
import { join } from "node:path";

import opentype from "opentype.js";
import sharp from "sharp";

// Resolved from the site directory, where every build runs: the build
// bundles this module, so a path relative to it would not hold.
const font = (name) => {
  const buf = readFileSync(join(process.cwd(), "src/assets/fonts/card", name));
  return opentype.parse(buf.buffer.slice(buf.byteOffset, buf.byteOffset + buf.byteLength));
};
const display = font("fraunces-144-600-wonky.ttf");
const mono = font("ibm-plex-mono-500.ttf");

const WIDTH = 1200;
const HEIGHT = 630;
const LEFT = 96;
const TEXT_WIDTH = 900;

const FROND = [
  "M6 29 C 9 22, 13 14, 18 9 C 21 6, 24 6, 25 8",
  "M7.6 25 C 4.5 24, 3 21.5, 3.2 18.8",
  "M9.6 21 C 7 20, 5.7 17.8, 6 15.4",
  "M11.8 17 C 9.6 16.2, 8.6 14.3, 9 12.2",
  "M14 13.5 C 12.2 12.9, 11.5 11.3, 12 9.6",
  "M9.4 23 C 12.5 22, 14.6 23.6, 15.6 26",
  "M11.4 19 C 14.2 17.6, 16.4 18.6, 17.8 20.8",
  "M13.6 15.4 C 16 14, 18.2 14.6, 19.6 16.4",
  "M16 12 C 18 10.8, 19.8 11.2, 21 12.6",
];

// Words split into lines no wider than `width` at `size`. A word wider
// than the line is a line of its own.
export function wrap(f, text, size, width) {
  const lines = [];
  let line = "";
  for (const word of text.split(/\s+/)) {
    const next = line ? `${line} ${word}` : word;
    if (line && f.getAdvanceWidth(next, size) > width) {
      lines.push(line);
      line = word;
    } else {
      line = next;
    }
  }
  if (line) lines.push(line);
  return lines;
}

const LABEL_Y = 214;
const TITLE_BOTTOM = 492;
const firstBaseline = (size) => LABEL_Y + 30 + size * 0.95;
const lead = (size) => size * 1.12;

// The largest size, from 92 down, at which the title fits in three lines
// with no line wider than the text area and its last line above the rule.
export function fitTitle(title) {
  for (let size = 92; size > 36; size -= 4) {
    const lines = wrap(display, title, size, TEXT_WIDTH);
    const bottom = firstBaseline(size) + (lines.length - 1) * lead(size);
    if (lines.length <= 3 && bottom <= TITLE_BOTTOM && lines.every((l) => display.getAdvanceWidth(l, size) <= TEXT_WIDTH)) {
      return { size, lines };
    }
  }
  return { size: 36, lines: wrap(display, title, 36, TEXT_WIDTH) };
}

function textPath(f, text, x, y, size, fill) {
  return `<path d="${f.getPath(text, x, y, size).toPathData(2)}" fill="${fill}"/>`;
}

export function cardSvg({ title, section, host }) {
  const { size, lines } = fitTitle(title);
  const top = firstBaseline(size);
  const titlePaths = lines.map((l, i) => textPath(display, l, LEFT, top + i * lead(size), size, "#143026")).join("");
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${WIDTH}" height="${HEIGHT}" viewBox="0 0 ${WIDTH} ${HEIGHT}">
  <rect width="${WIDTH}" height="${HEIGHT}" fill="#faf7f0"/>
  <g transform="translate(700 40) scale(17)" stroke="#2d6a4f" stroke-width="1" stroke-linecap="round" stroke-linejoin="round" fill="none" opacity="0.10">
    ${FROND.map((d) => `<path d="${d}"/>`).join("")}
  </g>
  <g transform="translate(${LEFT} 64) scale(1.6)" stroke="#2d6a4f" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" fill="none">
    ${FROND.map((d) => `<path d="${d}"/>`).join("")}
  </g>
  ${textPath(display, "Fern", LEFT + 62, 108, 46, "#143026")}
  ${textPath(mono, section.toUpperCase(), LEFT, LABEL_Y, 24, "#8a6520")}
  ${titlePaths}
  <rect x="${LEFT}" y="532" width="64" height="3" fill="#b08331"/>
  ${textPath(mono, host, LEFT, 580, 24, "#5f5442")}
</svg>`;
}

export async function renderCard(page) {
  return sharp(Buffer.from(cardSvg(page))).png({ compressionLevel: 9 }).toBuffer();
}
