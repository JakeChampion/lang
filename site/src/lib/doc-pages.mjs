// The docs pages in reading order, with the section each one belongs to.
// The social cards and llms.txt both use this order.

const SECTIONS = [
  ["index", "Fern"],
  ["why", "Fern"],
  ["tutorial/", "Tutorial"],
  ["http/", "HTTP guide"],
  ["cookbook", "Cookbook"],
  ["reference/", "Reference"],
  ["errors/", "Error codes"],
  ["stdlib/", "Standard library"],
  ["coreutils", "Coreutils"],
  ["releases", "Project"],
  ["contributing", "Project"],
];

const rank = (id) => {
  // A section's own index page has the section name as its id.
  const i = SECTIONS.findIndex(([p]) => (p.endsWith("/") ? id.startsWith(p) || id === p.slice(0, -1) : id === p));
  return i < 0 ? SECTIONS.length : i;
};

export const sectionOf = (id) => SECTIONS[rank(id)]?.[1] ?? "Fern";

export function readingOrder(entries) {
  return [...entries].sort((a, b) =>
    rank(a.id) - rank(b.id) ||
    (a.data.sidebar?.order ?? 1000) - (b.data.sidebar?.order ?? 1000) ||
    a.id.localeCompare(b.id));
}
