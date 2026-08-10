import { Page, expect } from '@playwright/test';

// Уникальный email на основе timestamp, чтобы тесты были идемпотентны.
export function uniqueEmail(prefix = 'user'): string {
  return `${prefix}_${Date.now()}_${Math.floor(Math.random() * 1000)}@e2e.test`;
}

// Регистрация нового пользователя через UI (включая чекбокс соглашения).
// После успешной регистрации сервер редиректит на /auth/login (email-верификация),
// поэтому для попадания в защищённую зону нужен loginUser.
export async function registerUser(page: Page, email: string, password = 'Password123!', name = 'E2E User') {
  await page.goto('/auth/register');
  await expect(page).toHaveTitle(/Регистрация|Register|Gengine/i, { timeout: 10000 });
  await page.fill('#name', name);
  await page.fill('#email', email);
  await page.fill('#password', password);
  // Чекбокс пользовательского соглашения.
  const terms = page.locator('#accept_terms');
  if (await terms.isVisible().catch(() => false)) {
    await terms.check();
  }
  await page.click('#registerBtn');
  // После регистрации — редирект на страницу входа.
  await expect(page).toHaveURL(/\/auth\/login/, { timeout: 15000 });
}

// Логин через UI.
export async function loginUser(page: Page, email: string, password = 'Password123!') {
  await page.goto('/auth/login');
  await page.fill('#email', email);
  await page.fill('#password', password);
  await page.click('#loginBtn');
}

// Получение CSRF-токена из любой HTML-страницы и выполнение POST-запроса
// через page.request (удобно для операций с формами).
export async function csrfToken(page: Page, url: string): Promise<string> {
  const resp = await page.request.get(url);
  const html = await resp.text();
  const m = html.match(/name="_csrf" value="([^"]+)"/);
  if (!m) throw new Error(`CSRF token not found in ${url}`);
  return m[1];
}
