import { defineRouteMiddleware } from "@astrojs/starlight/route-data";

// Starlight writes a hero action's link into the href as it is, without the
// site base. The hero links in frontmatter are therefore site paths such as
// `/tutorial/install/`, and this adds the base, the same as for sidebar links.
export const onRequest = defineRouteMiddleware((context) => {
  const hero = context.locals.starlightRoute.entry.data.hero;
  if (!hero?.actions) return;
  const base = import.meta.env.BASE_URL.replace(/\/$/, "");
  hero.actions = hero.actions.map((action) =>
    action.link.startsWith("/") && !action.link.startsWith("//")
      ? { ...action, link: `${base}${action.link}` }
      : action,
  );
});
