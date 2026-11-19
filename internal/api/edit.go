package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/alter095/coffee-comparison/internal/store"
)

// field distinguishes omission (keep), null (clear), and a supplied zero.
// Non-nullable fields explicitly reject Value == nil in their handler.
type field[T any] struct {
	Present bool
	Value   *T
}

func (f *field[T]) UnmarshalJSON(data []byte) error {
	f.Present = true
	return json.Unmarshal(data, &f.Value)
}

func (a *api) updateShop(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Version   *int64        `json:"version"`
		Name      field[string] `json:"name"`
		Shipping  field[int64]  `json:"shipping_yen"`
		Threshold field[int64]  `json:"free_shipping_threshold_yen"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Version == nil || *in.Version <= 0 || (!in.Name.Present && !in.Shipping.Present && !in.Threshold.Present) || (in.Name.Present && in.Name.Value == nil) {
		fail(w, 400, "version and at least one valid change are required")
		return
	}
	current, err := a.store.Shop(r.Context(), id)
	if err != nil {
		storeError(w, err)
		return
	}
	if current.Version != *in.Version {
		conflict(w, current)
		return
	}
	if in.Name.Present {
		current.Name = *in.Name.Value
	}
	if in.Shipping.Present {
		current.ShippingYen = in.Shipping.Value
	}
	if in.Threshold.Present {
		current.FreeShippingThresholdYen = in.Threshold.Value
	}
	updated, err := a.store.UpdateShop(r.Context(), current)
	if mutationError(w, err, updated) {
		return
	}
	writeJSON(w, 200, updated)
}

func conflict(w http.ResponseWriter, current any) {
	writeJSON(w, 409, map[string]any{"error": "record changed since it was read", "current": current})
}

func mutationError(w http.ResponseWriter, err error, current any) bool {
	if errors.Is(err, store.ErrConflict) {
		conflict(w, current)
		return true
	}
	if err != nil {
		storeError(w, err)
		return true
	}
	return false
}

func beanRecord(b store.Bean) map[string]any {
	return map[string]any{"id": b.ID, "shop_id": b.ShopID, "name": b.Name, "product_url": b.ProductURL, "status": b.Status, "buy_again": b.BuyAgain, "version": b.Version}
}

func (a *api) updateBean(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Version  *int64        `json:"version"`
		Name     field[string] `json:"name"`
		URL      field[string] `json:"product_url"`
		Status   field[string] `json:"status"`
		BuyAgain field[bool]   `json:"buy_again"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Version == nil || *in.Version <= 0 || (!in.Name.Present && !in.URL.Present && !in.Status.Present && !in.BuyAgain.Present) || (in.Name.Present && in.Name.Value == nil) || (in.Status.Present && in.Status.Value == nil) || (in.BuyAgain.Present && in.BuyAgain.Value == nil) {
		fail(w, 400, "version and at least one valid change are required")
		return
	}
	current, err := a.store.Bean(r.Context(), id)
	if err != nil {
		storeError(w, err)
		return
	}
	if current.Version != *in.Version {
		conflict(w, beanRecord(current))
		return
	}
	if in.Name.Present {
		current.Name = *in.Name.Value
	}
	if in.URL.Present {
		current.ProductURL = in.URL.Value
	}
	if in.Status.Present {
		current.Status = *in.Status.Value
	}
	if in.BuyAgain.Present {
		current.BuyAgain = *in.BuyAgain.Value
	}
	if !validURL(current.ProductURL) {
		fail(w, 400, "invalid product_url")
		return
	}
	updated, err := a.store.UpdateBean(r.Context(), current)
	if mutationError(w, err, beanRecord(updated)) {
		return
	}
	writeJSON(w, 200, beanRecord(updated))
}

func (a *api) updateVariant(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Version *int64       `json:"version"`
		Weight  field[int64] `json:"weight_g"`
		Price   field[int64] `json:"price_yen"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Version == nil || *in.Version <= 0 || (!in.Weight.Present && !in.Price.Present) || (in.Weight.Present && in.Weight.Value == nil) || (in.Price.Present && in.Price.Value == nil) {
		fail(w, 400, "version and at least one valid change are required")
		return
	}
	current, err := a.store.Variant(r.Context(), id)
	if err != nil {
		storeError(w, err)
		return
	}
	if current.Version != *in.Version {
		conflict(w, current)
		return
	}
	if in.Weight.Present {
		current.WeightG = *in.Weight.Value
	}
	if in.Price.Present {
		current.PriceYen = *in.Price.Value
	}
	updated, err := a.store.UpdateVariant(r.Context(), current)
	if mutationError(w, err, updated) {
		return
	}
	writeJSON(w, 200, updated)
}
