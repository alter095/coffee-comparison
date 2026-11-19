// Package api serves the comparison JSON API and static UI.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/alter095/coffee-comparison/internal/comparison"
	"github.com/alter095/coffee-comparison/internal/store"
)

func New(s *store.Store) http.Handler { return NewWithAssets(s, nil) }

// NewWithAssets serves only the UI entry point and its generated assets.
// Unknown API paths must never fall back to an HTML page.
func NewWithAssets(s *store.Store, assets fs.FS) http.Handler {
	a := &api{store: s}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Health(r.Context()); err != nil {
			fail(w, 503, "database unavailable")
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/shops", a.shops)
	mux.HandleFunc("POST /api/shops", a.createShop)
	mux.HandleFunc("PATCH /api/shops/{id}", a.updateShop)
	mux.HandleFunc("GET /api/beans", a.beans)
	mux.HandleFunc("POST /api/beans", a.createBean)
	mux.HandleFunc("PATCH /api/beans/{id}", a.updateBean)
	mux.HandleFunc("POST /api/beans/{id}/variants", a.createVariant)
	mux.HandleFunc("PATCH /api/variants/{id}", a.updateVariant)
	mux.HandleFunc("DELETE /api/shops/{id}", a.deleteRecord("shop"))
	mux.HandleFunc("DELETE /api/beans/{id}", a.deleteRecord("bean"))
	mux.HandleFunc("DELETE /api/variants/{id}", a.deleteRecord("variant"))
	if assets != nil {
		files := http.FileServerFS(assets)
		mux.Handle("GET /{$}", files)
		mux.Handle("GET /assets/", files)
	}
	protection := http.NewCrossOriginProtection()
	protection.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fail(w, 403, "cross-origin request rejected") }))
	return protection.Handler(mux)
}

type api struct{ store *store.Store }

func (a *api) shops(w http.ResponseWriter, r *http.Request) {
	shops, err := a.store.Shops(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"shops": shops})
}

func (a *api) createShop(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name      string `json:"name"`
		Shipping  *int64 `json:"shipping_yen"`
		Threshold *int64 `json:"free_shipping_threshold_yen"`
	}
	if !decode(w, r, &input) {
		return
	}
	id, err := a.store.CreateShop(r.Context(), input.Name, input.Shipping, input.Threshold)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, 201, store.Shop{ID: id, Name: input.Name, ShippingYen: input.Shipping, FreeShippingThresholdYen: input.Threshold, Version: 1})
}

func (a *api) createBean(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ShopID int64   `json:"shop_id"`
		Name   string  `json:"name"`
		URL    *string `json:"product_url"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.ShopID <= 0 || !validURL(input.URL) {
		fail(w, 400, "invalid shop_id or product_url")
		return
	}
	id, err := a.store.CreateBean(r.Context(), input.ShopID, input.Name, input.URL)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "shop_id": input.ShopID, "name": input.Name, "product_url": input.URL, "status": "candidate", "buy_again": false, "version": 1})
}

func validURL(value *string) bool {
	if value == nil {
		return true
	}
	if !utf8.ValidString(*value) || utf8.RuneCountInString(*value) > 4096 || strings.ContainsRune(*value, 0) {
		return false
	}
	u, err := url.Parse(*value)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}

func (a *api) createVariant(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var input struct {
		Weight *int64 `json:"weight_g"`
		Price  *int64 `json:"price_yen"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Weight == nil || input.Price == nil {
		fail(w, 400, "weight_g and price_yen are required")
		return
	}
	v, err := a.store.CreateVariant(r.Context(), id, *input.Weight, *input.Price)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, 201, v)
}

func (a *api) beans(w http.ResponseWriter, r *http.Request) {
	beans, err := a.store.Beans(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	type quotedVariant struct {
		store.Variant
		Quote comparison.Quote `json:"quote"`
	}
	type row struct {
		store.Bean
		Variants []quotedVariant `json:"variants"`
	}
	out := make([]row, 0, len(beans))
	for _, b := range beans {
		variants := make([]quotedVariant, 0, len(b.Variants))
		for _, v := range b.Variants {
			quote, err := comparison.Online(v.PriceYen, v.WeightG, b.Shop.ShippingYen, b.Shop.FreeShippingThresholdYen)
			if err != nil {
				serverError(w, err)
				return
			}
			variants = append(variants, quotedVariant{v, quote})
		}
		out = append(out, row{b, variants})
	}
	writeJSON(w, 200, map[string]any{"beans": out})
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(w, 400, "invalid id")
		return 0, false
	}
	return id, true
}

// decode accepts one bounded JSON value, rejecting unknown fields, trailing
// values and invalid UTF-8. Pointer fields distinguish zero from missing/null.
func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		fail(w, 415, "Content-Type must be application/json")
		return false
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			fail(w, 413, "request body too large")
		} else {
			fail(w, 400, "invalid request body")
		}
		return false
	}
	if !utf8.Valid(data) {
		fail(w, 400, "invalid UTF-8")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		fail(w, 400, "invalid JSON or fields")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		fail(w, 400, "expected one JSON value")
		return false
	}
	return true
}

func storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrInvalid):
		fail(w, 400, "invalid data or referenced record")
	case errors.Is(err, store.ErrNotFound):
		fail(w, 404, "record not found")
	default:
		serverError(w, err)
	}
}

func serverError(w http.ResponseWriter, err error) {
	slog.Error("API operation failed", "error", err)
	fail(w, 500, "internal server error")
}

func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
