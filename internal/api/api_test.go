package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/alter095/coffee-comparison/internal/store"
)

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "coffee.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return New(s)
}

func TestHealth(t *testing.T) {
	h := testHandler(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 200 || w.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func request(t *testing.T, h http.Handler, method, path, body string, status int) map[string]json.RawMessage {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatal("expected JSON response")
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCreateCompareAndUpdate(t *testing.T) {
	h := testHandler(t)
	request(t, h, "GET", "/api/shops", "", 200)
	shop := request(t, h, "POST", "/api/shops", `{"name":"焙煎店","shipping_yen":600,"free_shipping_threshold_yen":4000}`, 201)
	if string(shop["id"]) != "1" {
		t.Fatalf("shop %s", shop)
	}
	request(t, h, "POST", "/api/beans", `{"shop_id":1,"name":"ブレンド","product_url":"https://example.com/bean"}`, 201)
	request(t, h, "POST", "/api/beans/1/variants", `{"weight_g":200,"price_yen":1600}`, 201)
	request(t, h, "POST", "/api/beans/1/variants", `{"weight_g":500,"price_yen":3600}`, 201)
	list := request(t, h, "GET", "/api/beans", "", 200)
	var data struct {
		Beans []struct {
			Status   string
			Variants []struct {
				Weight int64 `json:"weight_g"`
				Quote  struct {
					Total *int64 `json:"total_yen"`
				}
			}
		}
	}
	raw, _ := json.Marshal(list)
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Beans) != 1 || data.Beans[0].Status != "candidate" || len(data.Beans[0].Variants) != 2 {
		t.Fatalf("unexpected list: %s", raw)
	}
	variants := data.Beans[0].Variants
	if variants[0].Weight != 200 || variants[0].Quote.Total == nil || *variants[0].Quote.Total != 2200 || variants[1].Quote.Total == nil || *variants[1].Quote.Total != 4200 {
		t.Fatalf("incorrect comparison: %s", raw)
	}
	updated := request(t, h, "PATCH", "/api/variants/1", `{"version":1,"price_yen":1750}`, 200)
	if string(updated["version"]) != "2" || string(updated["price_yen"]) != "1750" {
		t.Fatalf("update %s", updated)
	}
	conflict := request(t, h, "PATCH", "/api/variants/1", `{"version":1,"price_yen":1}`, 409)
	var current struct {
		Price   int64 `json:"price_yen"`
		Version int64
	}
	if err := json.Unmarshal(conflict["current"], &current); err != nil {
		t.Fatal(err)
	}
	if current.Price != 1750 || current.Version != 2 {
		t.Fatalf("conflict lost current value: %s", conflict)
	}
}

func TestInvalidWritesDoNotChangePrice(t *testing.T) {
	h := testHandler(t)
	request(t, h, "POST", "/api/shops", `{"name":"店"}`, 201)
	request(t, h, "POST", "/api/beans", `{"shop_id":1,"name":"豆"}`, 201)
	request(t, h, "POST", "/api/beans/1/variants", `{"weight_g":200,"price_yen":1600}`, 201)
	for _, body := range []string{
		`{}`, `null`, `{"version":1}`, `{"version":null,"price_yen":0}`, `{"version":1,"price_yen":null}`,
		`{"version":0,"price_yen":0}`, `{"version":1,"price_yen":-1}`, `{"version":1,"price_yen":1000001}`,
		`{"version":1,"price_yen":1.5}`, `{"version":1,"price_yen":"100"}`, `{"version":1,"price_yen":0,"extra":1}`,
		`{"version":1,"price_yen":0} {}`, `{`,
	} {
		request(t, h, "PATCH", "/api/variants/1", body, 400)
	}
	// A valid zero is distinct from a missing or null price, and none of the
	// rejected writes consumed version 1.
	request(t, h, "PATCH", "/api/variants/1", `{"version":1,"price_yen":0}`, 200)
	request(t, h, "PATCH", "/api/variants/999", `{"version":1,"price_yen":100}`, 404)
	request(t, h, "PATCH", "/api/variants/nope", `{"version":1,"price_yen":100}`, 400)
	request(t, h, "POST", "/api/beans/999/variants", `{"weight_g":200,"price_yen":1600}`, 404)
}

func TestRequestBoundaries(t *testing.T) {
	h := testHandler(t)
	for _, tt := range []struct {
		name, contentType, origin, site, body string
		status                                int
	}{
		{"wrong media", "text/plain", "", "", `{"name":"shop"}`, 415},
		{"missing media", "", "", "", `{"name":"shop"}`, 415},
		{"cross origin", "application/json", "https://evil.example", "", `{"name":"shop"}`, 403},
		{"cross site", "application/json", "", "cross-site", `{"name":"shop"}`, 403},
		{"too large", "application/json", "", "", `{"name":"` + strings.Repeat("x", 65536) + `"}`, 413},
		{"invalid utf8", "application/json", "", "", "{\"name\":\"\xff\"}", 400},
		{"blank name", "application/json", "", "", `{"name":"\t\n"}`, 400},
		{"long name", "application/json", "", "", `{"name":"x` + strings.Repeat(" ", 200) + `"}`, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://example.com/api/shops", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", tt.contentType)
			r.Header.Set("Origin", tt.origin)
			r.Header.Set("Sec-Fetch-Site", tt.site)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tt.status {
				t.Fatalf("%d want %d: %s", w.Code, tt.status, w.Body.String())
			}
		})
	}
	// All rejected requests must leave the database empty.
	out := request(t, h, "GET", "/api/shops", "", 200)
	if string(out["shops"]) != "[]" {
		t.Fatalf("unexpected writes: %s", out)
	}
	r := httptest.NewRequest("POST", "http://example.com/api/shops", strings.NewReader(`{"name":"same origin"}`))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	r.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("same origin rejected: %d %s", w.Code, w.Body.String())
	}
}

func TestEmptyBeansAndShippingQuotes(t *testing.T) {
	h := testHandler(t)
	empty := request(t, h, "GET", "/api/beans", "", 200)
	if string(empty["beans"]) != "[]" {
		t.Fatal(empty)
	}
	request(t, h, "POST", "/api/shops", `{"name":"店","free_shipping_threshold_yen":4000}`, 201)
	request(t, h, "POST", "/api/beans", `{"shop_id":1,"name":"豆"}`, 201)
	list := request(t, h, "GET", "/api/beans", "", 200)
	if !bytes.Contains(list["beans"], []byte(`"variants":[]`)) {
		t.Fatal(list)
	}
	request(t, h, "POST", "/api/beans/1/variants", `{"weight_g":500,"price_yen":4000}`, 201)
	request(t, h, "POST", "/api/beans/1/variants", `{"weight_g":200,"price_yen":1600}`, 201)
	list = request(t, h, "GET", "/api/beans", "", 200)
	var beans []struct {
		Variants []struct {
			Weight int64 `json:"weight_g"`
			Quote  struct {
				Shipping *int64 `json:"shipping_yen"`
				Total    *int64 `json:"total_yen"`
			}
		}
	}
	if err := json.Unmarshal(list["beans"], &beans); err != nil {
		t.Fatal(err)
	}
	variants := beans[0].Variants
	if len(variants) != 2 || variants[0].Weight != 200 || variants[0].Quote.Total != nil || variants[0].Quote.Shipping != nil || variants[1].Quote.Total == nil || *variants[1].Quote.Total != 4000 || variants[1].Quote.Shipping == nil || *variants[1].Quote.Shipping != 0 {
		t.Fatalf("quotes: %s", list)
	}
	request(t, h, "POST", "/api/beans/1/variants", `{"weight_g":200,"price_yen":0}`, 400)
	request(t, h, "POST", "/api/beans/1/variants", `{"weight_g":100}`, 400)
	for _, productURL := range []string{"javascript:alert(1)", "/relative", "https://user:pass@example.com"} {
		body, _ := json.Marshal(map[string]any{"shop_id": 1, "name": "invalid", "product_url": productURL})
		request(t, h, "POST", "/api/beans", string(body), 400)
	}
}

func TestInternalErrorsAreSanitized(t *testing.T) {
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "private-path.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	h := New(s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	out := request(t, h, "GET", "/api/beans", "", 500)
	if string(out["error"]) != `"internal server error"` {
		t.Fatalf("leaked internal error: %s", out)
	}
}

func TestShopPartialUpdateAndConflict(t *testing.T) {
	h := testHandler(t)
	request(t, h, "POST", "/api/shops", `{"name":"旧店名","shipping_yen":600,"free_shipping_threshold_yen":4000}`, 201)
	updated := request(t, h, "PATCH", "/api/shops/1", `{"version":1,"name":"新店名"}`, 200)
	if string(updated["shipping_yen"]) != "600" || string(updated["free_shipping_threshold_yen"]) != "4000" || string(updated["version"]) != "2" {
		t.Fatalf("omitted fields lost: %s", updated)
	}
	updated = request(t, h, "PATCH", "/api/shops/1", `{"version":2,"shipping_yen":0,"free_shipping_threshold_yen":null}`, 200)
	if string(updated["shipping_yen"]) != "0" || string(updated["free_shipping_threshold_yen"]) != "null" {
		t.Fatal(updated)
	}
	conflict := request(t, h, "PATCH", "/api/shops/1", `{"version":1,"shipping_yen":900}`, 409)
	var current struct {
		Version  int64
		Shipping *int64 `json:"shipping_yen"`
	}
	if err := json.Unmarshal(conflict["current"], &current); err != nil {
		t.Fatal(err)
	}
	if current.Version != 3 || current.Shipping == nil || *current.Shipping != 0 {
		t.Fatal(current)
	}
	updated = request(t, h, "PATCH", "/api/shops/1", `{"version":3,"shipping_yen":null}`, 200)
	if string(updated["shipping_yen"]) != "null" {
		t.Fatal(updated)
	}
}

func TestBeanAndVariantEditing(t *testing.T) {
	h := testHandler(t)
	request(t, h, "POST", "/api/shops", `{"name":"店"}`, 201)
	request(t, h, "POST", "/api/beans", `{"shop_id":1,"name":"豆","product_url":"https://example.com/a"}`, 201)
	updated := request(t, h, "PATCH", "/api/beans/1", `{"version":1,"status":"excluded"}`, 200)
	if string(updated["status"]) != `"excluded"` || string(updated["name"]) != `"豆"` || string(updated["product_url"]) != `"https://example.com/a"` {
		t.Fatal(updated)
	}
	updated = request(t, h, "PATCH", "/api/beans/1", `{"version":2,"name":"別の豆","product_url":null}`, 200)
	if string(updated["product_url"]) != "null" || string(updated["status"]) != `"excluded"` {
		t.Fatal(updated)
	}
	request(t, h, "PATCH", "/api/beans/1", `{"version":1,"name":"古い変更"}`, 409)
	request(t, h, "PATCH", "/api/beans/1", `{"version":3,"status":"invalid"}`, 400)
	request(t, h, "POST", "/api/beans/1/variants", `{"weight_g":200,"price_yen":1600}`, 201)
	request(t, h, "POST", "/api/beans/1/variants", `{"weight_g":500,"price_yen":3600}`, 201)
	updated = request(t, h, "PATCH", "/api/variants/1", `{"version":1,"weight_g":250}`, 200)
	if string(updated["weight_g"]) != "250" || string(updated["price_yen"]) != "1600" {
		t.Fatal(updated)
	}
	request(t, h, "PATCH", "/api/variants/1", `{"version":2,"weight_g":500}`, 400)
	updated = request(t, h, "PATCH", "/api/variants/1", `{"version":2,"weight_g":300,"price_yen":2000}`, 200)
	if string(updated["version"]) != "3" || string(updated["price_yen"]) != "2000" {
		t.Fatal(updated)
	}
	request(t, h, "PATCH", "/api/variants/1", `{"version":2,"price_yen":1}`, 409)
}

func TestVersionedDeletionAndIntegrity(t *testing.T) {
	h := testHandler(t)
	request(t, h, "POST", "/api/shops", `{"name":"店"}`, 201)
	request(t, h, "POST", "/api/beans", `{"shop_id":1,"name":"豆"}`, 201)
	request(t, h, "POST", "/api/beans/1/variants", `{"weight_g":200,"price_yen":1600}`, 201)
	request(t, h, "DELETE", "/api/shops/1", `{"version":1}`, 409)
	request(t, h, "DELETE", "/api/variants/1", `{"version":2}`, 409)
	request(t, h, "DELETE", "/api/variants/1", `{}`, 400)
	// The bean's original version is stale after adding a capacity.
	request(t, h, "DELETE", "/api/beans/1", `{"version":1}`, 409)
	request(t, h, "DELETE", "/api/variants/1", `{"version":1}`, 200)
	request(t, h, "DELETE", "/api/variants/1", `{"version":1}`, 404)
	request(t, h, "POST", "/api/beans/1/variants", `{"weight_g":500,"price_yen":3600}`, 201)
	request(t, h, "PATCH", "/api/variants/2", `{"version":1,"price_yen":3800}`, 200)
	request(t, h, "DELETE", "/api/beans/1", `{"version":4}`, 409)
	// insert, delete, insert, update each incremented the parent version.
	request(t, h, "DELETE", "/api/beans/1", `{"version":5}`, 200)
	request(t, h, "PATCH", "/api/variants/2", `{"version":2,"price_yen":1}`, 404)
	request(t, h, "DELETE", "/api/shops/1", `{"version":1}`, 200)
	list := request(t, h, "GET", "/api/beans", "", 200)
	if string(list["beans"]) != "[]" {
		t.Fatal(list)
	}
	request(t, h, "DELETE", "/api/beans/999", `{"version":1}`, 404)
	request(t, h, "DELETE", "/api/shops/999", `{"version":1}`, 404)
}

func TestInvalidEditsKeepDataAndVersion(t *testing.T) {
	h := testHandler(t)
	request(t, h, "POST", "/api/shops", `{"name":"店","shipping_yen":600}`, 201)
	request(t, h, "POST", "/api/beans", `{"shop_id":1,"name":"豆"}`, 201)
	request(t, h, "POST", "/api/beans/1/variants", `{"weight_g":200,"price_yen":1600}`, 201)
	for _, tt := range []struct{ path, body string }{
		{"/api/shops/1", `{"version":1,"name":null}`},
		{"/api/shops/1", `{"version":1,"shipping_yen":-1}`},
		{"/api/shops/1", `{"version":1}`},
		{"/api/shops/1", `{"version":1,"free_shipping_threshold_yen":1000001}`},
		{"/api/beans/1", `{"version":2,"status":null}`},
		{"/api/beans/1", `{"version":2,"status":"invalid"}`},
		{"/api/beans/1", `{"version":2,"product_url":"javascript:alert(1)"}`},
		{"/api/beans/1", `{"version":2,"name":"\t\n"}`},
		{"/api/beans/1", `{"version":2,"shop_id":999}`},
		{"/api/variants/1", `{"version":1,"weight_g":null}`},
		{"/api/variants/1", `{"version":1,"weight_g":0}`},
		{"/api/variants/1", `{"version":1,"weight_g":1000001}`},
	} {
		request(t, h, "PATCH", tt.path, tt.body, 400)
	}
	request(t, h, "PATCH", "/api/shops/1", `{"version":1,"shipping_yen":0}`, 200)
	// The invalid writes must not invalidate the parent's version.
	request(t, h, "PATCH", "/api/beans/1", `{"version":2,"status":"excluded"}`, 200)
	list := request(t, h, "GET", "/api/beans", "", 200)
	var beans []struct {
		Status   string
		Variants []struct {
			Version int64
			Quote   struct {
				Total *int64 `json:"total_yen"`
			}
		}
	}
	if err := json.Unmarshal(list["beans"], &beans); err != nil {
		t.Fatal(err)
	}
	if beans[0].Status != "excluded" || beans[0].Variants[0].Version != 1 || beans[0].Variants[0].Quote.Total == nil || *beans[0].Variants[0].Quote.Total != 1600 {
		t.Fatal(list)
	}
	for _, path := range []string{"/api/shops/999", "/api/beans/999"} {
		request(t, h, "PATCH", path, `{"version":1,"name":"新しい名前"}`, 404)
	}
}

func TestStaticUIAndAPIRoutesStaySeparate(t *testing.T) {
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "coffee.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := NewWithAssets(s, fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>Coffee</title>")}, "assets/app.js": {Data: []byte("console.log('coffee')")}})
	for _, tt := range []struct {
		path     string
		status   int
		contains string
	}{
		{"/", 200, "Coffee"}, {"/assets/app.js", 200, "console.log"}, {"/api/missing", 404, "404"}, {"/private.sqlite", 404, "404"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", tt.path, nil))
		if w.Code != tt.status || !strings.Contains(w.Body.String(), tt.contains) {
			t.Fatalf("%s: %d %s", tt.path, w.Code, w.Body.String())
		}
	}
}

func TestHealthRejectsUnavailableDatabase(t *testing.T) {
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "health.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	h := New(s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 503 || strings.Contains(w.Body.String(), "sql:") {
		t.Fatalf("health response %d %s", w.Code, w.Body.String())
	}
}

func TestCandidateAndBuyAgainAreIndependent(t *testing.T) {
	h := testHandler(t)
	request(t, h, "POST", "/api/shops", `{"name":"店"}`, 201)
	created := request(t, h, "POST", "/api/beans", `{"shop_id":1,"name":"豆"}`, 201)
	if string(created["status"]) != `"candidate"` || string(created["buy_again"]) != "false" {
		t.Fatalf("defaults: %s", created)
	}
	updated := request(t, h, "PATCH", "/api/beans/1", `{"version":1,"buy_again":true}`, 200)
	if string(updated["buy_again"]) != "true" || string(updated["status"]) != `"candidate"` {
		t.Fatalf("like: %s", updated)
	}
	updated = request(t, h, "PATCH", "/api/beans/1", `{"version":2,"status":"excluded"}`, 200)
	if string(updated["buy_again"]) != "true" {
		t.Fatal("excluding cleared like")
	}
	stale := request(t, h, "PATCH", "/api/beans/1", `{"version":2,"buy_again":false}`, 409)
	if !strings.Contains(string(stale["current"]), `"buy_again":true`) {
		t.Fatal("conflict omits current like")
	}
	request(t, h, "PATCH", "/api/beans/1", `{"version":3,"buy_again":null}`, 400)
	request(t, h, "PATCH", "/api/beans/1", `{"version":3,"buy_again":"true"}`, 400)
	request(t, h, "PATCH", "/api/beans/1", `{"version":3,"status":"purchased"}`, 400)
	updated = request(t, h, "PATCH", "/api/beans/1", `{"version":3,"buy_again":false}`, 200)
	if string(updated["status"]) != `"excluded"` {
		t.Fatal("unlike restored candidate")
	}
	updated = request(t, h, "PATCH", "/api/beans/1", `{"version":4,"status":"candidate"}`, 200)
	if string(updated["buy_again"]) != "false" {
		t.Fatal("candidate changed like")
	}
	listed := request(t, h, "GET", "/api/beans", "", 200)
	if !strings.Contains(string(listed["beans"]), `"buy_again":false`) {
		t.Fatal("list omits like")
	}
}
