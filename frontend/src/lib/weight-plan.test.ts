import { test } from 'node:test';
import assert from 'node:assert/strict';
import { maxTargetWeight, planPurchase } from './weight-plan.ts';
import type { Shop, Variant } from './types.ts';

const shop: Shop = {
  id: 1,
  name: '試算店',
  shipping_yen: 600,
  free_shipping_threshold_yen: null,
  version: 1,
};
const variant = (id: number, weight_g: number, price_yen: number): Variant => ({
  id,
  bean_id: 1,
  weight_g,
  price_yen,
  version: 1,
});

test('300g uses whole bags without exceeding the requested amount', () => {
  for (const [weight, quantity, totalWeight, shortage] of [
    [100, 3, 300, 0],
    [150, 2, 300, 0],
    [250, 1, 250, 50],
  ]) {
    const v = variant(1, weight, 1000);
    assert.deepEqual(planPurchase([v], shop, 300), {
      kind: 'ready',
      variant: v,
      quantity,
      totalWeight,
      shortage,
      priceUncertain: false,
      quantityLimited: false,
    });
  }
  assert.deepEqual(planPurchase([variant(1, 500, 1000)], shop, 300), {
    kind: 'unavailable',
    reason: 'too-heavy',
    quantityLimited: false,
  });
});

test('500g prefers the 200g package over an exact purchase of 100g packages', () => {
  const result = planPurchase(
    [variant(1, 100, 500), variant(2, 200, 1200)],
    shop,
    500,
  );
  assert.equal(result.kind, 'ready');
  if (result.kind !== 'ready') return;
  assert.equal(result.variant.id, 2);
  assert.equal(result.quantity, 2);
  assert.equal(result.totalWeight, 400);
  assert.equal(result.shortage, 100);
});

test('300g chooses 250g even when smaller packages can reach 300g exactly', () => {
  const result = planPurchase(
    [variant(1, 100, 700), variant(2, 150, 900), variant(3, 250, 2000)],
    shop,
    300,
  );
  assert.equal(result.kind, 'ready');
  if (result.kind !== 'ready') return;
  assert.equal(result.variant.id, 3);
  assert.equal(result.quantity, 1);
  assert.equal(result.shortage, 50);
});

test('package size takes precedence even if the larger package has a higher unit price', () => {
  const result = planPurchase(
    [variant(1, 100, 100), variant(2, 200, 1000)],
    shop,
    500,
  );
  assert.equal(result.kind, 'ready');
  if (result.kind !== 'ready') return;
  assert.equal(result.variant.id, 2);
  assert.equal(result.quantity, 2);
});

test('packages larger than the target cannot displace a usable smaller package', () => {
  const result = planPurchase(
    [variant(1, 100, 1000), variant(2, 500, 1000)],
    shop,
    300,
  );
  assert.equal(result.kind, 'ready');
  if (result.kind !== 'ready') return;
  assert.equal(result.variant.id, 1);
  assert.equal(result.quantity, 3);
});

test('free shipping makes the selected larger package total certain at its quantity', () => {
  const result = planPurchase(
    [variant(1, 100, 1000), variant(2, 150, 1800)],
    { ...shop, shipping_yen: null, free_shipping_threshold_yen: 3500 },
    300,
  );
  assert.equal(result.kind, 'ready');
  if (result.kind !== 'ready') return;
  assert.equal(result.variant.id, 2);
  assert.equal(result.quantity, 2);
  assert.equal(result.priceUncertain, false);
});

test('the selected larger package reports unknown shipping without choosing by subtotal', () => {
  const result = planPurchase(
    [variant(1, 100, 500), variant(2, 150, 900)],
    { ...shop, shipping_yen: null, free_shipping_threshold_yen: 2000 },
    300,
  );
  assert.equal(result.kind, 'ready');
  if (result.kind !== 'ready') return;
  assert.equal(result.variant.id, 2);
  assert.equal(result.quantity, 2);
  assert.equal(result.priceUncertain, true);
});

test('unknown shipping becomes certain at the free shipping threshold', () => {
  const result = planPurchase(
    [variant(1, 100, 600)],
    { ...shop, shipping_yen: null, free_shipping_threshold_yen: 1800 },
    300,
  );
  assert.equal(result.kind, 'ready');
  if (result.kind !== 'ready') return;
  assert.equal(result.priceUncertain, false);
});

test('zero prices are valid and deterministic ties use variant IDs', () => {
  const first = variant(1, 150, 0);
  const second = variant(2, 150, 0);
  const result = planPurchase([second, first], shop, 300);
  assert.equal(result.kind, 'ready');
  if (result.kind !== 'ready') return;
  assert.equal(result.variant.id, 1);
  assert.equal(result.quantity, 2);
});

test('999 bags are valid but 1000 bags are excluded rather than truncated', () => {
  const v = variant(1, 1, 1);
  const exact = planPurchase([v], shop, 999);
  assert.equal(exact.kind, 'ready');
  if (exact.kind !== 'ready') return;
  assert.equal(exact.quantity, 999);
  assert.equal(exact.quantityLimited, false);
  assert.deepEqual(planPurchase([v], shop, 1000), {
    kind: 'unavailable',
    reason: 'quantity-limit',
    quantityLimited: true,
  });
});

test('an excluded oversized quantity is reported even if another package works', () => {
  const result = planPurchase(
    [variant(1, 1, 1), variant(2, 250, 1000)],
    shop,
    1000,
  );
  assert.equal(result.kind, 'ready');
  if (result.kind !== 'ready') return;
  assert.equal(result.variant.id, 2);
  assert.equal(result.quantity, 4);
  assert.equal(result.quantityLimited, true);
});

test('missing sizes and entirely oversized sizes have different explanations', () => {
  assert.deepEqual(planPurchase([], shop, 300), {
    kind: 'unavailable',
    reason: 'no-variants',
    quantityLimited: false,
  });
  assert.equal(
    planPurchase([variant(1, 301, 0)], shop, 300).kind,
    'unavailable',
  );
  assert.deepEqual(
    planPurchase([variant(1, 1, 1), variant(2, 2000, 1)], shop, 1000),
    { kind: 'unavailable', reason: 'quantity-limit', quantityLimited: true },
  );
});

test('the maximum requested weight is supported without exceeding integer bounds', () => {
  assert.equal(maxTargetWeight, 1_000_000);
  const result = planPurchase(
    [variant(1, 1000, 1_000_000), variant(2, 1001, 1_000_000)],
    shop,
    maxTargetWeight,
  );
  assert.equal(result.kind, 'ready');
  if (result.kind !== 'ready') return;
  assert.equal(result.variant.id, 2);
  assert.equal(result.quantity, 999);
  assert.equal(result.totalWeight, 999999);
  assert.equal(result.shortage, 1);
  assert.equal(result.quantityLimited, true);
});

test('invalid requested weights are rejected', () => {
  for (const value of [0, -1, 1.5, NaN, Infinity, maxTargetWeight + 1]) {
    assert.throws(() => planPurchase([], shop, value), RangeError);
  }
});

test('planning does not mutate package data or the input order', () => {
  const packages = [variant(2, 150, 1000), variant(1, 100, 500)];
  const before = structuredClone(packages);
  planPurchase(packages, shop, 300);
  assert.deepEqual(packages, before);
});
