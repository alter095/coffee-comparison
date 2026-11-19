package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/alter095/coffee-comparison/internal/comparison"
)

func (s *Store) Shop(ctx context.Context, id int64) (Shop, error) {
	var v Shop
	err := s.db.QueryRowContext(ctx, `SELECT id,name,shipping_yen,free_shipping_threshold_yen,version FROM shops WHERE id=?`, id).Scan(&v.ID, &v.Name, &v.ShippingYen, &v.FreeShippingThresholdYen, &v.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

// UpdateShop applies a complete validated snapshot only if its version matches.
func (s *Store) UpdateShop(ctx context.Context, v Shop) (Shop, error) {
	if v.Version <= 0 || !validName(v.Name) {
		return Shop{}, ErrInvalid
	}
	var updated Shop
	err := s.db.QueryRowContext(ctx, `UPDATE shops SET name=?,shipping_yen=?,free_shipping_threshold_yen=?,version=version+1,updated_at=CURRENT_TIMESTAMP WHERE id=? AND version=? RETURNING id,name,shipping_yen,free_shipping_threshold_yen,version`, v.Name, v.ShippingYen, v.FreeShippingThresholdYen, v.ID, v.Version).Scan(&updated.ID, &updated.Name, &updated.ShippingYen, &updated.FreeShippingThresholdYen, &updated.Version)
	if errors.Is(err, sql.ErrNoRows) {
		current, err := s.Shop(ctx, v.ID)
		if err != nil {
			return current, err
		}
		return current, ErrConflict
	}
	return updated, classify(err)
}

func (s *Store) Bean(ctx context.Context, id int64) (Bean, error) {
	var v Bean
	err := s.db.QueryRowContext(ctx, `SELECT id,shop_id,name,product_url,status,buy_again,version FROM beans WHERE id=?`, id).Scan(&v.ID, &v.ShopID, &v.Name, &v.ProductURL, &v.Status, &v.BuyAgain, &v.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

// UpdateBean edits metadata without moving a bean to a different shop.
func (s *Store) UpdateBean(ctx context.Context, v Bean) (Bean, error) {
	if v.Version <= 0 || !validName(v.Name) {
		return Bean{}, ErrInvalid
	}
	var updated Bean
	err := s.db.QueryRowContext(ctx, `UPDATE beans SET name=?,product_url=?,status=?,buy_again=?,version=version+1,updated_at=CURRENT_TIMESTAMP WHERE id=? AND version=? RETURNING id,shop_id,name,product_url,status,buy_again,version`, v.Name, v.ProductURL, v.Status, v.BuyAgain, v.ID, v.Version).Scan(&updated.ID, &updated.ShopID, &updated.Name, &updated.ProductURL, &updated.Status, &updated.BuyAgain, &updated.Version)
	if errors.Is(err, sql.ErrNoRows) {
		current, err := s.Bean(ctx, v.ID)
		if err != nil {
			return current, err
		}
		return current, ErrConflict
	}
	return updated, classify(err)
}

func (s *Store) UpdateVariant(ctx context.Context, v Variant) (Variant, error) {
	if v.Version <= 0 {
		return Variant{}, ErrInvalid
	}
	if _, err := comparison.Online(v.PriceYen, v.WeightG, nil, nil); err != nil {
		return Variant{}, ErrInvalid
	}
	var updated Variant
	err := s.db.QueryRowContext(ctx, `UPDATE variants SET weight_g=?,price_yen=?,version=version+1,updated_at=CURRENT_TIMESTAMP WHERE id=? AND version=? RETURNING id,bean_id,weight_g,price_yen,version`, v.WeightG, v.PriceYen, v.ID, v.Version).Scan(&updated.ID, &updated.BeanID, &updated.WeightG, &updated.PriceYen, &updated.Version)
	if errors.Is(err, sql.ErrNoRows) {
		current, err := s.Variant(ctx, v.ID)
		if err != nil {
			return current, err
		}
		return current, ErrConflict
	}
	return updated, classify(err)
}
