package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDevelopmentListenerIsLoopbackOnly(t *testing.T) {
	for _, tt := range []struct {
		address string
		valid   bool
	}{
		{"127.0.0.1:3180", true}, {"[::1]:3180", true}, {"127.0.0.1:0", true},
		{":3180", false}, {"0.0.0.0:3180", false}, {"[::]:3180", false},
		{"192.168.1.2:3180", false}, {"example.com:3180", false}, {"127.0.0.1", false},
	} {
		if got := validateAddress(tt.address) == nil; got != tt.valid {
			t.Errorf("%s: got valid=%v", tt.address, got)
		}
	}
}

func TestDevelopmentHostValidation(t *testing.T) {
	h := developmentHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, tt := range []struct {
		host   string
		status int
	}{
		{"127.0.0.1:3180", 204}, {"localhost:3180", 204}, {"[::1]:3180", 204},
		{"attacker.example:3180", 403}, {"192.168.1.1:3180", 403},
	} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:3180/healthz", nil)
		r.Host = tt.host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tt.status {
			t.Errorf("host %s: %d want %d", tt.host, w.Code, tt.status)
		}
	}
}

func TestContainerListenerRequiresExplicitProductionMode(t *testing.T) {
	for _, tt := range []struct {
		address, mode    string
		container, valid bool
	}{
		{"0.0.0.0:3180", "production", true, true},
		{"[::]:3180", "production", true, true},
		{"0.0.0.0:3180", "production", false, false},
		{"0.0.0.0:3180", "development", true, false},
		{"127.0.0.1:3180", "development", true, false},
		{"192.168.1.2:3180", "production", true, false},
		{":3180", "production", true, false},
	} {
		if got := validateListener(tt.address, tt.mode, tt.container) == nil; got != tt.valid {
			t.Errorf("%+v: valid=%v", tt, got)
		}
	}
}
