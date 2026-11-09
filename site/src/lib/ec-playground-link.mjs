// "Open in playground" under each fern code block that is a whole program,
// with the program in the link, so a reader can run any example on the
// site. A rehype plugin that runs after Expressive Code: it finds each
// rendered fern block and reads the code from its copy button, which
// holds the lines joined by U+007F.

// URL-safe base64 with no padding, the encoding of the playground's hash
// (encodeHash in web/index.html).
export function encodeHash(text) {
  return Buffer.from(text, "utf8").toString("base64url");
}

// A block is a whole program when it defines main. A fragment, or an
// HTTP handler, which the playground runs in another mode, gets no link.
export function isProgram(code) {
  return /\bfunction main\s*\(/.test(code);
}

const classes = (node) => [].concat(node.properties?.className ?? []);

function find(node, test) {
  if (test(node)) return node;
  for (const child of node.children ?? []) {
    const hit = find(child, test);
    if (hit) return hit;
  }
  return null;
}

function link(code, base) {
  return {
    type: "element",
    tagName: "div",
    properties: { className: ["fern-run-link"] },
    children: [{
      type: "element",
      tagName: "a",
      properties: { href: `${base}/playground/#src=${encodeHash(code)}`, target: "_blank", rel: "noopener" },
      children: [{ type: "text", value: "Open in playground →" }],
    }],
  };
}

export function rehypePlaygroundLinks({ base = "" } = {}) {
  return (tree) => {
    const visit = (node) => {
      const kids = node.children ?? [];
      for (let i = 0; i < kids.length; i++) {
        const child = kids[i];
        if (child.type === "element" && classes(child).includes("expressive-code")) {
          const pre = find(child, (n) => n.tagName === "pre" && n.properties?.dataLanguage === "fern");
          const copy = find(child, (n) => n.tagName === "button" && n.properties?.dataCode !== undefined);
          const code = copy ? String(copy.properties.dataCode).replace(/\u007f/g, "\n") : "";
          if (pre && isProgram(code)) {
            kids.splice(i + 1, 0, link(code, base));
            i++;
          }
          continue;
        }
        visit(child);
      }
    };
    visit(tree);
  };
}

// An Astro integration that adds the plugin after Starlight's Expressive
// Code step. It must come after starlight() in the integrations list.
export function playgroundLinks() {
  return {
    name: "fern-playground-links",
    hooks: {
      "astro:config:setup": ({ updateConfig }) => {
        updateConfig({ markdown: { rehypePlugins: [rehypePlaygroundLinks] } });
      },
    },
  };
}
