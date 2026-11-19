// Package comparison calculates coffee prices without rounding comparison keys.
package comparison

import "fmt"

// MaxValue bounds API inputs so all cost and comparison products fit in int64.
const MaxValue int64 = 1_000_000

// UnitPrice is an exact yen-per-100g ratio. Round only for display.
type UnitPrice struct {
	YenTimes100 int64 `json:"yen_times_100"`
	WeightG     int64 `json:"weight_g"`
}

func (p UnitPrice) Less(other UnitPrice) bool {
	return p.YenTimes100*other.WeightG < other.YenTimes100*p.WeightG
}

// Quote uses nil totals when shipping is unknown. Zero means explicitly free.
type Quote struct {
	ProductPer100G   UnitPrice  `json:"product_per_100g"`
	ShippingYen      *int64     `json:"shipping_yen"`
	TotalYen         *int64     `json:"total_yen"`
	EffectivePer100G *UnitPrice `json:"effective_per_100g"`
}

// Online calculates the checkout total for one selected package, including
// shipping. A threshold applies to that package's price, not other purchases.
func Online(priceYen, weightG int64, shippingYen, thresholdYen *int64) (Quote, error) {
	if priceYen < 0 || priceYen > MaxValue || weightG <= 0 || weightG > MaxValue {
		return Quote{}, fmt.Errorf("price must be 0..%d and weight 1..%d", MaxValue, MaxValue)
	}
	for _, value := range []*int64{shippingYen, thresholdYen} {
		if value != nil && (*value < 0 || *value > MaxValue) {
			return Quote{}, fmt.Errorf("shipping and threshold must be 0..%d", MaxValue)
		}
	}
	q := Quote{ProductPer100G: UnitPrice{priceYen * 100, weightG}}
	var shipping int64
	if thresholdYen != nil && priceYen >= *thresholdYen {
		shipping = 0
	} else if shippingYen != nil {
		shipping = *shippingYen
	} else {
		return q, nil
	}
	total := priceYen + shipping
	q.ShippingYen = &shipping
	q.TotalYen = &total
	q.EffectivePer100G = &UnitPrice{total * 100, weightG}
	return q, nil
}
