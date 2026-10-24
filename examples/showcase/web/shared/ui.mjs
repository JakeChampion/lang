export { mountFullscreen } from "/fractal/fullscreen.mjs";
export const $ = (id) => document.getElementById(id);
export function toast(message) {
  let element = document.querySelector(".toast");
  if (!element) {
    element = document.createElement("div");
    element.className = "toast";
    element.setAttribute("role", "status");
    document.body.append(element);
  }
  element.textContent = message;
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => element.remove(), 3500);
}
export function status(id, text, live = false, error = false) {
  const element = $(id);
  element.textContent = text;
  element.classList.toggle("live", live);
  element.classList.toggle("error", error);
}
export function bytes(value) {
  if (!Number.isFinite(value)) return "—";
  if (value >= 1073741824) return `${(value / 1073741824).toFixed(2)} GiB`;
  if (value >= 1048576) return `${(value / 1048576).toFixed(1)} MiB`;
  if (value >= 1024) return `${(value / 1024).toFixed(1)} KiB`;
  return `${value} B`;
}
export async function json(url, options) {
  const response = await fetch(url, options);
  if (!response.ok) {
    let message = `Request failed (${response.status})`;
    try {
      const body = await response.json();
      message = body.detail || body.title || message;
    } catch {}
    throw new Error(message);
  }
  return response.json();
}
export async function copy(text) {
  try {
    await navigator.clipboard.writeText(text);
    toast("Link copied.");
  } catch {
    toast("Copy the address from your browser to share this page.");
  }
}
export function fitCanvas(canvas) {
  const ratio = Math.min(2, devicePixelRatio || 1),
    rect = canvas.getBoundingClientRect();
  canvas.width = Math.max(1, Math.round(rect.width * ratio));
  canvas.height = Math.max(1, Math.round(rect.height * ratio));
  return canvas.getContext("2d");
}
