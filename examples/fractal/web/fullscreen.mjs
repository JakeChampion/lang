// Share the fullscreen controls between the standalone fractal and the showcase.
export function mountFullscreen(
  target,
  { container, onchange = () => {} } = {},
) {
  if (!document.getElementById("fullscreen-controls-style")) {
    const style = document.createElement("style");
    style.id = "fullscreen-controls-style";
    style.textContent = `
      .fullscreen-bar{grid-column:1/-1;display:flex;justify-content:flex-end;position:relative;z-index:5}
      .fullscreen-toggle{font:500 12px var(--font-mono,ui-monospace,monospace);min-height:40px;padding:8px 16px;border:1px solid var(--gray-5,#384a3f);border-radius:999rem;background:var(--bg-raised,#131e18);color:var(--white,#f2f7f3);cursor:pointer;transition:border-color .18s}
      .fullscreen-toggle:hover{border-color:var(--accent,#3f9169)}
      .fullscreen-toggle:focus-visible{outline:2px solid var(--fern-spore,#b08331);outline-offset:2px}
      .fullscreen-fallback{position:fixed!important;inset:0!important;z-index:10000!important;width:100%!important;height:100dvh!important;margin:0!important;box-sizing:border-box}
      .is-fullscreen{overflow:auto;background:var(--bg,#101a15);padding:12px;max-width:none!important;border-radius:0!important}
      body.is-fullscreen{padding:0;background:var(--bg,#101a15)}
    `;
    document.head.append(style);
  }
  const button = document.createElement("button");
  button.type = "button";
  button.className = "fullscreen-toggle";
  button.textContent = "Fullscreen ⛶";
  button.setAttribute("aria-pressed", "false");
  if (container) container.append(button);
  else {
    const bar = document.createElement("div");
    bar.className = "fullscreen-bar";
    bar.append(button);
    target.prepend(bar);
  }
  let expanded = false,
    fallback = false,
    previousOverflow = "",
    returnFocus;
  const active = () =>
    document.fullscreenElement || document.webkitFullscreenElement;
  function update(value) {
    expanded = value;
    target.classList.toggle("is-fullscreen", value);
    button.textContent = value ? "Exit fullscreen ⤡" : "Fullscreen ⛶";
    button.setAttribute("aria-pressed", String(value));
    requestAnimationFrame(() => {
      dispatchEvent(new Event("resize"));
      onchange();
    });
  }
  function leaveFallback() {
    fallback = false;
    target.classList.remove("fullscreen-fallback");
    document.body.style.overflow = previousOverflow;
    update(false);
    returnFocus?.focus({ preventScroll: true });
  }
  button.onclick = async () => {
    if (fallback) {
      leaveFallback();
      return;
    }
    if (active() === target) {
      const exit = document.exitFullscreen || document.webkitExitFullscreen;
      await exit?.call(document);
      return;
    }
    returnFocus = document.activeElement;
    const request = target.requestFullscreen || target.webkitRequestFullscreen;
    if (request && document.fullscreenEnabled !== false) {
      try {
        await request.call(target);
        update(true);
        return;
      } catch {
        /* Use an expanded view if the browser refuses fullscreen. */
      }
    }
    previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    fallback = true;
    target.classList.add("fullscreen-fallback");
    update(true);
    button.focus({ preventScroll: true });
  };
  const changed = () => {
    if (!fallback) update(active() === target);
  };
  document.addEventListener("fullscreenchange", changed);
  document.addEventListener("webkitfullscreenchange", changed);
  document.addEventListener("keydown", (event) => {
    if (!fallback) return;
    if (event.key === "Escape") {
      event.preventDefault();
      leaveFallback();
    }
    if (event.key === "Tab") {
      const candidates = [
        ...target.querySelectorAll(
          'button,a[href],input,select,textarea,[tabindex="0"]',
        ),
      ].filter((e) => !e.disabled && e.getClientRects().length);
      const at = candidates.indexOf(document.activeElement),
        next = event.shiftKey ? at - 1 : at + 1;
      if (at < 0 || next < 0 || next >= candidates.length) {
        event.preventDefault();
        candidates[event.shiftKey ? candidates.length - 1 : 0]?.focus();
      }
    }
  });
  return button;
}
