import { test, expect } from '@playwright/test';

test.beforeEach(async ({ request }) => {
  const { beans } = await (await request.get('/api/beans')).json();
  for (const bean of beans)
    expect(
      (
        await request.delete(`/api/beans/${bean.id}`, {
          data: { version: bean.version },
        })
      ).ok(),
    ).toBeTruthy();
  const { shops } = await (await request.get('/api/shops')).json();
  for (const shop of shops)
    expect(
      (
        await request.delete(`/api/shops/${shop.id}`, {
          data: { version: shop.version },
        })
      ).ok(),
    ).toBeTruthy();
});

test('shop save confirmation expires and a later save gets its own display time', async ({
  page,
  request,
}) => {
  for (const name of ['通知確認店A', '通知確認店B'])
    await request.post('/api/shops', { data: { name } });
  await page.goto('/#shops');
  const first = page.getByRole('form', { name: '通知確認店A の店舗情報' });
  await expect(first).toBeVisible();
  await page.clock.install();
  await page.clock.pauseAt(new Date());
  await first.getByRole('button', { name: '店舗情報を保存' }).click();
  const confirmation = page
    .getByRole('region', { name: '店舗情報' })
    .getByRole('status', { includeHidden: true });
  await expect(confirmation).toContainText(
    '通知確認店A の店舗情報を保存しました',
  );
  await page.clock.runFor(3000);
  await page
    .getByRole('form', { name: '通知確認店B の店舗情報' })
    .getByRole('button', { name: '店舗情報を保存' })
    .click();
  await expect(confirmation).toContainText(
    '通知確認店B の店舗情報を保存しました',
  );
  await page.clock.runFor(1100);
  await expect(confirmation).toContainText(
    '通知確認店B の店舗情報を保存しました',
  );
  await page.clock.runFor(3000);
  await expect(confirmation).toBeEmpty();
  await first.getByRole('button', { name: '店舗情報を保存' }).click();
  await expect(confirmation).toContainText('保存しました');
  await page.getByRole('link', { name: '比較一覧へ戻る' }).click();
  await page.getByRole('link', { name: '店舗設定', exact: true }).click();
  await expect(confirmation).toBeEmpty();
});

test('shop save errors and drafts remain after the confirmation timeout', async ({
  page,
  request,
}) => {
  const shop = await (
    await request.post('/api/shops', { data: { name: '店舗失敗確認店' } })
  ).json();
  await page.goto('/#shops');
  const form = page.getByRole('form', { name: '店舗失敗確認店 の店舗情報' });
  await form.getByLabel('送料（円）', { exact: true }).fill('600');
  await page.clock.install();
  await page.route(`**/api/shops/${shop.id}`, (route) => route.abort());
  await form.getByRole('button', { name: '店舗情報を保存' }).click();
  await expect(page.getByRole('alert')).toBeVisible();
  await page.clock.runFor(5000);
  await expect(page.getByRole('alert')).toBeVisible();
  await expect(form.getByLabel('送料（円）', { exact: true })).toHaveValue(
    '600',
  );
});

test('a committed write with a lost response is checked before the old price can be saved', async ({
  page,
  request,
}) => {
  const shop = await (
    await request.post('/api/shops', {
      data: { name: '応答喪失テスト店', shipping_yen: 0 },
    })
  ).json();
  const bean = await (
    await request.post('/api/beans', {
      data: { shop_id: shop.id, name: '応答確認の豆' },
    })
  ).json();
  const variant = await (
    await request.post(`/api/beans/${bean.id}/variants`, {
      data: { weight_g: 200, price_yen: 1600 },
    })
  ).json();
  await page.goto('/');
  const row = page.getByRole('article', { name: '応答確認の豆' });
  const price = row.getByLabel('応答確認の豆 200g の価格', { exact: true });
  await page.route(`**/api/variants/${variant.id}`, async (route) => {
    await route.fetch();
    await route.abort();
  });
  await price.fill('1750');
  await price.press('Enter');
  await expect(row).toContainText('保存失敗');
  await page.unrouteAll();
  await price.fill('1600');
  await price.press('Enter');
  await expect(row).toContainText('他の端末で変更されました');
  await expect(price).toHaveValue('1600');
  let data = await (await request.get('/api/beans')).json();
  expect(data.beans[0].variants[0].price_yen).toBe(1750);
  await row
    .getByRole('button', { name: '入力値で再保存', exact: true })
    .click();
  await expect(row).toContainText('保存済み');
  data = await (await request.get('/api/beans')).json();
  expect(data.beans[0].variants[0].price_yen).toBe(1600);
});

test('a delayed response sends a queued confirmation without saving unfinished typing', async ({
  page,
  request,
}) => {
  const shop = await (
    await request.post('/api/shops', {
      data: { name: '遅延テスト店', shipping_yen: 0 },
    })
  ).json();
  const bean = await (
    await request.post('/api/beans', {
      data: { shop_id: shop.id, name: '送信待ちの豆' },
    })
  ).json();
  const variant = await (
    await request.post(`/api/beans/${bean.id}/variants`, {
      data: { weight_g: 200, price_yen: 1600 },
    })
  ).json();
  await page.goto('/');
  const row = page.getByRole('article', { name: '送信待ちの豆' });
  const price = row.getByLabel('送信待ちの豆 200g の価格', { exact: true });
  let release!: () => void, firstCommitted!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  const first = new Promise<void>((resolve) => (firstCommitted = resolve));
  const sent: number[] = [];
  await page.route(`**/api/variants/${variant.id}`, async (route) => {
    sent.push(route.request().postDataJSON().price_yen);
    if (sent.length === 1) {
      const response = await route.fetch();
      firstCommitted();
      await held;
      await route.fulfill({ response });
    } else await route.continue();
  });
  try {
    await price.fill('1700');
    await price.press('Enter');
    await first;
    await price.fill('1800');
    await price.press('Enter');
    await price.fill('2');
    release();
    await expect.poll(() => sent).toEqual([1700, 1800]);
    await expect(price).toHaveValue('2');
    await expect(row).toContainText('未保存');
    const data = await (await request.get('/api/beans')).json();
    expect(data.beans[0].variants[0].price_yen).toBe(1800);
    await price.fill('2000');
    await price.press('Enter');
    await expect(row).toContainText('保存済み');
    expect(sent).toEqual([1700, 1800, 2000]);
  } finally {
    release();
  }
});

test('five-digit amounts fit the card and input at 320px', async ({
  page,
  request,
}) => {
  const shop = await (
    await request.post('/api/shops', {
      data: { name: '5桁確認店', shipping_yen: 0 },
    })
  ).json();
  const bean = await (
    await request.post('/api/beans', {
      data: { shop_id: shop.id, name: '5桁表示の豆' },
    })
  ).json();
  const variant = await (
    await request.post(`/api/beans/${bean.id}/variants`, {
      data: { weight_g: 100, price_yen: 10000 },
    })
  ).json();
  await page.setViewportSize({ width: 320, height: 844 });
  for (const [index, amount] of [10000, 99999].entries()) {
    if (index)
      expect(
        (
          await request.patch(`/api/variants/${variant.id}`, {
            data: { version: 1, price_yen: amount },
          })
        ).ok(),
      ).toBeTruthy();
    await page.goto('/');
    const row = page.getByRole('article', { name: '5桁表示の豆' });
    await expect(
      row.getByLabel('5桁表示の豆 100g の価格', { exact: true }),
    ).toHaveValue(String(amount));
    const visible = await row
      .locator('.price-box input, .metric strong')
      .evaluateAll((elements) =>
        elements.every((element) => {
          const rect = element.getBoundingClientRect();
          const card = element.closest('article')!.getBoundingClientRect();
          if (rect.left < card.left || rect.right > card.right) return false;
          if (element instanceof HTMLInputElement) {
            const style = getComputedStyle(element),
              canvas = document.createElement('canvas'),
              context = canvas.getContext('2d')!;
            context.font = style.font;
            return (
              context.measureText(element.value).width +
                parseFloat(style.paddingLeft) +
                parseFloat(style.paddingRight) <=
              element.clientWidth
            );
          }
          return true;
        }),
      );
    expect(visible).toBe(true);
  }
});

test('login errors are shown inline and a successful login survives reload', async ({
  page,
}) => {
  const cookie = '__Host-coffee_session=browser-test-session';
  await page.route('**/api/**', async (route) => {
    if (route.request().headers().cookie?.includes(cookie)) {
      await route.continue();
      return;
    }
    await route.fulfill({
      status: 401,
      contentType: 'text/plain',
      body: 'authentication required',
    });
  });
  await page.route('**/auth/login', async (route) => {
    const authorization = route.request().headers().authorization;
    if (authorization !== 'Basic Y29mZmVlOmNvcnJlY3Q=') {
      await route.fulfill({
        status: 401,
        contentType: 'text/plain',
        body: 'invalid username or password',
      });
      return;
    }
    await route.fulfill({
      status: 204,
      headers: {
        'set-cookie': `${cookie}; Path=/; Max-Age=43200; Secure; HttpOnly; SameSite=Strict`,
      },
    });
  });
  await page.goto('/');
  await page.getByLabel('ユーザー名', { exact: true }).fill('coffee');
  await page.getByLabel('パスワード', { exact: true }).fill('wrong');
  await page.getByRole('button', { name: 'ログイン', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('違います');
  await expect(page.getByLabel('パスワード', { exact: true })).toBeVisible();
  await page.getByLabel('パスワード', { exact: true }).fill('correct');
  await page.getByRole('button', { name: 'ログイン', exact: true }).click();
  await expect(page.getByText('豆が登録されていません')).toBeVisible();
  await expect(page.getByLabel('希望購入重量（g）')).toBeHidden();
  await expect(
    page.getByRole('button', { name: '条件を変更', exact: true }),
  ).toBeVisible();
  await page.reload();
  await expect(page.getByText('豆が登録されていません')).toBeVisible();
  await expect(page.getByLabel('希望購入重量（g）')).toBeHidden();
  await expect(
    page.getByRole('button', { name: '条件を変更', exact: true }),
  ).toBeVisible();
});

test('expired price saves retain drafts through failed login and can be retried after login', async ({
  page,
  request,
}) => {
  const shop = await (
    await request.post('/api/shops', {
      data: { name: '再認証店', shipping_yen: 0 },
    })
  ).json();
  const bean = await (
    await request.post('/api/beans', {
      data: { shop_id: shop.id, name: '再認証豆' },
    })
  ).json();
  const variant = await (
    await request.post(`/api/beans/${bean.id}/variants`, {
      data: { weight_g: 200, price_yen: 1600 },
    })
  ).json();
  await page.goto('/');
  const row = page.getByRole('article', { name: '再認証豆', exact: true });
  // The comparison is hidden during login; inspect its retained input directly.
  const price = page.locator('input[aria-label="再認証豆 200g の価格"]');
  await expect(price).toHaveValue('1600');
  let authenticated = false;
  await page.route('**/api/**', async (route) => {
    if (authenticated) {
      await route.continue();
      return;
    }
    await route.fulfill({
      status: 401,
      contentType: 'text/plain',
      body: 'authentication required',
    });
  });
  await page.route('**/auth/login', async (route) => {
    if (
      route.request().headers().authorization !== 'Basic Y29mZmVlOmNvcnJlY3Q='
    ) {
      await route.fulfill({
        status: 401,
        contentType: 'text/plain',
        body: 'invalid username or password',
      });
      return;
    }
    authenticated = true;
    await route.fulfill({ status: 204 });
  });
  await price.fill('1750');
  await price.press('Enter');
  await expect(page.getByLabel('ユーザー名', { exact: true })).toBeVisible();
  await expect(price).toHaveValue('1750');
  await page.getByLabel('ユーザー名', { exact: true }).fill('coffee');
  await page.getByLabel('パスワード', { exact: true }).fill('wrong');
  await page.getByRole('button', { name: 'ログイン', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('違います');
  await expect(price).toHaveValue('1750');
  await page.getByLabel('パスワード', { exact: true }).fill('correct');
  await page.getByRole('button', { name: 'ログイン', exact: true }).click();
  await expect(row).toBeVisible();
  await expect(price).toHaveValue('1750');
  await expect(row.locator('.total strong')).toHaveText('¥1,600');
  await row.getByRole('button', { name: '再試行', exact: true }).click();
  await expect(row).toContainText('保存済み');
  await expect(row.locator('.total strong')).toHaveText('¥1,750');
  const { beans } = await (await request.get('/api/beans')).json();
  expect(
    beans[0].variants.find((v: { id: number }) => v.id === variant.id)
      .price_yen,
  ).toBe(1750);
});

test('invalid quantities and unsaved prices never become valid checkout totals', async ({
  page,
  request,
}) => {
  const shop = await (
    await request.post('/api/shops', {
      data: {
        name: '数量検証店',
        shipping_yen: 600,
        free_shipping_threshold_yen: 4000,
      },
    })
  ).json();
  const bean = await (
    await request.post('/api/beans', {
      data: { shop_id: shop.id, name: '数量検証豆' },
    })
  ).json();
  const variant = await (
    await request.post(`/api/beans/${bean.id}/variants`, {
      data: { weight_g: 200, price_yen: 2000 },
    })
  ).json();
  await page.goto('/');
  const row = page.getByRole('article', { name: '数量検証豆', exact: true });
  const quantity = row.getByLabel('数量検証豆 の購入個数');
  for (const value of ['', '0', '1.5', '1000']) {
    await quantity.fill(value);
    await expect(quantity).toHaveAttribute('aria-invalid', 'true');
    await quantity.blur();
    await expect(quantity).toHaveAttribute('aria-invalid', 'true');
    await expect(row.locator('.total strong')).toHaveText('—');
  }
  await quantity.fill('2');
  await quantity.blur();
  await expect(quantity).toHaveAttribute('aria-invalid', 'false');
  await expect(row.locator('.total strong')).toHaveText('¥4,000');
  await page.route(`**/api/variants/${variant.id}`, (route) => route.abort());
  const price = row.getByLabel('数量検証豆 200g の価格', { exact: true });
  await price.fill('1750');
  await price.press('Enter');
  await expect(row).toContainText('保存失敗');
  await expect(row.locator('.total strong')).toHaveText('¥4,000');
  await quantity.fill('3');
  await quantity.blur();
  await expect(row.locator('.total strong')).toHaveText('¥6,000');
  await page.unroute(`**/api/variants/${variant.id}`);
  await row.getByRole('button', { name: '再試行', exact: true }).click();
  await expect(row.locator('.total strong')).toHaveText('¥5,250');
  await quantity.fill('2');
  await quantity.blur();
  await expect(row.locator('.total strong')).toHaveText('¥4,100');
  await expect(row).toContainText('400g・送料 ¥600 込み');
});

test('unified refresh during a committed pending save does not advance the bean version twice', async ({
  page,
  request,
}) => {
  const shop = await (
    await request.post('/api/shops', {
      data: { name: '応答待ち更新店', shipping_yen: 0 },
    })
  ).json();
  const bean = await (
    await request.post('/api/beans', {
      data: { shop_id: shop.id, name: '応答待ち更新豆' },
    })
  ).json();
  const variant = await (
    await request.post(`/api/beans/${bean.id}/variants`, {
      data: { weight_g: 200, price_yen: 1600 },
    })
  ).json();
  await page.goto('/');
  await page.getByRole('button', { name: '条件を変更', exact: true }).click();
  await page
    .getByRole('button', { name: 'この条件で比較', exact: true })
    .click();
  const row = page.getByRole('article', {
    name: '応答待ち更新豆',
    exact: true,
  });
  const price = row.getByLabel('応答待ち更新豆 200g の価格', { exact: true });
  let release!: () => void, committed!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  const serverCommitted = new Promise<void>((resolve) => (committed = resolve));
  await page.route(`**/api/variants/${variant.id}`, async (route) => {
    const response = await route.fetch();
    expect(response.status()).toBe(200);
    committed();
    await held;
    await route.fulfill({ response });
  });
  try {
    await price.fill('1700');
    await price.press('Enter');
    await serverCommitted;
    await page.getByRole('button', { name: '条件を変更', exact: true }).click();
    const refresh = page.getByRole('button', {
      name: 'この条件で比較',
      exact: true,
    });
    await expect(refresh).toBeEnabled();
    await refresh.click();
    await expect(price).toHaveValue('1700');
    await expect(row.locator('.total strong')).toHaveText('¥1,700');
    release();
    await expect(row).toContainText('保存済み');
    const heart = row.locator('.buy-again');
    await heart.click();
    await expect(heart).toHaveAttribute('aria-pressed', 'true');
    await expect(page.getByRole('alert')).toHaveCount(0);
    const { beans } = await (await request.get('/api/beans')).json();
    expect(beans.find((b: { id: number }) => b.id === bean.id).buy_again).toBe(
      true,
    );
  } finally {
    release();
  }
});

test('unified refresh ignores an older list response delivered after a successful price save', async ({
  page,
  request,
}) => {
  const shop = await (
    await request.post('/api/shops', {
      data: { name: '逆順更新店', shipping_yen: 0 },
    })
  ).json();
  const bean = await (
    await request.post('/api/beans', {
      data: { shop_id: shop.id, name: '逆順更新豆' },
    })
  ).json();
  const variant = await (
    await request.post(`/api/beans/${bean.id}/variants`, {
      data: { weight_g: 200, price_yen: 1600 },
    })
  ).json();
  await page.goto('/');
  await page.getByRole('button', { name: '条件を変更', exact: true }).click();
  await page
    .getByRole('button', { name: 'この条件で比較', exact: true })
    .click();
  const row = page.getByRole('article', { name: '逆順更新豆', exact: true });
  const price = row.getByLabel('逆順更新豆 200g の価格', { exact: true });
  await expect(price).toHaveValue('1600');
  let releasePatch!: () => void, patchWaiting!: () => void;
  let releaseRead!: () => void, readCaptured!: () => void;
  const patchHeld = new Promise<void>((resolve) => (releasePatch = resolve));
  const patchStarted = new Promise<void>((resolve) => (patchWaiting = resolve));
  const readHeld = new Promise<void>((resolve) => (releaseRead = resolve));
  const oldListCaptured = new Promise<void>(
    (resolve) => (readCaptured = resolve),
  );
  await page.route(`**/api/variants/${variant.id}`, async (route) => {
    patchWaiting();
    await patchHeld;
    const response = await route.fetch();
    expect(response.status()).toBe(200);
    await route.fulfill({ response });
  });
  await page.route('**/api/beans', async (route) => {
    const response = await route.fetch();
    readCaptured();
    await readHeld;
    await route.fulfill({ response });
  });
  try {
    await price.fill('1700');
    await price.press('Enter');
    await patchStarted;
    await page.getByRole('button', { name: '条件を変更', exact: true }).click();
    await page
      .getByRole('button', { name: 'この条件で比較', exact: true })
      .click();
    await oldListCaptured;
    releasePatch();
    await expect(
      page.getByRole('button', { name: /未保存 \d+件・編集を確認/ }),
    ).toHaveCount(0);
    releaseRead();
    await expect(price).toHaveValue('1700');
    await expect(row.locator('.total strong')).toHaveText('¥1,700');
    const heart = row.locator('.buy-again');
    await heart.click();
    await expect(heart).toHaveAttribute('aria-pressed', 'true');
    await expect(page.getByRole('alert')).toHaveCount(0);
  } finally {
    releasePatch();
    releaseRead();
  }
});

test('unified refresh keeps a newer server price when an older successful save response arrives last', async ({
  page,
  request,
}) => {
  const shop = await (
    await request.post('/api/shops', {
      data: { name: '新値保持店', shipping_yen: 0 },
    })
  ).json();
  const bean = await (
    await request.post('/api/beans', {
      data: { shop_id: shop.id, name: '新値保持豆' },
    })
  ).json();
  const variant = await (
    await request.post(`/api/beans/${bean.id}/variants`, {
      data: { weight_g: 200, price_yen: 1600 },
    })
  ).json();
  await page.goto('/');
  await page.getByRole('button', { name: '条件を変更', exact: true }).click();
  await page
    .getByRole('button', { name: 'この条件で比較', exact: true })
    .click();
  const row = page.getByRole('article', { name: '新値保持豆', exact: true });
  const price = row.getByLabel('新値保持豆 200g の価格', { exact: true });
  let release!: () => void, committed!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  const serverCommitted = new Promise<void>((resolve) => (committed = resolve));
  await page.route(`**/api/variants/${variant.id}`, async (route) => {
    const response = await route.fetch();
    expect(response.status()).toBe(200);
    committed();
    await held;
    await route.fulfill({ response });
  });
  try {
    await price.fill('1700');
    await price.press('Enter');
    await serverCommitted;
    expect(
      (
        await request.patch(`/api/variants/${variant.id}`, {
          data: { version: 2, price_yen: 1900 },
        })
      ).ok(),
    ).toBe(true);
    await page.getByRole('button', { name: '条件を変更', exact: true }).click();
    await page
      .getByRole('button', { name: 'この条件で比較', exact: true })
      .click();
    await expect(price).toHaveValue('1700');
    await expect(row.locator('.total strong')).toHaveText('¥1,900');
    release();
    await expect(row).toContainText('保存済み');
    await expect(price).toHaveValue('1900');
    await expect(row.locator('.total strong')).toHaveText('¥1,900');
    const heart = row.locator('.buy-again');
    await heart.click();
    await expect(heart).toHaveAttribute('aria-pressed', 'true');
    await expect(page.getByRole('alert')).toHaveCount(0);
    const { beans } = await (await request.get('/api/beans')).json();
    expect(beans[0].variants[0].price_yen).toBe(1900);
  } finally {
    release();
  }
});

test('unified refresh preserves fetched deletions when queued saves advance a surviving package', async ({
  page,
  request,
}) => {
  const shop = await (
    await request.post('/api/shops', {
      data: { name: '削除併合店', shipping_yen: 0 },
    })
  ).json();
  const bean = await (
    await request.post('/api/beans', {
      data: { shop_id: shop.id, name: '削除併合豆' },
    })
  ).json();
  const a = await (
    await request.post(`/api/beans/${bean.id}/variants`, {
      data: { weight_g: 200, price_yen: 1600 },
    })
  ).json();
  const b = await (
    await request.post(`/api/beans/${bean.id}/variants`, {
      data: { weight_g: 400, price_yen: 2800 },
    })
  ).json();
  await page.goto('/');
  await page.getByRole('button', { name: '条件を変更', exact: true }).click();
  await page
    .getByRole('button', { name: 'この条件で比較', exact: true })
    .click();
  const row = page.getByRole('article', { name: '削除併合豆', exact: true });
  const price = row.getByLabel('削除併合豆 200g の価格', { exact: true });
  const size = row.getByLabel('削除併合豆 の容量', { exact: true });
  await expect(size.locator('option')).toHaveCount(2);
  let releasePatch!: () => void, patchWaiting!: () => void;
  let releaseRead!: () => void, readCaptured!: () => void;
  const patchHeld = new Promise<void>((resolve) => (releasePatch = resolve));
  const firstPatchStarted = new Promise<void>(
    (resolve) => (patchWaiting = resolve),
  );
  const readHeld = new Promise<void>((resolve) => (releaseRead = resolve));
  const deletedListCaptured = new Promise<void>(
    (resolve) => (readCaptured = resolve),
  );
  const sent: number[] = [];
  await page.route(`**/api/variants/${a.id}`, async (route) => {
    sent.push(route.request().postDataJSON().price_yen);
    if (sent.length === 1) {
      patchWaiting();
      await patchHeld;
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      await route.fulfill({ response });
    } else await route.continue();
  });
  await page.route('**/api/beans', async (route) => {
    const response = await route.fetch();
    readCaptured();
    await readHeld;
    await route.fulfill({ response });
  });
  try {
    await price.fill('1700');
    await price.press('Enter');
    await firstPatchStarted;
    await price.fill('1800');
    await price.press('Enter');
    expect(
      (
        await request.delete(`/api/variants/${b.id}`, {
          data: { version: b.version },
        })
      ).ok(),
    ).toBe(true);
    await page.getByRole('button', { name: '条件を変更', exact: true }).click();
    await page
      .getByRole('button', { name: 'この条件で比較', exact: true })
      .click();
    await deletedListCaptured;
    releasePatch();
    await expect.poll(() => sent).toEqual([1700, 1800]);
    await expect(
      page.getByRole('button', { name: /未保存 \d+件・編集を確認/ }),
    ).toHaveCount(0);
    releaseRead();
    await expect(size.locator('option')).toHaveCount(1);
    await expect(price).toHaveValue('1800');
    await expect(row.locator('.total strong')).toHaveText('¥1,800');
    const heart = row.locator('.buy-again');
    await heart.click();
    await expect(heart).toHaveAttribute('aria-pressed', 'true');
    await expect(page.getByRole('alert')).toHaveCount(0);
    const { beans } = await (await request.get('/api/beans')).json();
    expect(beans[0].variants).toHaveLength(1);
    expect(beans[0].variants[0].price_yen).toBe(1800);
  } finally {
    releasePatch();
    releaseRead();
  }
});

test('remembered login exposes logout in settings and returns to login', async ({
  page,
}) => {
  let active = false;
  await page.route('**/api/**', async (route) => {
    if (active) await route.continue();
    else await route.fulfill({ status: 401, body: 'authentication required' });
  });
  await page.route('**/auth/login', (route) => {
    active = true;
    return route.fulfill({ status: 204 });
  });
  await page.route('**/auth/session', (route) =>
    route.fulfill({
      status: active ? 200 : 401,
      contentType: 'application/json',
      body: JSON.stringify({ authenticated: active }),
    }),
  );
  await page.route('**/auth/logout', (route) => {
    active = false;
    return route.fulfill({ status: 204 });
  });
  await page.goto('/');
  await page.getByLabel('ユーザー名', { exact: true }).fill('coffee');
  await page
    .getByLabel('パスワード', { exact: true })
    .fill('テスト用の十分な長さのパスワード');
  await page.getByRole('button', { name: 'ログイン', exact: true }).click();
  await expect(page.getByText('豆が登録されていません')).toBeVisible();
  await expect(page.getByLabel('希望購入重量（g）')).toBeHidden();
  await expect(
    page.getByRole('button', { name: '条件を変更', exact: true }),
  ).toBeVisible();
  await page.reload();
  await page.getByRole('link', { name: '店舗設定', exact: true }).click();
  await page
    .getByRole('button', { name: 'この端末からログアウト', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'ログイン', exact: true }),
  ).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole('heading', { name: 'ログイン', exact: true }),
  ).toBeVisible();
});
