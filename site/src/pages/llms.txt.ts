import type { APIRoute } from "astro";
import { getCollection } from "astro:content";

import { llmsIndex } from "../lib/llms.mjs";

export const GET: APIRoute = async ({ site }) => {
  const origin = (process.env.CANONICAL_SITE ?? site?.origin ?? "").replace(/\/$/, "");
  const base = process.env.CANONICAL_SITE ? "" : import.meta.env.BASE_URL.replace(/\/$/, "");
  const docs = await getCollection("docs");
  return new Response(llmsIndex(origin, base, docs, docs.find((e) => e.id === "index")?.data.description ?? ""), {
    headers: { "content-type": "text/plain; charset=utf-8" },
  });
};
