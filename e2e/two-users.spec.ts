import { test, expect, Page } from '@playwright/test';
import { registerUser, loginUser, uniqueEmail, waitForChatConnected } from './helpers';

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

  // H1 (PASS-7): получатель личного чата (не-владелец) видит баннер согласия
  // и может принять чат; после принятия обе стороны могут писать. Раньше при
  // инициаторе с большим ID получатель не мог принять вовсе, а инициатор мог
  // принять сам себя.
  test('personal chat: recipient accepts consent and both can write', async ({ browser }) => {
    const ctx1 = await browser.newContext();
    const ctx2 = await browser.newContext();
    const user1 = await ctx1.newPage();
    const user2 = await ctx2.newPage();

    const email1 = uniqueEmail('a1');
    const email2 = uniqueEmail('a2');
    const password = 'Password123!';
    // Уникальные имена — findUserId ищет users[0] по имени, а при повторе
    // прогонов старые 'E2E Sender' остаются в БД (найденный чужой пользователь
    // ломал флоу: комната с другим владельцем, получатель не видел баннер).
    const name1 = `Sender${Date.now()}`;
    const name2 = `Recipient${Date.now()}`;

    await registerUser(user1, email1, password, name1);
    await registerUser(user2, email2, password, name2);
    await loginUser(user1, email1, password);
    await expect(user1).toHaveURL(/\/dashboard/, { timeout: 15000 });
    await loginUser(user2, email2, password);
    await expect(user2).toHaveURL(/\/dashboard/, { timeout: 15000 });

    // user1 находит id user2 и открывает личный чат (инициатор = user1).
    const id2 = await findUserId(user1, name2);
    await user1.goto(`/users/${id2}`);
    await user1.locator('a[href^="/chat/personal/"]').first().click();
    await expect(user1).toHaveURL(/\/chat\/personal\/\d+/, { timeout: 10000 });
    // Инициатор (владелец) видит активный ввод сразу, без баннера.
    await expect(user1.locator('#chat-input')).toBeEnabled({ timeout: 10000 });

    // user2 открывает тот же чат (получатель) — видит баннер согласия.
    const id1 = await findUserId(user2, name1);
    await user2.goto(`/chat/personal/${id1}`);
    await expect(user2.locator('#consent-banner')).toBeVisible({ timeout: 10000 });
    await expect(user2.locator('#chat-input')).toBeDisabled();

    // Получатель принимает чат.
    await user2.locator('#accept-chat-btn').click();
    // После принятия инпут разблокируется (страница перезагружается).
    await expect(user2.locator('#chat-input')).toBeEnabled({ timeout: 10000 });
    await expect(user2.locator('#consent-banner')).toBeHidden();

    // Обе стороны могут писать и видят сообщения.
    await user1.fill('#chat-input', 'hello from sender');
    await user1.click('#send-btn');
    await expect(user2.locator('#chat-messages')).toContainText('hello from sender', { timeout: 10000 });

    // user2 перезагрузил страницу после принятия — ждём готовности WebSocket,
    // иначе send() дропнет сообщение (createReconnectingWebSocket).
    await waitForChatConnected(user2);
    await user2.fill('#chat-input', 'hello from recipient');
    await user2.click('#send-btn');
    await expect(user1.locator('#chat-messages')).toContainText('hello from recipient', { timeout: 10000 });

    await ctx1.close();
    await ctx2.close();
  });

  // H1 (PASS-7) E2E-дополнение: инициатор с БОЛЬШИМ id.
  // Регистрируем user2 (больший id) первым как инициатора чата с user1 (меньший id).
  // Получатель user1 — user1_id в комнате; он должен суметь принять (регрессия PASS-6).
  test('personal chat: initiator with higher id cannot self-accept, recipient accepts', async ({ browser }) => {
    const ctx1 = await browser.newContext();
    const ctx2 = await browser.newContext();
    const user1 = await ctx1.newPage();
    const user2 = await ctx2.newPage();

    const email1 = uniqueEmail('b1');
    const email2 = uniqueEmail('b2');
    const password = 'Password123!';
    const name1 = `Low${Date.now()}`;
    const name2 = `High${Date.now()}`;

    // Регистрируем user1 ПЕРВЫМ — у него меньший id.
    await registerUser(user1, email1, password, name1);
    await registerUser(user2, email2, password, name2);
    await loginUser(user1, email1, password);
    await expect(user1).toHaveURL(/\/dashboard/, { timeout: 15000 });
    await loginUser(user2, email2, password);
    await expect(user2).toHaveURL(/\/dashboard/, { timeout: 15000 });

    // Инициатор — user2 (больший id).
    const id1 = await findUserId(user2, name1);
    await user2.goto(`/users/${id1}`);
    await user2.locator('a[href^="/chat/personal/"]').first().click();
    await expect(user2).toHaveURL(/\/chat\/personal\/\d+/, { timeout: 10000 });
    // Владелец может писать сразу.
    await expect(user2.locator('#chat-input')).toBeEnabled({ timeout: 10000 });

    // Получатель (user1, меньший id) видит баннер и принимает.
    const id2 = await findUserId(user1, name2);
    await user1.goto(`/chat/personal/${id2}`);
    await expect(user1.locator('#consent-banner')).toBeVisible({ timeout: 10000 });
    await expect(user1.locator('#chat-input')).toBeDisabled();
    await user1.locator('#accept-chat-btn').click();
    await expect(user1.locator('#chat-input')).toBeEnabled({ timeout: 10000 });

    // Инициатор НЕ имеет кнопки принятия (не получатель) — баннера нет.
    await expect(user2.locator('#consent-banner')).toBeHidden({ timeout: 10000 });

    // user1 (получатель) пишет, user2 видит.
    // user1 перезагрузил страницу после принятия — ждём готовности WebSocket,
    // иначе send() дропнет сообщение (createReconnectingWebSocket).
    await waitForChatConnected(user1);
    await user1.fill('#chat-input', 'accepted by lower-id recipient');
    await user1.click('#send-btn');
    await expect(user2.locator('#chat-messages')).toContainText('accepted by lower-id recipient', { timeout: 10000 });

    await ctx1.close();
    await ctx2.close();
  });
});
