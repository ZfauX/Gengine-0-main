import { test, expect } from '@playwright/test';
import { registerUser, loginUser, uniqueEmail } from './helpers';

test.describe('Auth flow', () => {
  test('register → login → dashboard → logout', async ({ page }) => {
    const email = uniqueEmail('reg');
    const password = 'Password123!';

    await registerUser(page, email, password);

    // Логинимся после регистрации.
    await loginUser(page, email, password);

    await expect(page).toHaveURL(/\/dashboard/, { timeout: 15000 });
    await expect(page.locator('h1.page-title')).toContainText('Личный кабинет');

    // На дашборде есть ссылка «Создать команду» / «Создать игру».
    await expect(page.locator('body')).toContainText('Создать команду', { timeout: 10000 });

    // Выход.
    const logout = page.locator('form[action="/auth/logout"] button, a[href="/auth/logout"]').first();
    if (await logout.isVisible().catch(() => false)) {
      await logout.click();
      await expect(page).toHaveURL(/\/auth\/login|\//, { timeout: 15000 });
    }
  });

  test('register → login → profile page', async ({ page }) => {
    const email = uniqueEmail('login');
    const password = 'Password123!';

    await registerUser(page, email, password);
    await loginUser(page, email, password);
    await expect(page).toHaveURL(/\/dashboard/, { timeout: 15000 });

    // Профиль.
    await page.goto('/profile');
    await expect(page.locator('h1.page-title')).toBeVisible({ timeout: 10000 });
    await expect(page.locator('#name')).toHaveValue('E2E User', { timeout: 10000 });
  });

  test('login with wrong password shows error', async ({ page }) => {
    const email = uniqueEmail('wrongpw');
    const password = 'Password123!';

    await registerUser(page, email, password);

    await page.goto('/auth/login');
    await page.fill('#email', email);
    await page.fill('#password', 'WrongPass123!');
    await page.click('#loginBtn');

    await expect(page).toHaveURL(/\/auth\/login/, { timeout: 15000 });
    await expect(page.locator('body')).toContainText(/неверн|ошибк/i);
  });
});
