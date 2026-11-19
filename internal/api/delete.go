package api

import (
	"errors"
	"net/http"

	"github.com/alter095/coffee-comparison/internal/store"
)

// kind comes only from the fixed route registration, never from SQL input.
func (a *api) deleteRecord(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var in struct {
			Version *int64 `json:"version"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Version == nil || *in.Version <= 0 {
			fail(w, 400, "positive version is required")
			return
		}
		var err error
		switch kind {
		case "shop":
			err = a.store.DeleteShop(r.Context(), id, *in.Version)
		case "bean":
			err = a.store.DeleteBean(r.Context(), id, *in.Version)
		case "variant":
			err = a.store.DeleteVariant(r.Context(), id, *in.Version)
		}
		if errors.Is(err, store.ErrInUse) {
			fail(w, 409, "shop still contains beans")
			return
		}
		if errors.Is(err, store.ErrConflict) {
			var current any
			switch kind {
			case "shop":
				current, err = a.store.Shop(r.Context(), id)
			case "bean":
				var bean store.Bean
				bean, err = a.store.Bean(r.Context(), id)
				current = beanRecord(bean)
			case "variant":
				current, err = a.store.Variant(r.Context(), id)
			}
			if err != nil {
				storeError(w, err)
				return
			}
			conflict(w, current)
			return
		}
		if err != nil {
			storeError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"deleted": true})
	}
}
