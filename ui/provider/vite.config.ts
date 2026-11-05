import { federation } from '@module-federation/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// Billing's staff-portal plugin: a Module Federation remote loaded by
// staff-portal's plugin host at runtime (app/modules/plugins/ there).
//
// One extension today: a `portal.page/service` page at path "" — the
// reserved "replace the built-in Overview" convention — so the fleet-wide
// billing overview is the landing page of
// /admin/service-catalog/billing-miloapis-com. staff-portal keeps the
// built-in page (pricing, meters, quota, conditions) under its "Service
// details" nav item whenever a plugin overrides Overview.
//
// Assets are fetched server-side by staff-portal's asset proxy and served
// under /api/plugins/<slug>/…, so the browser never contacts this origin.
// `shared` mirrors staff-portal's `federation-host.ts`: those are the only
// modules (and `@datum-cloud/datum-ui` subpaths) the host provides. Every
// entry is `requiredVersion: false` because the host's copy always wins, and
// a strict check fails the container load on any patch drift.
export default defineConfig({
  server: { port: 5299, strictPort: true, cors: true },
  preview: { port: 5299, strictPort: true, cors: true },
  build: {
    target: 'esnext',
    // Minified: the browser parses every byte of a plugin chunk on first
    // load. Sourcemaps keep stack traces readable.
    sourcemap: true,
  },
  plugins: [
    react(),
    federation({
      // Must equal the manifest `name`: the host keys the remote by it.
      name: 'billing.staff-portal.miloapis.com',
      filename: 'remoteEntry.js',
      manifest: true,
      // Keys map 1:1 to the manifest's `exposedModules` / $codeRefs.
      exposes: {
        './FleetOverview': './src/pages/fleet-overview.tsx',
      },
      shared: {
        react: { singleton: true, requiredVersion: false },
        'react/jsx-runtime': { singleton: true, requiredVersion: false },
        'react-dom': { singleton: true, requiredVersion: false },
        // Only the standalone preview (main.tsx) imports this, but MF still
        // finds it in the build graph; left unconfigured it is shared with a
        // strict version check that fails on host/plugin patch drift.
        'react-dom/client': { singleton: true, requiredVersion: false },
        'react-router': { singleton: true, requiredVersion: false },
        '@tanstack/react-query': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/badge': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/button': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/card': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/icons': { singleton: true, requiredVersion: false },
        '@datum-cloud/datum-ui/skeleton': { singleton: true, requiredVersion: false },
      },
    }),
  ],
});
