package comparison

import "testing"

func ptr(v int64) *int64 { return &v }

func TestOnline(t *testing.T) {
	tests := []struct {
		name                string
		price, weight       int64
		shipping, threshold *int64
		total               *int64
	}{
		{"200g", 1600, 200, ptr(600), ptr(4000), ptr(2200)},
		{"500g", 3600, 500, ptr(600), ptr(4000), ptr(4200)},
		{"below threshold", 3999, 200, ptr(600), ptr(4000), ptr(4599)},
		{"at threshold", 4000, 200, ptr(600), ptr(4000), ptr(4000)},
		{"above threshold", 4001, 200, ptr(600), ptr(4000), ptr(4001)},
		{"unknown shipping", 1600, 200, nil, nil, nil},
		{"unknown below threshold", 3999, 200, nil, ptr(4000), nil},
		{"known free at threshold", 4000, 200, nil, ptr(4000), ptr(4000)},
		{"explicit free", 1600, 200, ptr(0), nil, ptr(1600)},
		{"zero threshold", 0, 100, nil, ptr(0), ptr(0)},
		{"maximum", MaxValue, MaxValue, ptr(MaxValue), nil, ptr(2 * MaxValue)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := Online(tt.price, tt.weight, tt.shipping, tt.threshold)
			if err != nil {
				t.Fatal(err)
			}
			if q.ProductPer100G != (UnitPrice{tt.price * 100, tt.weight}) {
				t.Fatal(q)
			}
			if tt.total == nil {
				if q.TotalYen != nil || q.ShippingYen != nil || q.EffectivePer100G != nil {
					t.Fatal(q)
				}
				return
			}
			if q.TotalYen == nil || *q.TotalYen != *tt.total {
				t.Fatalf("got %+v, want %d", q, *tt.total)
			}
			if q.ShippingYen == nil || *q.ShippingYen != *tt.total-tt.price {
				t.Fatal(q)
			}
			if q.EffectivePer100G == nil || *q.EffectivePer100G != (UnitPrice{*tt.total * 100, tt.weight}) {
				t.Fatal(q)
			}
		})
	}
}

func TestOnlineRejectsInvalidInputs(t *testing.T) {
	for _, args := range [][4]int64{
		{-1, 100, 0, 0}, {MaxValue + 1, 100, 0, 0},
		{100, 0, 0, 0}, {100, -1, 0, 0}, {100, MaxValue + 1, 0, 0},
		{100, 100, -1, 0}, {100, 100, MaxValue + 1, 0},
		{100, 100, 0, -1}, {100, 100, 0, MaxValue + 1},
	} {
		if _, err := Online(args[0], args[1], ptr(args[2]), ptr(args[3])); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestUnitPriceComparison(t *testing.T) {
	for _, tt := range []struct {
		a, b UnitPrice
		less bool
	}{
		{UnitPrice{175000, 200}, UnitPrice{95000, 100}, true},
		{UnitPrice{100000, 300}, UnitPrice{100100, 300}, true},
		{UnitPrice{100000, 200}, UnitPrice{200000, 400}, false},
		{UnitPrice{200 * MaxValue, MaxValue}, UnitPrice{200 * MaxValue, 1}, true},
	} {
		if got := tt.a.Less(tt.b); got != tt.less {
			t.Fatalf("%v < %v: %v", tt.a, tt.b, got)
		}
	}
}
