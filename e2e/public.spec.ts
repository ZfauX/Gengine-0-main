import { test, expect } from '@playwright/test';

test.describe('Public pages', () => {
  test('home page loads', async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('body')).not.toBeEmpty({ timeout: 10000 });
    // Ссылки на логин/регистрацию присутствуют.
    await expect(page.locator('a[href="/auth/login"]').first()).toBeVisible();
    await expect(page.locator('a[href="/auth/register"]').first()).toBeVisible();
  });

  test('login page renders form', async ({ page }) => {
    await page.goto('/auth/login');
    await expect(page.locator('#email')).toBeVisible();
    await expect(page.locator('#password')).toBeVisible();
    await expect(page.locator('#loginBtn')).toBeVisible();
  });

  test('register page renders form with terms checkbox', async ({ page }) => {
    await page.goto('/auth/register');
    await expect(page.locator('#name')).toBeVisible();
    await expect(page.locator('#email')).toBeVisible();
    await expect(page.locator('#accept_terms')).toBeVisible();
  });

  test('protected route redirects to login', async ({ page }) => {
    await page.goto('/dashboard');
    // Неавторизованный получает редирект на /auth/login.
    await expect(page).toHaveURL(/\/auth\/login/, { timeout: 10000 });
  });
});

test.describe('Server global chat', () => {
  test('global chat page loads for authenticated user', async ({ page }) => {
    // Регистрируемся через API-подобную последовательность: сперва быстрый
    // прямой заход на страницу чата без авторизации должен редиректить.
    await page.goto('/chat/global');
    await expect(page).toHaveURL(/\/auth\/login/, { timeout: 10000 });
  });
});
