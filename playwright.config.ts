import { defineConfig, devices } from '@playwright/test';

// baseURL из окружения (E2E_BASE_URL) — позволяет гонять тесты против
// поднятого контейнера/compose (:8080) или локального dev-сервера (:8081).
const baseURL = process.env.E2E_BASE_URL || 'http://localhost:8081';

export default defineConfig({
  testDir: './e2e',
  timeout: 30000,
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    // Service Worker кэширует HTML-страницы → подставляет устаревший CSRF-токен.
    // Блокируем SW, чтобы каждая страница грузилась свежей.
    serviceWorkers: 'block',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
});
