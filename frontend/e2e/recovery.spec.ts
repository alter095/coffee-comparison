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

for (const deletedKind of ['variant', 'bean'] as const) {
  test(`deleted ${deletedKind} can be discarded without losing another price draft`, async ({
    page,
    request,
  }) => {
    const shop = await (
      await request.post('/api/shops', {
        data: { name: '削除復旧店', shipping_yen: 0 },
      })
    ).json();
    const records: Array<{
      bean: { id: number };
      variant: { id: number; version: number };
    }> = [];
    for (const name of ['削除対象豆', '下書き保持豆']) {
      const bean = await (
        await request.post('/api/beans', {
          data: { name, shop_id: shop.id },
        })
      ).json();
      const variant = await (
        await request.post(`/api/beans/${bean.id}/variants`, {
          data: { weight_g: 200, price_yen: 1600 },
        })
      ).json();
      records.push({ bean, variant });
    }
    await page.goto('/');
    const other = page.getByRole('article', {
      name: '下書き保持豆',
      exact: true,
    });
    const otherPrice = other.getByLabel('下書き保持豆 200g の価格', {
      exact: true,
    });
    await page.route(`**/api/variants/${records[1].variant.id}`, (route) =>
      route.abort(),
    );
    await otherPrice.fill('1800');
    await otherPrice.press('Enter');
    await expect(other).toContainText('保存失敗');
    const { beans } = await (await request.get('/api/beans')).json();
    const deletion =
      deletedKind === 'variant'
        ? await request.delete(`/api/variants/${records[0].variant.id}`, {
            data: { version: records[0].variant.version },
          })
        : await request.delete(`/api/beans/${records[0].bean.id}`, {
            data: {
              version: beans.find(
                (b: { id: number }) => b.id === records[0].bean.id,
              ).version,
            },
          });
    expect(deletion.ok()).toBe(true);
    const row = page.getByRole('article', { name: '削除対象豆', exact: true });
    const price = row.getByLabel('削除対象豆 200g の価格', { exact: true });
    await price.fill('1700');
    await price.press('Enter');
    await expect(row).toContainText('削除されています');
    await expect(price).toHaveValue('1700');
    await row
      .getByRole('button', {
        name: 'この下書きを破棄して一覧を更新',
        exact: true,
      })
      .click();
    if (deletedKind === 'bean') await expect(row).toHaveCount(0);
    else await expect(row).toContainText('容量未登録');
    await expect(otherPrice).toHaveValue('1800');
    await expect(other).toContainText('保存失敗');
    await page.unroute(`**/api/variants/${records[1].variant.id}`);
    await other.getByRole('button', { name: '再試行', exact: true }).click();
    await expect(other).toContainText('保存済み');
    await expect(
      page.getByRole('button', { name: '＋ 豆を追加', exact: true }).first(),
    ).toBeEnabled();
  });
}

for (const reapply of [false, true]) {
  test(`price conflict preserves the draft and recovers by ${reapply ? 'explicitly saving the input' : 'accepting the saved value'}`, async ({
    page,
    request,
  }) => {
    const shop = await (
      await request.post('/api/shops', {
        data: { name: '価格競合店', shipping_yen: 0 },
      })
    ).json();
    const bean = await (
      await request.post('/api/beans', {
        data: { name: '価格競合豆', shop_id: shop.id },
      })
    ).json();
    const variant = await (
      await request.post(`/api/beans/${bean.id}/variants`, {
        data: { weight_g: 200, price_yen: 1600 },
      })
    ).json();
    await page.goto('/');
    const row = page.getByRole('article', { name: '価格競合豆', exact: true });
    const price = row.getByLabel('価格競合豆 200g の価格', { exact: true });
    await expect(price).toHaveValue('1600');
    // Simulate another device saving after this browser loaded its version.
    expect(
      (
        await request.patch(`/api/variants/${variant.id}`, {
          data: { version: variant.version, price_yen: 1900 },
        })
      ).status(),
    ).toBe(200);
    let patches = 0;
    page.on('request', (req) => {
      if (
        req.method() === 'PATCH' &&
        new URL(req.url()).pathname === `/api/variants/${variant.id}`
      )
        patches++;
    });
    const conflict = page.waitForResponse(
      (res) =>
        res.request().method() === 'PATCH' &&
        new URL(res.url()).pathname === `/api/variants/${variant.id}`,
    );
    await price.fill('1700');
    await price.press('Enter');
    expect((await conflict).status()).toBe(409);
    await expect(row).toContainText('他の端末で変更されました');
    await expect(row.locator('.conflict-box')).toContainText('1,900');
    await expect(price).toHaveValue('1700');
    const before = await (await request.get('/api/beans')).json();
    expect(before.beans[0].variants[0].price_yen).toBe(1900);
    expect(patches).toBe(1);
    await row
      .getByRole('button', {
        name: reapply ? '入力値で再保存' : '保存先の値を使う',
        exact: true,
      })
      .click();
    await expect(row).toContainText('保存済み');
    await expect(row.locator('.conflict-box')).toHaveCount(0);
    await expect(price).toHaveValue(reapply ? '1700' : '1900');
    const after = await (await request.get('/api/beans')).json();
    expect(after.beans[0].variants[0].price_yen).toBe(reapply ? 1700 : 1900);
    expect(patches).toBe(reapply ? 2 : 1);
    await expect(
      page.getByRole('button', { name: '＋ 豆を追加', exact: true }).first(),
    ).toBeEnabled();
  });
}

test('initial capacity POST that never reaches the server can be retried without duplicate records', async ({
  page,
  request,
}) => {
  await page.goto('/');
  await page.getByRole('button', { name: '＋ 豆を追加', exact: true }).click();
  const panel = page.getByRole('region', { name: '豆の登録' });
  await panel.getByLabel('豆の名前', { exact: true }).fill('未到達復旧豆');
  await panel.getByLabel('店舗名', { exact: true }).fill('未到達復旧店');
  await panel.getByLabel('容量（g）', { exact: true }).fill('200');
  await panel.getByLabel('価格（円）', { exact: true }).fill('1600');
  let posts = 0;
  await page.route('**/api/beans/*/variants', async (route) => {
    posts++;
    // Abort before forwarding: the first capacity must remain unsaved.
    if (posts === 1) await route.abort();
    else await route.continue();
  });
  await panel.getByRole('button', { name: '豆を登録', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('容量の登録を再試行');
  await expect(panel.getByLabel('価格（円）', { exact: true })).toHaveValue(
    '1600',
  );
  const before = await (await request.get('/api/beans')).json();
  expect(before.beans).toHaveLength(1);
  expect(before.beans[0].variants).toHaveLength(0);
  await panel
    .getByRole('button', { name: '容量の登録を再試行', exact: true })
    .click();
  const row = page.getByRole('article', { name: '未到達復旧豆', exact: true });
  await expect(row.getByLabel('未到達復旧豆 200g の価格')).toHaveValue('1600');
  expect(posts).toBe(2);
  const after = await (await request.get('/api/beans')).json();
  expect(after.beans).toHaveLength(1);
  expect(after.beans[0].id).toBe(before.beans[0].id);
  expect(after.beans[0].variants).toHaveLength(1);
  expect(after.beans[0].variants[0]).toMatchObject({
    weight_g: 200,
    price_yen: 1600,
  });
  const { shops } = await (await request.get('/api/shops')).json();
  expect(shops).toHaveLength(1);
  await page.getByRole('button', { name: '＋ 豆を追加', exact: true }).click();
  await expect(panel.getByLabel('豆の名前', { exact: true })).toBeEnabled();
});

for (const changedPrice of [false, true]) {
  test(`lost initial capacity response recovers ${changedPrice ? 'with explicit acceptance of a changed saved price' : 'without duplicate records'}`, async ({
    page,
    request,
  }) => {
    await page.goto('/');
    await page
      .getByRole('button', { name: '＋ 豆を追加', exact: true })
      .click();
    const panel = page.getByRole('region', { name: '豆の登録' });
    await panel.getByLabel('豆の名前', { exact: true }).fill('応答喪失豆');
    await panel.getByLabel('店舗名', { exact: true }).fill('応答喪失店');
    await panel.getByLabel('容量（g）', { exact: true }).fill('200');
    await panel.getByLabel('価格（円）', { exact: true }).fill('1600');
    let posts = 0;
    await page.route('**/api/beans/*/variants', async (route) => {
      posts++;
      if (posts > 1) {
        await route.continue();
        return;
      }
      const response = await route.fetch();
      expect(response.status()).toBe(201);
      await route.abort();
    });
    await panel.getByRole('button', { name: '豆を登録', exact: true }).click();
    await expect(page.getByRole('alert')).toContainText('容量の登録を再試行');
    const saved = (await (await request.get('/api/beans')).json()).beans[0];
    if (changedPrice) {
      expect(
        (
          await request.patch(`/api/variants/${saved.variants[0].id}`, {
            data: { version: saved.variants[0].version, price_yen: 1900 },
          })
        ).ok(),
      ).toBe(true);
    }
    await panel
      .getByRole('button', { name: '容量の登録を再試行', exact: true })
      .click();
    if (changedPrice) {
      await expect(panel.getByLabel('価格（円）', { exact: true })).toHaveValue(
        '1600',
      );
      await expect(panel).toContainText('1,900');
      await panel
        .getByRole('button', {
          name: '保存済みの容量で登録を完了',
          exact: true,
        })
        .click();
    }
    const row = page.getByRole('article', { name: '応答喪失豆', exact: true });
    await expect(row).toBeVisible();
    await expect(
      row.getByLabel('応答喪失豆 200g の価格', { exact: true }),
    ).toHaveValue(changedPrice ? '1900' : '1600');
    expect(posts).toBe(1);
    const { beans } = await (await request.get('/api/beans')).json();
    expect(beans).toHaveLength(1);
    expect(beans[0].variants).toHaveLength(1);
    await expect(
      page.getByRole('button', { name: '＋ 豆を追加', exact: true }).first(),
    ).toBeEnabled();
    await page
      .getByRole('button', { name: '＋ 豆を追加', exact: true })
      .click();
    await expect(panel.getByLabel('豆の名前', { exact: true })).toBeEnabled();
  });
}
