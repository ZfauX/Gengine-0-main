import { test, expect } from '@playwright/test';
import { registerUser, loginUser, uniqueEmail } from './helpers';

// Вспомогательная функция: регистрация + вход.
async function registerAndLogin(page: Page, prefix: string) {
  const email = uniqueEmail(prefix);
  const password = 'Password123!';
  await registerUser(page, email, password);
  await loginUser(page, email, password);
  await expect(page).toHaveURL(/\/dashboard/, { timeout: 15000 });
  return { email, password };
}

test.describe('Content creation', () => {
  test('create team', async ({ page }) => {
    await registerAndLogin(page, 'team');

    await page.goto('/teams/new');
    await expect(page.locator('h1.page-title')).toBeVisible({ timeout: 10000 });

    const teamName = 'E2E Team ' + Date.now();
    await page.fill('#name', teamName);
    await page.click('form[action="/teams/new"] button[type="submit"]');

    // После создания — страница команды или список моих команд.
    await expect(page).toHaveURL(/\/teams/, { timeout: 15000 });
    await expect(page.locator('body')).toContainText(teamName, { timeout: 10000 });
  });

  test('create game draft', async ({ page }) => {
    await registerAndLogin(page, 'game');

    await page.goto('/games/new');
    await expect(page.locator('h1.page-title')).toBeVisible({ timeout: 10000 });

    const gameName = 'E2E Game ' + Date.now();
    await page.fill('#name', gameName);
    await page.fill('#description', 'Описание для E2E-теста');
    // Черновик по умолчанию включён (is_draft checked).
    await page.click('form[action="/games/new"] button[type="submit"]');

    await expect(page).toHaveURL(/\/games\//, { timeout: 15000 });
    await expect(page.locator('body')).toContainText(gameName, { timeout: 10000 });
  });

  test('profile page renders after registration', async ({ page }) => {
    await registerAndLogin(page, 'prof');

    await page.goto('/profile');
    await expect(page.locator('h1.page-title')).toBeVisible({ timeout: 10000 });
    // Имя пользователя отображается в поле редактирования профиля.
    await expect(page.locator('#name')).toHaveValue('E2E User', { timeout: 10000 });
  });
});
