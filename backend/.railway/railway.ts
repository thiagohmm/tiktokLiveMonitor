import { defineRailway, preserve, project, service } from "railway/iac";

// Last resort for a per-service CaC repo. Prefer one .railway file for the
// project and drop this if you later combine services into that file.
export const partial = "backend";

export default defineRailway(() => {
  const backend = service("backend", {
    healthcheck: "/api/readiness",
    healthcheckTimeout: 300,
    variables: {
      AUTH_ENABLED: preserve(),
      CORS_ALLOWED_ORIGINS: preserve(),
      DATABASE_URL: preserve(),
      DB_MAX_CONNS: preserve(),
      HOST: preserve(),
      SUPABASE_ANON_KEY: preserve(),
      SUPABASE_SERVICE_ROLE_KEY: preserve(),
      SUPABASE_URL: preserve(),
    },
  });
  return project("tiktok-live-monitor", {
    resources: [backend],
  });
});
