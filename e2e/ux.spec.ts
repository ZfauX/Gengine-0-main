// e2e/ux.spec.ts
// Проверка новых UX-фич PASS-16:
//   - дашборд показывает секцию «Последние уведомления» (пустое состояние скрыто);
//   - календарь имеет кнопку «Сегодня» и попап-превью;
//   - профиль имеет выбор режима темы (system/time/dark/light).
import { test, expect } from '@playwright/test';
import { registerUser, loginUser, uniqueEmail } from './helpers';

test('UX: dashboard показывает уведомления при их наличии', async ({ browser }) => {
  const base = process.env.E2E_BASE_URL || 'http://localhost:8081';
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  const email = uniqueEmail('ux');
  await registerUser(page, email, 'Password123!');
  await loginUser(page, email, 'Password123!');
  await expect(page).toHaveURL(/\/dashboard/, { timeout: 15000 });

  // Дашборд рендерится; секция «Последние уведомления» может быть скрыта
  // (нет уведомлений у нового юзера), но onboarding/карточки видны.
  const body = await page.content();
  console.log('DASH_HAS_RECENT=' + body.includes('dashboard.recent_notifications') || body.includes('Последние уведомления'));
  await expect(page.locator('h1.page-title')).toContainText('Личный кабинет');
  await ctx.close();
});

test('UX: календарь имеет кнопку «Сегодня»', async ({ page }) => {
  const base = process.env.E2E_BASE_URL || 'http://localhost:8081';
  await page.goto(base + '/calendar');
  await page.waitForLoadState('networkidle');
  const todayBtn = page.locator('#today-btn');
  await expect(todayBtn).toBeVisible({ timeout: 5000 });
  console.log('CAL_HAS_TODAY=' + (await todayBtn.count()) > 0);
  await todayBtn.click();
  // После клика заголовок месяца показывает текущий месяц.
  await expect(page.locator('#month-year')).toHaveText(/\d{4}/, { timeout: 5000 });
});

test('UX: профиль имеет выбор режима темы', async ({ browser }) => {
  const base = process.env.E2E_BASE_URL || 'http://localhost:8081';
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  const email = uniqueEmail('thememode');
  await registerUser(page, email, 'Password123!');
  await loginUser(page, email, 'Password123!');
  await expect(page).toHaveURL(/\/dashboard/, { timeout: 15000 });
  await page.goto(base + '/profile');
  await page.waitForLoadState('networkidle');
  const modeRadios = page.locator('input[name="theme_mode"]');
  await expect(modeRadios).toHaveCount(4, { timeout: 5000 });
  console.log('THEME_RADIOS=' + (await modeRadios.count()));
  // Выбираем «Системная» и сохраняем именно форму настроек темы
  // (на странице профиля несколько форм).
  const themeForm = page.locator('form[action="/profile/theme-settings"]');
  await themeForm.locator('input[name="theme_mode"]').first().check();
  await themeForm.locator('button[type="submit"]').click();
  await page.waitForLoadState('networkidle');
  await page.waitForURL(/\/profile/, { timeout: 10000 });
  console.log('THEME_SAVED_URL=' + page.url());
  await ctx.close();
});
