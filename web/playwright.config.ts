import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  workers: 1,
  timeout: 30_000,
  use: { browserName: 'chromium', trace: 'off' },
  projects: [
    { name: 'desktop', use: { viewport: { width: 1365, height: 1000 } } },
    { name: 'mobile', use: { viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } },
  ],
});
