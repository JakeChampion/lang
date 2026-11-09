// The Netlify Function for playground short links: see ../share.mjs.
import { getStore } from "@netlify/blobs";

import { handle } from "../share.mjs";

export default async (req) => handle(req, getStore("playground-shares"));

export const config = { path: ["/api/share", "/p/:id"] };
