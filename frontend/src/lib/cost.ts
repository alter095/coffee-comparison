import type { Shop, Variant } from './types.ts';
export const yen = (value: number) =>
  `¥${Math.round(value).toLocaleString('ja-JP')}`;
export const maxQuantity = 999;
export function cost(v: Variant, shop: Shop, quantity: number | null = 1) {
  const valid =
    quantity !== null &&
    Number.isInteger(quantity) &&
    quantity >= 1 &&
    quantity <= maxQuantity;
  const subtotal = valid ? v.price_yen * quantity : null;
  const weight = valid ? v.weight_g * quantity : null;
  const shipping =
    subtotal === null
      ? null
      : shop.free_shipping_threshold_yen !== null &&
          subtotal >= shop.free_shipping_threshold_yen
        ? 0
        : shop.shipping_yen;
  const total =
    subtotal === null || shipping === null ? null : subtotal + shipping;
  return {
    subtotal,
    weight,
    shipping,
    total,
    unit: (v.price_yen * 100) / v.weight_g,
    effective:
      total === null || weight === null ? null : (total * 100) / weight,
  };
}
export function compare(
  a: { price: number | null; weight: number },
  b: { price: number | null; weight: number },
  mode: 'unit' | 'total',
) {
  if (a.price === null) return b.price === null ? 0 : 1;
  if (b.price === null) return -1;
  // API bounds make these products exactly representable as JS safe integers.
  return mode === 'unit'
    ? a.price * b.weight - b.price * a.weight
    : a.price - b.price;
}
