// Draws the homepage previews from the live commons projects. A preview
// that cannot reach the server draws a sample state instead.
const plate = "#0c1510",
  line = "#1f3027",
  text = "#e0eae3",
  muted = "#8ba394",
  frond = "#74c69d",
  mint = "#9ddcbc",
  spore = "#d9b169";
const foundryColors = [
  plate,
  spore,
  "#6f9486",
  "#6f9486",
  "#6f9486",
  "#6f9486",
  frond,
  "#d0876a",
  "#8fc0b8",
  "#b3a6c2",
  "#7fb7c9",
  "#c9b27a",
];
const foundryGlyphs = ["", "M", ">", "v", "<", "^", "D", "S", "G", "=", "+", "R"];
const gateLabels = ["LATCH", "WIRE", "NOT", "AND", "OR", "XOR", "NAND", "NOR", "OFF", "ON"];

function sample(name) {
  if (name === "foundry") {
    const cells = new Array(512).fill(0);
    for (let x = 2; x < 30; x++) cells[2 * 32 + x] = cells[7 * 32 + x] = 2;
    cells[2 * 32 + 2] = cells[7 * 32 + 2] = 1;
    cells[2 * 32 + 29] = cells[7 * 32 + 29] = 6;
    cells[7 * 32 + 15] = 7;
    return { revision: 0, width: 32, cells };
  }
  if (name === "patchbay") {
    const cells = new Array(128).fill(0);
    for (let step = 0; step < 16; step++) cells[step * 8 + ((step % 4) * 2)] = 1;
    return { revision: 0, width: 8, cells };
  }
  if (name === "circuit")
    return { revision: 0, width: 3, cells: [9, 0, 0, 8, 0, 0, 5, 0, 1, 3, 0, 1] };
  if (name === "fernbase")
    return {
      revision: 0,
      total: 3,
      rows: ["alpha", "birch", "fern"].map((key) => ({ key, value: "sample" })),
    };
  return { revision: 0, documents: 0, terms: 0, words: 0 };
}

const draw = {
  foundry(c, w, h, data) {
    const rows = Math.ceil(data.cells.length / data.width),
      size = Math.min(w / data.width, h / rows),
      ox = (w - size * data.width) / 2,
      oy = (h - size * rows) / 2;
    c.font = `${Math.round(size * 0.6)}px "IBM Plex Mono", monospace`;
    c.textAlign = "center";
    data.cells.forEach((kind, i) => {
      const x = ox + (i % data.width) * size,
        y = oy + Math.floor(i / data.width) * size;
      c.strokeStyle = line;
      c.strokeRect(x, y, size, size);
      if (kind > 0 && kind < foundryColors.length) {
        c.fillStyle = foundryColors[kind];
        c.fillRect(x + 2, y + 2, size - 4, size - 4);
        c.fillStyle = plate;
        c.fillText(foundryGlyphs[kind], x + size / 2, y + size * 0.72);
      }
    });
  },
  patchbay(c, w, h, data) {
    const cellW = (w - 36) / 16,
      cellH = 20;
    for (let step = 0; step < 16; step++)
      for (let row = 0; row < 8; row++) {
        c.fillStyle = data.cells[step * 8 + row] > 0 ? frond : line;
        c.fillRect(18 + step * cellW, 18 + row * (cellH + 3), cellW - 5, cellH);
      }
    c.strokeStyle = spore;
    c.beginPath();
    for (let x = 0; x < w; x++) {
      const y = 235 + Math.sin(x * 0.045) * 15 + Math.sin(x * 0.1) * 4;
      x ? c.lineTo(x, y) : c.moveTo(x, y);
    }
    c.stroke();
  },
  circuit(c, w, h, data) {
    const count = Math.min(16, Math.floor(data.cells.length / 3)),
      at = (i) => [90 + (i % 4) * 160, 45 + Math.floor(i / 4) * 62];
    c.strokeStyle = "#5f9c7e";
    for (let i = 0; i < count; i++)
      for (const port of [1, 2]) {
        const source = data.cells[i * 3 + port];
        if (source === i || source >= count) continue;
        c.beginPath();
        c.moveTo(...at(source));
        c.lineTo(...at(i));
        c.stroke();
      }
    c.textAlign = "center";
    for (let i = 0; i < count; i++) {
      const [x, y] = at(i),
        kind = data.cells[i * 3];
      c.fillStyle = kind === 9 ? frond : "#24332b";
      c.fillRect(x - 40, y - 18, 80, 36);
      c.fillStyle = kind === 9 ? plate : text;
      c.fillText(gateLabels[kind] ?? "?", x, y + 5);
    }
  },
  fernbase(c, w, h, data) {
    c.fillStyle = mint;
    c.fillText("SELECT * FROM entries;", 35, 40);
    c.fillStyle = muted;
    c.fillText(`${data.total} rows · revision ${data.revision}`, 35, 70);
    data.rows.slice(0, 6).forEach((row, i) => {
      c.fillStyle = i % 2 ? "#15211b" : "#111c16";
      c.fillRect(30, 90 + i * 26, 600, 24);
      c.fillStyle = i === 0 ? spore : "#b9c8be";
      c.fillText(
        `${String(i).padStart(3, "0")}   ${row.key.slice(0, 18).padEnd(20)}${row.value.slice(0, 26)}`,
        44,
        107 + i * 26,
      );
    });
  },
  lantern(c, w, h, data) {
    c.fillStyle = line;
    c.fillRect(28, 22, 604, 42);
    c.fillStyle = muted;
    c.fillText("Search the archive…", 44, 49);
    const facts = [
      ["documents", data.documents, 4096],
      ["distinct terms", data.terms, 65536],
      ["indexed words", data.words, 262144],
    ];
    facts.forEach(([label, value, limit], i) => {
      const y = 104 + i * 58;
      c.fillStyle = mint;
      c.fillText(`${value.toLocaleString()} ${label}`, 34, y);
      c.fillStyle = "#24332b";
      c.fillRect(34, y + 12, 590, 7);
      c.fillStyle = frond;
      c.fillRect(34, y + 12, Math.max(3, (590 * value) / limit), 7);
    });
  },
};

const sources = {
  foundry: "/foundry/preview",
  patchbay: "/patchbay/preview",
  circuit: "/circuit/preview",
  fernbase: "/fernbase/state?room=commons",
  lantern: "/lantern/state?room=commons",
};

for (const canvas of document.querySelectorAll("[data-lab-preview]")) {
  const c = canvas.getContext("2d"),
    name = canvas.dataset.labPreview,
    render = (data) => {
      c.fillStyle = plate;
      c.fillRect(0, 0, canvas.width, canvas.height);
      c.lineWidth = 2;
      c.textAlign = "left";
      c.font = '15px "IBM Plex Mono", ui-monospace, monospace';
      draw[name](c, canvas.width, canvas.height, data);
    };
  render(sample(name));
  fetch(sources[name])
    .then((response) => (response.ok ? response.json() : Promise.reject()))
    .then(render)
    .catch(() => {});
}
