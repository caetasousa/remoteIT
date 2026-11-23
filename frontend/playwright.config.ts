import { defineConfig, devices } from '@playwright/test';

// Os testes rodam contra o ambiente do compose.e2e.yaml (make test-e2e):
// relay real, Postgres real e um agente Linux com tela sintética.
export default defineConfig({
  testDir: './e2e',
  workers: 1, // os testes compartilham o mesmo agente
  timeout: 60_000,
  reporter: [['list']],
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:18080',
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
