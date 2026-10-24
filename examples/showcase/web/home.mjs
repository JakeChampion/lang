import { $, bytes, json } from "/assets/ui.mjs";

for (const button of document.querySelectorAll("[data-filter]")) {
  button.addEventListener("click", () => {
    let count = 0;
    for (const card of document.querySelectorAll(".example")) {
      card.hidden =
        button.dataset.filter !== "all" &&
        !card.dataset.categories.split(" ").includes(button.dataset.filter);
      if (!card.hidden) count++;
    }
    for (const tab of document.querySelectorAll("[data-filter]"))
      tab.setAttribute("aria-pressed", String(tab === button));
    $("filter-result").textContent = `${count} examples shown.`;
  });
}
async function load() {
  const tasks = [
    json("/gather/status").then((value) => {
      $("home-gather-count").textContent =
        `${value.players} / ${value.capacity}`;
    }),
    json("/api/meta").then((meta) => {
      $("binary-size").textContent = meta.binary_bytes
        ? bytes(meta.binary_bytes)
        : "Native";
      $("instance").textContent = meta.instance;
    }),
    json("/pulse/snapshot").then((snapshot) => {
      $("home-rss").textContent = bytes(snapshot.memory.rss);
    }),
    json("/mosaic/state?room=commons").then((board) => {
      const context = $("mosaic-preview").getContext("2d");
      board.pixels.forEach((pixel, i) => {
        context.fillStyle = `#${board.palette[pixel].toString(16).padStart(6, "0")}`;
        context.fillRect(i % 64, Math.floor(i / 64), 1, 1);
      });
    }),
    json("/fractal/api/config").then((config) => {
      document.querySelectorAll("[data-fractal]").forEach((image, i) => {
        image.src = `/fractal/tile/${config.render_version}/14/${7193 + (i % 2)}/${7651 + Math.floor(i / 2)}`;
      });
    }),
  ];
  await Promise.allSettled(tasks);
}
load();
