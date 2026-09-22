import { defineConfig } from '@playwright/test';

// The suite starts burp (dev mode, sqlite) and the scripted GitHub in
// e2e/fakegh. Both binaries must be built first:
//   go build -o burp ./cmd/burp && go build -o e2e/fakegh/fakegh ./e2e/fakegh
// Set CHROMIUM_PATH to use a pre-installed browser instead of Playwright's.
const launchOptions = process.env.CHROMIUM_PATH
  ? { executablePath: process.env.CHROMIUM_PATH, args: ['--no-sandbox'] }
  : {};

export default defineConfig({
  testDir: '.',
  testMatch: /.*\.spec\.mjs/,
  timeout: 60_000,
  workers: 1,
  fullyParallel: false,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: 'http://localhost:8080',
    launchOptions,
    trace: 'retain-on-failure',
  },
  webServer: [
    {
      command: './fakegh/fakegh :9999',
      url: 'http://localhost:9999/state',
      reuseExistingServer: false,
      timeout: 30_000,
    },
    {
      command: 'rm -f e2e.db e2e.db-shm e2e.db-wal && ../burp serve',
      url: 'http://localhost:8080/healthz',
      reuseExistingServer: false,
      timeout: 30_000,
      env: {
        BURP_DEV_MODE: 'true',
        BURP_BASE_URL: 'http://localhost:8080',
        BURP_GITHUB_CLIENT_ID: 'id',
        BURP_GITHUB_CLIENT_SECRET: 'sec',
        BURP_GITHUB_APP_SLUG: 'burp',
        BURP_GITHUB_URL: 'http://localhost:9999',
        BURP_GITHUB_API_URL: 'http://localhost:9999/api/v3',
        BURP_DATABASE_DSN: 'e2e.db',
        BURP_LOG_LEVEL: 'debug',
      },
    },
  ],
});
