import { defineConfig } from '@playwright/test'

export default defineConfig({
  testMatch: 'journey.spec.js',
  workers: 1,
  retries: 0,
  reporter: [['list', { printSteps: true }]],
  use: {
    baseURL: 'http://127.0.0.1:4173',
    browserName: 'chromium',
    viewport: { width: 1440, height: 900 },
    trace: 'retain-on-failure',
  },
})
