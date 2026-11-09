import type { APIRoute } from "astro";
import { getCollection } from "astro:content";

import { llmsStdlib } from "../lib/llms.mjs";

export const GET: APIRoute = async ({ site }) => {
  const docs = await getCollection("docs");
  return new Response(llmsStdlib(site?.origin ?? "", docs), {
    headers: { "content-type": "text/plain; charset=utf-8" },
  });
};
