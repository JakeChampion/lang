for (const canvas of document.querySelectorAll("[data-lab-preview]")) {
  const c = canvas.getContext("2d"),
    name = canvas.dataset.labPreview,
    w = canvas.width,
    h = canvas.height;
  c.fillStyle = "#102438";
  c.fillRect(0, 0, w, h);
  c.lineWidth = 2;
  c.font = "15px ui-monospace,monospace";
  if (name === "foundry") {
    for (let y = 0; y < 9; y++)
      for (let x = 0; x < 22; x++) {
        c.strokeStyle = "#233d50";
        c.strokeRect(x * 30, y * 30, 30, 30);
      }
    for (let row = 1; row < 8; row += 3)
      for (let x = 2; x < 20; x++) {
        c.fillStyle =
          x === 2
            ? "#e7b765"
            : x === 19
              ? "#79d2a0"
              : x === 10
                ? "#df9276"
                : "#507f8f";
        c.fillRect(x * 30 + 2, row * 30 + 2, 26, 26);
        c.fillStyle = "#152739";
        c.fillText(x === 10 ? "S" : ">", x * 30 + 9, row * 30 + 21);
        if (x % 3 === 0) {
          c.fillStyle = "#f8d37b";
          c.fillRect(x * 30 + 19, row * 30 + 5, 6, 6);
        }
      }
    c.strokeStyle = "#74dce6";
    c.beginPath();
    c.moveTo(315, 75);
    c.lineTo(315, 200);
    c.lineTo(160, 200);
    c.stroke();
  } else if (name === "patchbay") {
    for (let row = 0; row < 8; row++)
      for (let step = 0; step < 16; step++) {
        c.fillStyle = (step * 3 + row) % 7 === 0 ? "#88dcb0" : "#213c50";
        c.fillRect(18 + step * 39, 18 + row * 23, 34, 18);
      }
    c.strokeStyle = "#f2cd7f";
    c.beginPath();
    for (let x = 0; x < w; x++) {
      const y = 235 + Math.sin(x * 0.045) * 15 + Math.sin(x * 0.1) * 4;
      x ? c.lineTo(x, y) : c.moveTo(x, y);
    }
    c.stroke();
  } else if (name === "circuit") {
    const positions = [
      [85, 65],
      [85, 205],
      [255, 65],
      [255, 205],
      [425, 65],
      [425, 205],
      [575, 135],
    ];
    c.strokeStyle = "#73cfa3";
    for (const [a, b] of [
      [0, 2],
      [1, 2],
      [0, 3],
      [1, 3],
      [2, 4],
      [2, 5],
      [3, 6],
      [5, 6],
    ]) {
      c.beginPath();
      c.moveTo(...positions[a]);
      c.lineTo(...positions[b]);
      c.stroke();
    }
    positions.forEach(([x, y], i) => {
      c.fillStyle = i % 3 ? "#284b5d" : "#74cfa2";
      c.fillRect(x - 36, y - 20, 72, 40);
      c.fillStyle = i % 3 ? "#d6ebde" : "#142c36";
      c.textAlign = "center";
      c.fillText(["A", "B", "XOR", "AND", "SUM", "AND", "OR"][i], x, y + 5);
    });
  } else if (name === "fernbase") {
    c.fillStyle = "#89dcb0";
    c.fillText("BEGIN", 35, 40);
    c.fillText("INSERT → INDEX → COMMIT", 35, 75);
    c.strokeStyle = "#50758c";
    for (let i = 0; i < 6; i++) {
      c.fillStyle = i % 2 ? "#1d374b" : "#172e42";
      c.fillRect(30, 100 + i * 24, 600, 22);
      c.fillStyle = i === 2 ? "#f0cf7b" : "#b8ccd3";
      c.fillText(
        String(i).padStart(3, "0") +
          "   " +
          ["alpha", "birch", "café", "delta", "ember", "fern"][i] +
          "              committed",
        44,
        116 + i * 24,
      );
    }
  } else {
    c.fillStyle = "#213c50";
    c.fillRect(28, 22, 604, 42);
    c.fillStyle = "#a4e1bd";
    c.fillText("Search the archive…", 44, 49);
    for (let i = 0; i < 3; i++) {
      c.fillStyle = "#7fd6ac";
      c.fillText(
        ["A garden of sound", "Build a world", "Fern on the server"][i],
        34,
        99 + i * 66,
      );
      c.fillStyle = "#829ba9";
      c.fillRect(34, 110 + i * 66, 490 - i * 55, 7);
      c.fillRect(34, 125 + i * 66, 350 + i * 35, 7);
    }
  }
}
