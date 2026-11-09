import type { APIRoute } from "astro";
import { getCollection } from "astro:content";

import { llmsIndex } from "../lib/llms.mjs";

export const GET: APIRoute = async ({ site }) => {
  const docs = await getCollection("docs");
  return new Response(llmsIndex(site?.origin ?? "", docs, docs.find((e) => e.id === "index")?.data.description ?? ""), {
    headers: { "content-type": "text/plain; charset=utf-8" },
  });
};
