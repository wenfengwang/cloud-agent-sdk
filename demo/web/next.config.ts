import type { NextConfig } from "next";

// The production build is a static export served by the demo backend
// (--web-dir). In development, `next dev` proxies /api to the backend; a
// static export cannot have rewrites, so they exist only in development.
const backend = process.env.DEMO_BACKEND ?? "http://127.0.0.1:8787";

const common: NextConfig = {
  turbopack: {
    rules: {
      "*.css": {
        loaders: ["@tailwindcss/turbopack"],
        as: "*.css",
      },
    },
  },
};

const nextConfig: NextConfig =
  process.env.NODE_ENV === "development"
    ? {
        ...common,
        async rewrites() {
          return [{ source: "/api/:path*", destination: `${backend}/api/:path*` }];
        },
      }
    : { ...common, output: "export", trailingSlash: true };

export default nextConfig;
