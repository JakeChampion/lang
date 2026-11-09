// A group's modules in alphabetical order, with each module family nested
// under its parent: `opus_silk_tables` sits under `opus_silk`, which sits
// under `opus`. A family with no parent module stays flat.
export function stdlibItems(slugs) {
  const sorted = [...slugs].sort();
  const parentOf = (s) =>
    sorted.filter((p) => p !== s && s.startsWith(`${p}_`)).sort((a, b) => b.length - a.length)[0];
  const children = new Map(sorted.map((s) => [s, []]));
  const top = [];
  for (const s of sorted) {
    const p = parentOf(s);
    (p ? children.get(p) : top).push(s);
  }
  const item = (s) => {
    const link = { label: s, link: `/stdlib/${s}/` };
    const kids = children.get(s);
    if (kids.length === 0) return link;
    return { label: s, collapsed: true, items: [link, ...kids.map(item)] };
  };
  return top.map(item);
}
