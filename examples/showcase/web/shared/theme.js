// Applies the visitor's color theme before the first paint, and adds the
// theme control to the navigation. Without a stored choice, or without
// JavaScript, the system setting decides.
(() => {
  const key = "fern-theme";
  const order = ["auto", "light", "dark"];
  const root = document.documentElement;
  let choice = "auto";
  try {
    choice = order.includes(localStorage.getItem(key))
      ? localStorage.getItem(key)
      : "auto";
  } catch {}
  const apply = () => {
    if (choice === "auto") delete root.dataset.theme;
    else root.dataset.theme = choice;
  };
  apply();

  const label = (button) => {
    const glyph = document.createElement("span"),
      name = document.createElement("span");
    glyph.textContent = "◐";
    glyph.setAttribute("aria-hidden", "true");
    name.className = "theme-name";
    name.textContent = choice[0].toUpperCase() + choice.slice(1);
    button.replaceChildren(glyph, " ", name);
    button.setAttribute(
      "aria-label",
      `Color theme: ${choice}. Select to change the theme.`,
    );
  };
  function mount(nav) {
    if (!nav || nav.querySelector(".theme-toggle")) return;
    const button = document.createElement("button");
    button.type = "button";
    button.className = "theme-toggle";
    label(button);
    button.addEventListener("click", () => {
      choice = order[(order.indexOf(choice) + 1) % order.length];
      try {
        if (choice === "auto") localStorage.removeItem(key);
        else localStorage.setItem(key, choice);
      } catch {}
      apply();
      label(button);
      dispatchEvent(new Event("fern-theme"));
    });
    nav.append(button);
  }
  window.fernTheme = { mount };
  const mountAll = () =>
    document.querySelectorAll(".site-nav nav, .app-nav nav").forEach(mount);
  if (document.readyState === "loading")
    document.addEventListener("DOMContentLoaded", mountAll);
  else mountAll();
})();
