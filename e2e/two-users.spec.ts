import { test, expect, Page } from '@playwright/test';
import { registerUser, loginUser, uniqueEmail } from './helpers';

// T-4 (pass 47): сценарии с двумя пользователями.
// Регистрируем двух пользователей, user1 находит user2 через публичный поиск,
// открывает его профиль и переходит в личный чат.

async function findUserId(page: Page, name: string): Promise<number> {
  // Публичный API поиска: /api/users/search?q=<name>.
  const resp = await page.request.get(`/api/users/search?q=${encodeURIComponent(name)}`);
  expect(resp.ok()).toBeTruthy();
  const data = await resp.json();
  const users = data.users || [];
  expect(users.length).toBeGreaterThan(0);
  return users[0].id as number;
}

test.describe('Two users', () => {
  test('user1 opens user2 profile and starts personal chat', async ({ browser }) => {
    const ctx1 = await browser.newContext();
    const ctx2 = await browser.newContext();
    const user1 = await ctx1.newPage();
    const user2 = await ctx2.newPage();

    const email1 = uniqueEmail('u1');
    const email2 = uniqueEmail('u2');
    const password = 'Password123!';
    const name2 = 'E2E User2';

    await registerUser(user1, email1, password, 'E2E User1');
    await registerUser(user2, email2, password, name2);
    await loginUser(user1, email1, password);
    await expect(user1).toHaveURL(/\/dashboard/, { timeout: 15000 });
    await loginUser(user2, email2, password);
    await expect(user2).toHaveURL(/\/dashboard/, { timeout: 15000 });

    // user1 ищет user2 через публичный API.
    const id2 = await findUserId(user1, name2);
    expect(id2).toBeGreaterThan(0);

    // user1 открывает публичный профиль user2.
    await user1.goto(`/users/${id2}`);
    await expect(user1.locator('body')).toContainText(name2, { timeout: 10000 });

    // Кнопка «Написать» (личный чат) видна на чужом профиле.
    const msgBtn = user1.locator('a[href^="/chat/personal/"]').first();
    await expect(msgBtn).toBeVisible({ timeout: 10000 });

    // Переходим в личный чат.
    await msgBtn.click();
    await expect(user1).toHaveURL(/\/chat\/personal\/\d+/, { timeout: 10000 });
    await expect(user1.locator('#chat-input')).toBeVisible({ timeout: 10000 });
    await expect(user1.locator('#send-btn')).toBeVisible();

    await ctx1.close();
    await ctx2.close();
  });
});
