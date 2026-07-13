import { fileURLToPath, URL } from 'node:url';

import react from '@vitejs/plugin-react';
import { defineConfig, loadEnv } from 'vite';

// Dev-only config. In production the SPA is served by nginx, which also
// reverse-proxies /v1 to the target :9080 (see deploy/nginx.conf.template).
// The config API is unauthenticated — no token is injected on either side.
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');
  // Where a guardrails / extproc-guardrails config API (:9080) is reachable in dev.
  const target = env.GUARDRAILS_API_URL || 'http://localhost:9080';

  return {
    plugins: [react()],
    resolve: {
      alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
    },
    server: {
      port: 5173,
      proxy: {
        '/v1': {
          target,
          changeOrigin: true,
        },
      },
    },
  };
});
