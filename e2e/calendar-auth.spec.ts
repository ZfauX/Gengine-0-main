// e2e/calendar-auth.spec.ts
// Проверка, что /calendar показывает состояние авторизации:
//   - авторизованный видит навбар с logout/профилем и кнопку «Создать игру»
//     в JS-шаблоне дня ({{if .CurrentUserID}});
//   - аноним видит навбар «Войти» и НЕ видит кнопку «Создать игру».
import { test, expect } from '@playwright/test';

test('calendar показывает состояние авторизации', async ({ browser }) => {
  const base = process.env.E2E_BASE_URL || 'http://localhost:8081';

  // --- Анонимный контекст ---
  const anonCtx = await browser.newContext();
  const anon = await anonCtx.newPage();
  await anon.goto(base + '/calendar');
  await anon.waitForLoadState('networkidle');
  const anonBody = await anon.content();
  console.log('ANON_HAS_LOGIN_LINK=' + (anonBody.includes('/auth/login') || anonBody.includes('Войти')));
  console.log('ANON_HAS_CREATE_HREF=' + anonBody.includes('href="/games/new"'));
  // Аноним: навбар показывает «Войти», кнопка «Создать игру» отсутствует.
  expect(anonBody).toContain('/auth/login');
  expect(anonBody).not.toContain('href="/games/new"');
  await anonCtx.close();

  // --- Авторизованный контекст (admin из pod-манифеста) ---
  const authCtx = await browser.newContext();
  const page = await authCtx.newPage();
  await page.goto(base + '/auth/login');
  await page.fill('input[name="email"]', 'admin@pod.test');
  await page.fill('input[name="password"]', 'AdminPod123456!');
  await page.click('button[type="submit"]');
  await page.waitForLoadState('networkidle');
  console.log('AFTER_LOGIN_URL=' + page.url());

  await page.goto(base + '/calendar');
  await page.waitForLoadState('networkidle');
  const body = await page.content();
  console.log('AUTH_HAS_NAV=' + body.includes('<nav'));
  console.log('AUTH_HAS_DASHBOARD=' + body.includes('/dashboard'));
  console.log('AUTH_HAS_LOGOUT=' + (body.includes('/auth/logout') || body.includes('Выйти')));
  console.log('AUTH_HAS_CREATE_HREF=' + body.includes('href="/games/new"'));
  // Авторизованный: навбар показывает профиль/выход, кнопка «Создать игру»
  // рендерится в JS-шаблоне дня (CurrentUserID != 0).
  expect(body).toContain('/auth/logout');
  expect(body).toContain('href="/games/new"');
  await authCtx.close();
});
