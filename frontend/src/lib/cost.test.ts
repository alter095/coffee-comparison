import { test } from 'node:test';
import assert from 'node:assert/strict';
import { cost, compare } from './cost.ts';
const shop = {
  id: 1,
  name: '店',
  shipping_yen: 600,
  free_shipping_threshold_yen: 4000,
  version: 1,
};
const variant = {
  id: 1,
  bean_id: 1,
  weight_g: 200,
  price_yen: 1600,
  version: 1,
};
test('known, unknown and explicitly free shipping remain distinct', () => {
  assert.equal(cost(variant, shop).total, 2200);
  assert.equal(cost(variant, { ...shop, shipping_yen: null }).total, null);
  assert.equal(cost(variant, { ...shop, shipping_yen: 0 }).total, 1600);
  assert.equal(
    cost({ ...variant, price_yen: 4000 }, { ...shop, shipping_yen: null })
      .total,
    4000,
  );
});
test('sort uses unrounded prices and places unknown totals last', () => {
  assert.ok(
    compare(
      { price: 1000, weight: 300 },
      { price: 1001, weight: 300 },
      'unit',
    ) < 0,
  );
  assert.ok(
    compare(
      { price: null, weight: 200 },
      { price: 2200, weight: 200 },
      'total',
    ) > 0,
  );
});

test('quantity changes the subtotal, weight and free shipping without multiplying shipping', () => {
  const two = cost(variant, shop, 2);
  assert.equal(two.subtotal, 3200);
  assert.equal(two.weight, 400);
  assert.equal(two.shipping, 600);
  assert.equal(two.total, 3800);
  assert.equal(two.unit, 800);
  assert.equal(two.effective, 950);
  assert.equal(cost(variant, shop, 3).total, 4800);
  assert.equal(cost(variant, shop, 3).shipping, 0);
  const unknown = { ...shop, shipping_yen: null };
  assert.equal(cost(variant, unknown, 2).total, null);
  assert.equal(cost(variant, unknown, 3).total, 4800);
  assert.equal(cost({ ...variant, price_yen: 2000 }, shop, 2).total, 4000);
  assert.equal(cost({ ...variant, price_yen: 0 }, shop, 2).total, 600);
});

test('empty and invalid quantities never become a valid purchase total', () => {
  for (const quantity of [null, 0, -1, 1.5, 1000, NaN, Infinity]) {
    const quote = cost(variant, shop, quantity);
    assert.equal(quote.total, null);
    assert.equal(quote.weight, null);
    assert.equal(quote.subtotal, null);
  }
  assert.equal(
    cost({ ...variant, price_yen: 1000000, weight_g: 1000000 }, shop, 999)
      .total,
    999000000,
  );
});
