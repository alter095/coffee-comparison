export interface Variant {
  id: number;
  bean_id: number;
  weight_g: number;
  price_yen: number;
  version: number;
}
export interface Shop {
  id: number;
  name: string;
  shipping_yen: number | null;
  free_shipping_threshold_yen: number | null;
  version: number;
}
export interface Bean {
  id: number;
  shop_id: number;
  name: string;
  product_url: string | null;
  status: 'candidate' | 'excluded';
  buy_again: boolean;
  version: number;
  shop: Shop;
  variants: Variant[];
}
