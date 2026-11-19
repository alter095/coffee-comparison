package store

import (
	"context"
	"database/sql"
)

type Shop struct {
	ID                       int64  `json:"id"`
	Name                     string `json:"name"`
	ShippingYen              *int64 `json:"shipping_yen"`
	FreeShippingThresholdYen *int64 `json:"free_shipping_threshold_yen"`
	Version                  int64  `json:"version"`
}

type Bean struct {
	ID         int64     `json:"id"`
	ShopID     int64     `json:"shop_id"`
	Name       string    `json:"name"`
	ProductURL *string   `json:"product_url"`
	Status     string    `json:"status"`
	BuyAgain   bool      `json:"buy_again"`
	Version    int64     `json:"version"`
	Shop       Shop      `json:"shop"`
	Variants   []Variant `json:"variants"`
}

func (s *Store) Shops(ctx context.Context) ([]Shop, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,shipping_yen,free_shipping_threshold_yen,version FROM shops ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	shops := make([]Shop, 0)
	for rows.Next() {
		var shop Shop
		if err := rows.Scan(&shop.ID, &shop.Name, &shop.ShippingYen, &shop.FreeShippingThresholdYen, &shop.Version); err != nil {
			return nil, err
		}
		shops = append(shops, shop)
	}
	return shops, rows.Err()
}

// Beans reads one consistent SQLite statement snapshot, including beans with
// no sizes. It avoids issuing a query per bean or holding nested connections.
func (s *Store) Beans(ctx context.Context) ([]Bean, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT b.id,b.shop_id,b.name,b.product_url,b.status,b.buy_again,b.version,
 s.id,s.name,s.shipping_yen,s.free_shipping_threshold_yen,s.version,
 v.id,v.weight_g,v.price_yen,v.version
 FROM beans b JOIN shops s ON s.id=b.shop_id LEFT JOIN variants v ON v.bean_id=b.id
 ORDER BY b.id,v.weight_g,v.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	beans := make([]Bean, 0)
	for rows.Next() {
		var b Bean
		var id, weight, price, version sql.NullInt64
		if err := rows.Scan(&b.ID, &b.ShopID, &b.Name, &b.ProductURL, &b.Status, &b.BuyAgain, &b.Version,
			&b.Shop.ID, &b.Shop.Name, &b.Shop.ShippingYen, &b.Shop.FreeShippingThresholdYen, &b.Shop.Version,
			&id, &weight, &price, &version); err != nil {
			return nil, err
		}
		if len(beans) == 0 || beans[len(beans)-1].ID != b.ID {
			b.Variants = make([]Variant, 0)
			beans = append(beans, b)
		}
		if id.Valid {
			last := &beans[len(beans)-1]
			last.Variants = append(last.Variants, Variant{ID: id.Int64, BeanID: b.ID, WeightG: weight.Int64, PriceYen: price.Int64, Version: version.Int64})
		}
	}
	return beans, rows.Err()
}
