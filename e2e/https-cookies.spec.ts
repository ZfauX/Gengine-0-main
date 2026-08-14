// e2e/https-cookies.spec.ts
// Проверка флагов cookie (Secure/HttpOnly/SameSite) в зависимости от протокола:
//   - HTTPS (E2E_BASE_URL=https://…): ВСЕ cookie должны быть Secure+HttpOnly,
//     а JS-куки (tz_offset, lang) — Secure (не HttpOnly).
//   - HTTP (CI/localhost): Secure-флаг должен быть ВЫКЛЮЧЕН. Если сервер
//     форсирует Secure на HTTP (FORCE_SECURE_COOKIE/TRUSTED_PROXIES), браузер
//     не сохраняет cookie → авторизация ломается (известный CI-кейс PASS-15).
import { test, expect } from '@playwright/test';

test('Cookie flags: Secure+HttpOnly согласованы с протоколом', async ({ page, context }) => {
  const base = process.env.E2E_BASE_URL || 'http://localhost:8081';
  const isHttps = base.startsWith('https://');

  // Открываем login (ставит CSRF cookie)
  await page.goto(base + '/auth/login');
  await page.waitForLoadState('networkidle');

  // Регистрируем нового пользователя (E2E-паттерн: уникальный email)
  await page.goto(base + '/auth/register');
  const email = `sec_${Date.now()}_${Math.floor(Math.random() * 1000)}@e2e.test`;
  await page.fill('input[name="name"]', 'SecTest');
  await page.fill('input[name="email"]', email);
  await page.fill('input[name="password"]', 'SecurePass123!');
  await page.check('input[name="accept_terms"]');
  await page.click('button[type="submit"]');
  await page.waitForURL(/\/auth\/login|\/dashboard/, { timeout: 10000 });

  // Логинимся
  if (page.url().includes('/auth/login')) {
    await page.fill('input[name="email"]', email);
    await page.fill('input[name="password"]', 'SecurePass123!');
    await page.click('button[type="submit"]');
  }
  await page.waitForURL(/\/dashboard/, { timeout: 10000 });

  // Cookie после авторизации
  const cookies = await context.cookies(base);
  const flags = cookies.map(c => ({
    name: c.name,
    secure: c.secure,
    httpOnly: c.httpOnly,
    sameSite: c.sameSite,
  }));
  console.log('COOKIES_AFTER_LOGIN=' + JSON.stringify(flags, null, 2));

  // JS-куки (tz_offset, lang) ставятся скриптом и не могут быть HttpOnly.
  const jsCookies = new Set(['tz_offset', 'lang']);

  for (const c of cookies) {
    if (isHttps) {
      // На HTTPS каждая cookie должна ходить только по TLS.
      expect(c.secure, `cookie ${c.name} должен быть Secure на HTTPS`).toBe(true);
    } else {
      // На HTTP Secure-флаг быть не должен — иначе браузер не сохранит cookie
      // и сессия/CSRF сломаются (FORCE_SECURE_COOKIE=true ломает HTTP-формы).
      expect(c.secure, `cookie ${c.name} НЕ должен быть Secure на HTTP`).toBe(false);
    }
    if (!jsCookies.has(c.name)) {
      expect(c.httpOnly, `cookie ${c.name} должен быть HttpOnly`).toBe(true);
    }
  }
});
