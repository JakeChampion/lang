// A social card for each docs page except the home page, which keeps the
// card in public/og.png.
import type { APIRoute, GetStaticPaths } from "astro";
import { getCollection } from "astro:content";

import { sectionOf } from "../../lib/doc-pages.mjs";
import { renderCard } from "../../lib/og-card.mjs";

export const getStaticPaths = (async () => {
  const docs = await getCollection("docs");
  return docs
    .filter((e) => e.id !== "index" && e.id !== "")
    .map((e) => ({ params: { slug: e.id }, props: { title: e.data.title, section: sectionOf(e.id) } }));
}) satisfies GetStaticPaths;

const host = new URL(process.env.CANONICAL_SITE ?? import.meta.env.SITE).host;

export const GET: APIRoute = async ({ props }) =>
  new Response(await renderCard({ ...(props as { title: string; section: string }), host }), {
    headers: { "content-type": "image/png" },
  });
