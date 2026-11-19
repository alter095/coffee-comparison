import { test, expect } from '@playwright/test';

test.beforeEach(async ({ request }) => {
  const { beans } = await (await request.get('/api/beans')).json();
  for (const bean of beans)
    await request.delete(`/api/beans/${bean.id}`, {
      data: { version: bean.version },
    });
  const { shops } = await (await request.get('/api/shops')).json();
  for (const shop of shops)
    await request.delete(`/api/shops/${shop.id}`, {
      data: { version: shop.version },
    });
});
async function seed(request: any) {
  const shop = await (
    await request.post('/api/shops', {
      data: {
        name: '青空珈琲（通販）',
        shipping_yen: 600,
        free_shipping_threshold_yen: 3000,
      },
    })
  ).json();
  const bean = await (
    await request.post('/api/beans', {
      data: { name: '青空ブレンド', shop_id: shop.id },
    })
  ).json();
  for (const [weight_g, price_yen] of [
    [100, 800],
    [200, 1500],
  ])
    await request.post(`/api/beans/${bean.id}/variants`, {
      data: { weight_g, price_yen },
    });
  return { shop, bean };
}
test('one comparison applies weight, budget and sorting; draft and manual edits remain predictable', async ({
  page,
  request,
}) => {
  await seed(request);
  await page.goto('/');
  await page.getByRole('button', { name: '条件を変更', exact: true }).click();
  await page.getByLabel('希望購入重量（g）').fill('500');
  await page.getByLabel('並び順', { exact: true }).selectOption('total');
  await expect(page.getByText('未適用', { exact: true })).toBeVisible();
  const row = page.getByRole('article', { name: '青空ブレンド', exact: true });
  await expect(row.getByLabel('青空ブレンド の購入個数')).toHaveValue('1');
  await page
    .getByRole('button', { name: 'この条件で比較', exact: true })
    .click();
  await expect(row.getByLabel('青空ブレンド の購入個数')).toHaveValue('2');
  await expect(row).toContainText('¥3,000');
  await expect(row).toContainText('不足100g');
  await expect(
    page.getByRole('button', { name: '比較する', exact: true }),
  ).toHaveCount(0);
  await row.getByLabel('青空ブレンド の購入個数').fill('3');
  await row.getByLabel('青空ブレンド の購入個数').blur();
  await page.getByRole('button', { name: '条件を変更', exact: true }).click();
  await page
    .getByRole('button', { name: 'この条件で比較', exact: true })
    .click();
  await expect(row.getByLabel('青空ブレンド の購入個数')).toHaveValue('3');
  await page.getByRole('button', { name: '条件を変更', exact: true }).click();
  await page.getByLabel('希望購入重量（g）').fill('300');
  await page.getByRole('button', { name: '条件を閉じる', exact: true }).click();
  await expect(page.getByText(/表示中：500g以内/)).toBeVisible();
  await page.getByRole('button', { name: '条件を変更', exact: true }).click();
  await expect(page.getByLabel('希望購入重量（g）')).toHaveValue('300');
  await page
    .getByRole('button', { name: 'この条件で比較', exact: true })
    .click();
  await expect(row.getByLabel('青空ブレンド の購入個数')).toHaveValue('1');
});
test('search hundreds of shops, preserve registration draft and warn about similar names', async ({
  page,
  request,
}, info) => {
  await seed(request);
  for (let i = 0; i < 500; i++)
    await request.post('/api/shops', { data: { name: `テスト焙煎店${i}` } });
  await page.goto('/#add');
  const panel = page.getByRole('region', { name: '豆の登録' });
  await panel.getByLabel('豆の名前', { exact: true }).fill('別の豆');
  await panel
    .getByRole('button', { name: '登録済みの店舗を検索・選択', exact: true })
    .click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('店舗名を検索').fill('焙煎店４９９');
  await expect(dialog.getByRole('status')).toHaveText('1件');
  await dialog.getByRole('button', { name: /テスト焙煎店499/ }).click();
  await expect(panel.getByLabel('豆の名前', { exact: true })).toHaveValue(
    '別の豆',
  );
  await panel
    .getByRole('button', { name: 'テスト焙煎店499', exact: true })
    .click();
  await page.keyboard.press('Escape');
  await expect(dialog).not.toBeVisible();
  await expect(panel).toBeVisible();
  await panel
    .getByRole('button', { name: 'テスト焙煎店499', exact: true })
    .click();
  await dialog
    .getByRole('button', { name: '新しい店舗名を入力', exact: true })
    .click();
  await panel.getByLabel('店舗名', { exact: true }).fill('青空珈琲');
  await expect(
    panel.getByText('似た名前の登録済み店舗があります。'),
  ).toBeVisible();
  await panel.getByLabel('容量（g）', { exact: true }).fill('200');
  await panel.getByLabel('価格（円）', { exact: true }).fill('1600');
  await panel.getByRole('button', { name: '豆を登録', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText(
    '似た名前の店舗があります',
  );
  expect((await (await request.get('/api/beans')).json()).beans).toHaveLength(
    1,
  );
  await panel
    .getByRole('button', { name: '青空珈琲（通販）を使う', exact: true })
    .click();
  await panel.getByLabel('容量（g）', { exact: true }).fill('200');
  await panel.getByLabel('価格（円）', { exact: true }).fill('1600');
  for (const width of [320, 360, 390, 412, 768, 1440]) {
    await page.setViewportSize({ width, height: 844 });
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
  }
  await page.setViewportSize(info.project.use.viewport!);
  await page.screenshot({
    path: info.outputPath('registration.png'),
    fullPage: true,
  });
  await panel.getByRole('button', { name: '豆を登録', exact: true }).click();
  await expect(
    page.getByRole('article', { name: '別の豆', exact: true }),
  ).toContainText('¥2,200');
  expect((await (await request.get('/api/shops')).json()).shops).toHaveLength(
    501,
  );
});
test('edit bean and package, reject stale writes, confirm deletion and protect shops in use', async ({
  page,
  request,
}) => {
  const { shop, bean } = await seed(request);
  await page.goto('/');
  let row = page.getByRole('article', { name: '青空ブレンド', exact: true });
  await row.getByText('編集・削除', { exact: true }).click();
  let manager = row.getByRole('region', { name: '青空ブレンド の編集' });
  await manager.getByLabel('豆の名前', { exact: true }).fill('訂正した豆');
  await manager
    .getByRole('button', { name: '豆の情報を保存', exact: true })
    .click();
  row = page.getByRole('article', { name: '訂正した豆', exact: true });
  await expect(row).toBeVisible();
  const form = row.getByRole('form', { name: '100g の容量編集', exact: true });
  await form.getByLabel('容量（g）', { exact: true }).fill('150');
  await form.getByRole('button', { name: '容量を保存', exact: true }).click();
  await expect(
    row.getByRole('form', { name: '150g の容量編集', exact: true }),
  ).toBeVisible();
  page.on('dialog', (d) => d.accept());
  await row
    .getByRole('form', { name: '150g の容量編集', exact: true })
    .getByRole('button', { name: 'この容量を削除', exact: true })
    .click();
  await expect(
    row.getByRole('form', { name: '150g の容量編集', exact: true }),
  ).toHaveCount(0);
  const saved = (await (await request.get('/api/beans')).json()).beans[0];
  await request.patch(`/api/beans/${bean.id}`, {
    data: { version: saved.version, name: '他端末の豆' },
  });
  await row.getByLabel('豆の名前', { exact: true }).fill('競合する入力');
  await row
    .getByRole('button', { name: '豆の情報を保存', exact: true })
    .click();
  await expect(row.getByRole('alert')).toBeVisible();
  await expect(row.getByLabel('豆の名前', { exact: true })).toHaveValue(
    '競合する入力',
  );
  await page.getByRole('link', { name: '店舗設定', exact: true }).click();
  await page.getByRole('button', { name: '店舗を削除', exact: true }).click();
  await expect(page.getByRole('alert')).toBeVisible();
  await page.getByRole('link', { name: '比較一覧', exact: true }).click();
  await page.getByRole('button', { name: '条件を変更', exact: true }).click();
  await page
    .getByRole('button', { name: 'この条件で比較', exact: true })
    .click();
  row = page.getByRole('article', { name: '他端末の豆', exact: true });
  await row.getByRole('button', { name: '豆を削除', exact: true }).click();
  await expect(row).toHaveCount(0);
  await page.getByRole('link', { name: '店舗設定', exact: true }).click();
  await page.getByRole('button', { name: '店舗を削除', exact: true }).click();
  await expect(
    page.getByRole('form', { name: `${shop.name} の店舗情報` }),
  ).toHaveCount(0);
});
test('mobile layout and comparison states do not overflow', async ({
  page,
  request,
}, info) => {
  const measurements: { width: number; height: number }[] = [];
  await seed(request);
  await page.goto('/');
  await page.getByRole('button', { name: '条件を変更', exact: true }).click();
  await page.getByLabel('希望購入重量（g）').fill('500');
  await page
    .getByRole('button', { name: 'この条件で比較', exact: true })
    .click();
  for (const width of [320, 360, 390, 412, 768, 1440]) {
    await page.setViewportSize({ width, height: 844 });
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    const row = page.getByRole('article').first();
    const size = row.locator('.size-cell select');
    const price = row.locator('.price-box input');
    await expect(size).toHaveCSS('font-size', '16px');
    await expect(price).toHaveCSS('font-size', '16px');
    await price.click();
    await expect(row.locator('.price-box')).toHaveCSS('outline-style', 'solid');
    await expect(price).toHaveCSS('outline-style', 'none');
    await size.focus();
    await page.keyboard.press('Tab');
    await expect(price).toBeFocused();
    await expect(row.locator('.price-box')).toHaveCSS('outline-style', 'solid');
    if (width <= 700) {
      const height = (await row.boundingBox())!.height;
      measurements.push({ width, height });
      expect(height).toBeLessThanOrEqual(300);
      expect((await size.boundingBox())!.height).toBeGreaterThanOrEqual(44);
      expect((await price.boundingBox())!.height).toBeGreaterThanOrEqual(44);
    } else {
      expect((await size.boundingBox())!.height).toBe(36);
      const priceBox = (await row.locator('.price-box').boundingBox())!;
      expect(priceBox.height).toBe(36);
      expect(priceBox.width).toBeLessThanOrEqual(120);
    }
  }
  await page.setViewportSize(info.project.use.viewport!);
  await page.screenshot({
    path: info.outputPath('comparison.png'),
    fullPage: true,
  });
  console.log('compact row heights', measurements);
  await page.getByRole('link', { name: '店舗設定', exact: true }).click();
  const threshold = page.getByLabel('送料無料（円）', { exact: true });
  await expect(threshold).toBeVisible();
  for (const width of [320, 390, 768]) {
    await page.setViewportSize({ width, height: 844 });
    const lines = await threshold.evaluate((input) => {
      const label = input.closest('label')!;
      const range = document.createRange();
      range.selectNodeContents(label.firstChild!);
      return new Set(Array.from(range.getClientRects()).map((r) => r.top)).size;
    });
    expect(lines).toBe(1);
  }
});

test('the initial list is sorted and usable with comparison conditions closed', async ({
  page,
  request,
}) => {
  const { shop } = await seed(request);
  for (const [name, price, status] of [
    ['安い豆', 400, 'candidate'],
    ['候補外の豆', 100, 'excluded'],
  ] as const) {
    const bean = await (
      await request.post('/api/beans', {
        data: { name, shop_id: shop.id },
      })
    ).json();
    if (status === 'excluded')
      expect(
        (
          await request.patch(`/api/beans/${bean.id}`, {
            data: { version: bean.version, status },
          })
        ).ok(),
      ).toBe(true);
    await request.post(`/api/beans/${bean.id}/variants`, {
      data: { weight_g: 100, price_yen: price },
    });
  }
  await page.goto('/');
  await expect(page.getByRole('article')).toHaveCount(2);
  await expect(page.getByRole('article').first()).toHaveAccessibleName(
    '安い豆',
  );
  await expect(page.getByLabel('希望購入重量（g）')).toBeHidden();
  await expect(
    page.getByRole('button', { name: 'この条件で比較', exact: true }),
  ).toHaveCount(0);
  const controls = page.getByRole('region', { name: '比較条件', exact: true });
  await expect(controls).toContainText('100g単価順');
  await expect(controls).not.toContainText('送料未設定');
  const row = page.getByRole('article', { name: '安い豆', exact: true });
  await row.getByLabel('安い豆 100g の価格', { exact: true }).fill('450');
  await row.getByLabel('安い豆 100g の価格', { exact: true }).press('Enter');
  await expect(row.locator('.total strong')).toHaveText('¥1,050');
  await page.reload();
  await expect(page.getByRole('article')).toHaveCount(2);
  await expect(page.getByLabel('希望購入重量（g）')).toBeHidden();
});

test('comparison disclosure keeps drafts and results while toggling with two basic actions', async ({
  page,
  request,
}, info) => {
  await seed(request);
  let reads = 0;
  page.on('request', (request) => {
    if (new URL(request.url()).pathname === '/api/beans') reads++;
  });
  await page.goto('/');
  const controls = page.getByRole('region', { name: '比較条件', exact: true });
  const toggle = controls.getByRole('button', {
    name: /^条件を(変更|閉じる)$/,
  });
  const row = page.getByRole('article', { name: '青空ブレンド', exact: true });
  await expect(row.locator('.total strong')).toHaveText('¥1,400');
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  const initialReads = reads;
  const position = (await toggle.boundingBox())!;
  await toggle.focus();
  await toggle.press('Enter');
  await expect(toggle).toHaveAccessibleName('条件を閉じる');
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  await expect(toggle).toBeFocused();
  const expandedPosition = (await toggle.boundingBox())!;
  expect(expandedPosition.x).toBe(position.x);
  expect(expandedPosition.y).toBe(position.y);
  if (info.project.name === 'mobile')
    expect(expandedPosition.height).toBeGreaterThanOrEqual(44);
  await expect(controls.getByRole('button')).toHaveCount(2);
  await page.getByLabel('希望購入重量（g）').fill('500');
  await page.getByLabel('並び順', { exact: true }).selectOption('total');
  await page.getByText('絞り込み条件', { exact: true }).click();
  await page.getByLabel('豆・店舗名で検索').fill('青空');
  await page.getByLabel('予算上限（送料込・円）').fill('4000');
  await toggle.click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await expect(controls.getByText('未適用', { exact: true })).toBeVisible();
  await expect(page.getByLabel('希望購入重量（g）')).toBeHidden();
  await expect(controls.getByRole('button')).toHaveCount(1);
  await expect(row.getByLabel('青空ブレンド の購入個数')).toHaveValue('1');
  await expect(row.locator('.total strong')).toHaveText('¥1,400');
  await expect(controls.locator('.condition-summary')).toContainText(
    '100g単価順',
  );
  await expect(controls.locator('.condition-summary')).not.toContainText(
    '500g',
  );
  expect(reads).toBe(initialReads);
  await page.screenshot({
    path: info.outputPath('conditions-pending.png'),
    fullPage: true,
  });
  await toggle.press('Space');
  await expect(page.getByLabel('希望購入重量（g）')).toHaveValue('500');
  await expect(page.getByLabel('並び順', { exact: true })).toHaveValue('total');
  await expect(page.getByLabel('豆・店舗名で検索')).toBeVisible();
  await expect(page.getByLabel('豆・店舗名で検索')).toHaveValue('青空');
  await expect(page.getByLabel('予算上限（送料込・円）')).toHaveValue('4000');
  await page.screenshot({
    path: info.outputPath('conditions-open.png'),
    fullPage: true,
  });
  await controls
    .getByRole('button', { name: 'この条件で比較', exact: true })
    .click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await expect(controls.getByText('未適用', { exact: true })).toHaveCount(0);
  await expect(controls.locator('.condition-summary')).toContainText(
    '500g以内',
  );
  await expect(row.getByLabel('青空ブレンド の購入個数')).toHaveValue('2');
  await expect(row.locator('.total strong')).toHaveText('¥3,000');
  await expect(page.locator('#comparison-results')).toBeFocused();
});

test('comparison disclosure can close invalid conditions and keep them for correction', async ({
  page,
  request,
}) => {
  await seed(request);
  await page.goto('/');
  const controls = page.getByRole('region', { name: '比較条件', exact: true });
  const toggle = controls.getByRole('button', {
    name: /^条件を(変更|閉じる)$/,
  });
  const target = page.getByLabel('希望購入重量（g）');
  await toggle.click();
  await target.fill('0');
  await toggle.click();
  await expect(target).toBeHidden();
  await expect(controls.getByText('未適用', { exact: true })).toBeVisible();
  await toggle.press('Tab');
  await expect(controls.locator('input:focus, select:focus')).toHaveCount(0);
  await toggle.click();
  await expect(target).toHaveValue('0');
  await controls
    .getByRole('button', { name: 'この条件で比較', exact: true })
    .click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  await expect(target).toBeFocused();
  await expect(controls.locator('.condition-summary')).not.toContainText(
    '0g以内',
  );
  await target.fill('');
  await target.pressSequentially('1e');
  expect(
    await target.evaluate((el: HTMLInputElement) => el.validity.badInput),
  ).toBe(true);
  await toggle.click();
  await expect(controls.getByText('未適用', { exact: true })).toBeVisible();
  await toggle.click();
  expect(
    await target.evaluate((el: HTMLInputElement) => el.validity.badInput),
  ).toBe(true);
  await target.press('ControlOrMeta+A');
  await target.press('Backspace');
  expect(
    await target.evaluate((el: HTMLInputElement) => el.validity.badInput),
  ).toBe(false);
  await page.getByText('絞り込み条件', { exact: true }).click();
  const budget = page.getByLabel('予算上限（送料込・円）');
  await budget.pressSequentially('1e');
  await expect(controls.getByText('未適用', { exact: true })).toBeVisible();
  await controls
    .getByRole('button', { name: '予算・容量をクリア', exact: true })
    .click();
  await expect(budget).toHaveValue('');
  expect(
    await budget.evaluate((el: HTMLInputElement) => el.validity.badInput),
  ).toBe(false);
  await expect(controls.getByText('未適用', { exact: true })).toHaveCount(0);
  await target.fill('300');
  await controls
    .getByRole('button', { name: 'この条件で比較', exact: true })
    .click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await expect(controls.getByText('未適用', { exact: true })).toHaveCount(0);
});

test('quantity feedback distinguishes unavailable plans, unfinished typing and invalid committed input', async ({
  page,
  request,
}) => {
  await seed(request);
  await page.goto('/');
  const row = page.getByRole('article', { name: '青空ブレンド', exact: true });
  const quantity = row.getByLabel('青空ブレンド の購入個数');
  await page.getByRole('button', { name: '条件を変更', exact: true }).click();
  await page.getByLabel('希望購入重量（g）').fill('50');
  await page
    .getByRole('button', { name: 'この条件で比較', exact: true })
    .click();
  await expect(row).toContainText('希望重量に収まる容量がありません');
  await expect(quantity).toHaveAttribute('aria-invalid', 'false');
  await expect(row.locator('.quantity-error')).toHaveCount(0);
  await expect(row.locator('.total strong')).toHaveText('—');
  await quantity.fill('2');
  await quantity.blur();
  await expect(row.locator('.total strong')).toHaveText('¥2,200');
  await quantity.fill('');
  await expect(row.locator('.quantity-error')).toHaveCount(0);
  await quantity.blur();
  await expect(quantity).toHaveAccessibleDescription('個数を入力してください');
  await expect(row.locator('.total strong')).toHaveText('—');
  await quantity.fill('1000');
  await expect(row.locator('.quantity-error')).toHaveCount(0);
  await quantity.blur();
  await expect(quantity).toHaveAccessibleDescription(
    '1〜999個で入力してください',
  );
  await quantity.fill('1');
  await quantity.blur();
  await expect(row.locator('.quantity-error')).toHaveCount(0);
  await page.getByRole('button', { name: '条件を変更', exact: true }).click();
  await page.getByLabel('希望購入重量（g）').fill('1000000');
  await page
    .getByRole('button', { name: 'この条件で比較', exact: true })
    .click();
  await expect(row).toContainText('個数上限999個のため試算不可');
  await expect(quantity).toHaveAttribute('aria-invalid', 'false');
  await expect(row.locator('.quantity-error')).toHaveCount(0);
});

test('unknown shipping is marked on the affected row and noted only for an applied budget', async ({
  page,
  request,
}) => {
  const { shop } = await seed(request);
  await request.patch(`/api/shops/${shop.id}`, {
    data: { version: shop.version, shipping_yen: null },
  });
  await page.goto('/');
  const controls = page.getByRole('region', { name: '比較条件', exact: true });
  const row = page.getByRole('article', { name: '青空ブレンド', exact: true });
  await expect(row.locator('.total')).toContainText('送料未設定');
  await expect(row.locator('.total strong')).toHaveText('—');
  await expect(controls).not.toContainText('送料未設定');
  await page.getByRole('button', { name: '条件を変更', exact: true }).click();
  await page.getByText('絞り込み条件', { exact: true }).click();
  await page.getByLabel('予算上限（送料込・円）', { exact: true }).fill('4000');
  await page
    .getByRole('button', { name: 'この条件で比較', exact: true })
    .click();
  await expect(controls).toContainText('送料未設定も含む');
  await expect(row).toBeVisible();
  await page.getByRole('button', { name: '条件を変更', exact: true }).click();
  await page.getByLabel('希望購入重量（g）').fill('500');
  await page
    .getByRole('button', { name: 'この条件で比較', exact: true })
    .click();
  await expect(row.locator('.total strong')).toHaveText('¥3,000');
  await expect(controls).not.toContainText('送料未設定');
});

test('newest bean order applies explicitly and includes beans without prices or shipping', async ({
  page,
  request,
}, info) => {
  await seed(request);
  const shop = await (
    await request.post('/api/shops', { data: { name: '新しい店舗' } })
  ).json();
  for (const name of ['高い豆', '容量未登録の豆']) {
    const bean = await (
      await request.post('/api/beans', {
        data: { name, shop_id: shop.id },
      })
    ).json();
    if (name === '高い豆')
      await request.post(`/api/beans/${bean.id}/variants`, {
        data: { weight_g: 100, price_yen: 5000 },
      });
  }
  await page.goto('/');
  const rows = page.getByRole('article');
  const names = () =>
    rows.evaluateAll((els) => els.map((el) => el.getAttribute('aria-label')));
  const original = ['青空ブレンド', '高い豆', '容量未登録の豆'];
  await expect(rows).toHaveCount(3);
  expect(await names()).toEqual(original);
  const quantity = page.getByLabel('青空ブレンド の購入個数');
  await quantity.fill('3');
  await quantity.blur();
  await page.getByRole('button', { name: '条件を変更', exact: true }).click();
  await page.getByLabel('並び順', { exact: true }).selectOption('newest');
  expect(await names()).toEqual(original);
  await page
    .getByRole('button', { name: 'この条件で比較', exact: true })
    .click();
  const newest = ['容量未登録の豆', '高い豆', '青空ブレンド'];
  await expect.poll(names).toEqual(newest);
  await expect(page.locator('.condition-summary')).toContainText(
    '登録が新しい順',
  );
  await expect(quantity).toHaveValue('3');
  const price = page.getByLabel('青空ブレンド 100g の価格', { exact: true });
  await price.fill('6000');
  await price.press('Enter');
  await expect(
    page
      .getByRole('article', { name: '青空ブレンド', exact: true })
      .locator('.total strong'),
  ).toHaveText('¥18,000');
  await expect.poll(names).toEqual(newest);
  await page.screenshot({
    path: info.outputPath('newest-beans.png'),
    fullPage: true,
  });
  await page.getByRole('button', { name: '条件を変更', exact: true }).click();
  await page.getByLabel('並び順', { exact: true }).selectOption('unit');
  await page
    .getByRole('button', { name: 'この条件で比較', exact: true })
    .click();
  await expect
    .poll(names)
    .toEqual(['高い豆', '青空ブレンド', '容量未登録の豆']);
  await page.reload();
  await expect(page.locator('.condition-summary')).toContainText('100g単価順');
});

test('shop settings and picker retain newest registration order after edits and selection', async ({
  page,
  request,
}, info) => {
  for (const name of ['あ・古い店舗', 'ん・新しい店舗'])
    await request.post('/api/shops', { data: { name } });
  await page.goto('/#shops');
  const settings = page.getByRole('form', { name: / の店舗情報$/ });
  await expect(settings).toHaveCount(2);
  await expect(
    settings.first().getByLabel('店舗名', { exact: true }),
  ).toHaveValue('ん・新しい店舗');
  await settings
    .last()
    .getByLabel('店舗名', { exact: true })
    .fill('変更した古い店舗');
  await settings
    .last()
    .getByRole('button', { name: '店舗情報を保存', exact: true })
    .click();
  await expect(
    page.getByRole('status').filter({ hasText: '保存しました' }),
  ).toBeVisible();
  await expect(
    settings.first().getByLabel('店舗名', { exact: true }),
  ).toHaveValue('ん・新しい店舗');
  await page.reload();
  await expect(
    settings.first().getByLabel('店舗名', { exact: true }),
  ).toHaveValue('ん・新しい店舗');
  await page.screenshot({
    path: info.outputPath('newest-shops.png'),
    fullPage: true,
  });
  await page.getByRole('button', { name: '＋ 豆を追加', exact: true }).click();
  await page
    .getByRole('button', { name: '登録済みの店舗を検索・選択', exact: true })
    .click();
  const dialog = page.getByRole('dialog');
  const choices = dialog.locator('.shop-results button');
  await expect(choices.first()).toContainText('ん・新しい店舗');
  await choices.last().click();
  await page
    .getByRole('button', { name: '変更した古い店舗', exact: true })
    .click();
  await expect(choices.first()).toContainText('ん・新しい店舗');
  await dialog.getByLabel('店舗名を検索').fill('店舗');
  await expect(choices.first()).toContainText('ん・新しい店舗');
  await dialog.getByRole('button', { name: '戻る', exact: true }).click();
});
