import { cost, maxQuantity } from './cost.ts';
import type { Shop, Variant } from './types.ts';

export const maxTargetWeight = 1_000_000;

export type PurchasePlan =
  | {
      kind: 'ready';
      variant: Variant;
      quantity: number;
      totalWeight: number;
      shortage: number;
      priceUncertain: boolean;
      quantityLimited: boolean;
    }
  | {
      kind: 'unavailable';
      reason: 'no-variants' | 'too-heavy' | 'quantity-limit';
      quantityLimited: boolean;
    };

export function planPurchase(
  variants: Variant[],
  shop: Shop,
  target: number,
): PurchasePlan {
  if (!Number.isInteger(target) || target < 1 || target > maxTargetWeight)
    throw new RangeError(
      `希望重量は1〜${maxTargetWeight}gの整数で指定してください。`,
    );

  let quantityLimited = false;
  let best: {
    variant: Variant;
    quantity: number;
  } | null = null;
  for (const variant of variants) {
    const quantity = Math.floor(target / variant.weight_g);
    if (quantity === 0) continue;
    if (quantity > maxQuantity) {
      quantityLimited = true;
      continue;
    }
    if (
      best === null ||
      variant.weight_g > best.variant.weight_g ||
      (variant.weight_g === best.variant.weight_g &&
        variant.id < best.variant.id)
    )
      best = { variant, quantity };
  }
  if (!best)
    return {
      kind: 'unavailable',
      reason:
        variants.length === 0
          ? 'no-variants'
          : quantityLimited
            ? 'quantity-limit'
            : 'too-heavy',
      quantityLimited,
    };
  const totalWeight = best.variant.weight_g * best.quantity;
  return {
    kind: 'ready',
    variant: best.variant,
    quantity: best.quantity,
    totalWeight,
    shortage: target - totalWeight,
    priceUncertain: cost(best.variant, shop, best.quantity).total === null,
    quantityLimited,
  };
}
