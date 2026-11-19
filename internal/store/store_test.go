package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func seed(t *testing.T, s *Store) Variant {
	t.Helper()
	ctx := context.Background()
	shop, err := s.CreateShop(ctx, "焙煎店", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	bean, err := s.CreateBean(ctx, shop, "ブレンド", nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.CreateVariant(ctx, bean, 200, 1600)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func openTest(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func TestPersistenceAndMigrationIdempotence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "coffee.sqlite")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	v := seed(t, s)
	saved, err := s.UpdatePrice(ctx, v.ID, v.Version, 1750)
	if err != nil || saved.Version != 2 {
		t.Fatalf("%+v %v", saved, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTest(t, path)
	got, err := reopened.Variant(ctx, v.ID)
	if err != nil || got != saved {
		t.Fatalf("got %+v, want %+v: %v", got, saved, err)
	}
	var migrations int
	if err := reopened.db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&migrations); err != nil || migrations != 4 {
		t.Fatalf("migrations=%d: %v", migrations, err)
	}
	var journal string
	if err := reopened.db.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil || journal != "wal" {
		t.Fatalf("journal=%s: %v", journal, err)
	}
}

func TestConcurrentUpdatesAcrossConnections(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "coffee.sqlite")
	first := openTest(t, path)
	original := seed(t, first)
	second := openTest(t, path)
	start := make(chan struct{})
	type outcome struct {
		v   Variant
		err error
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i, s := range []*Store{first, second} {
		wg.Add(1)
		go func(s *Store, price int64) {
			defer wg.Done()
			<-start
			v, err := s.UpdatePrice(ctx, original.ID, original.Version, price)
			results <- outcome{v, err}
		}(s, int64(1700+i*100))
	}
	close(start)
	wg.Wait()
	close(results)
	var successes, conflicts int
	for r := range results {
		switch {
		case r.err == nil:
			successes++
		case errors.Is(r.err, ErrConflict):
			conflicts++
		default:
			t.Fatal(r.err)
		}
		if r.v.Version != 2 {
			t.Fatalf("unexpected version %+v", r)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	current, err := first.Variant(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := second.UpdatePrice(ctx, original.ID, original.Version, 1)
	if !errors.Is(err, ErrConflict) || stale != current {
		t.Fatalf("stale %+v current %+v: %v", stale, current, err)
	}
}

func TestConstraintsAndDeleteIntegrity(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "coffee.sqlite"))
	v := seed(t, s)
	for _, query := range []string{
		`INSERT INTO shops(name) VALUES(' ')`,
		`INSERT INTO shops(name,shipping_yen) VALUES('x',-1)`,
		`INSERT INTO shops(name,free_shipping_threshold_yen) VALUES('x',1000001)`,
		`INSERT INTO beans(shop_id,name) VALUES(999,'x')`,
		`UPDATE beans SET status='invalid'`,
		`UPDATE variants SET weight_g=0`,
		`UPDATE variants SET price_yen=1000001`,
		`INSERT INTO variants(bean_id,weight_g,price_yen) VALUES(1,200,100)`,
		`DELETE FROM shops WHERE id=1`,
	} {
		if _, err := s.db.ExecContext(ctx, query); !errors.Is(classify(err), ErrInvalid) {
			t.Fatalf("query %s: %v", query, err)
		}
	}
	if _, err := s.CreateVariant(ctx, v.BeanID, 200, 1800); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.CreateVariant(ctx, v.BeanID, 0, 100); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.UpdatePrice(ctx, v.ID, 0, 100); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.UpdatePrice(ctx, 999, 1, 100); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM beans WHERE id=?`, v.BeanID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Variant(ctx, v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestNullShippingAndExplicitZero(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "coffee.sqlite"))
	zero := int64(0)
	for _, shipping := range []*int64{nil, &zero} {
		id, err := s.CreateShop(ctx, "shop", shipping, nil)
		if err != nil {
			t.Fatal(err)
		}
		var got *int64
		if err := s.db.QueryRowContext(ctx, `SELECT shipping_yen FROM shops WHERE id=?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if (got == nil) != (shipping == nil) || (got != nil && *got != *shipping) {
			t.Fatalf("shipping changed: %v", got)
		}
	}
}

func TestNewerSchemaRejected(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "coffee.sqlite")
	s := openTest(t, path)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES(5)`); err != nil {
		t.Fatal(err)
	}
	other, err := Open(ctx, path)
	if err == nil {
		other.Close()
		t.Fatal("accepted newer schema")
	}
}

func TestForeignKeysSurviveConnectionReplacement(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "coffee.sqlite"))
	s.db.SetMaxIdleConns(0)
	if _, err := s.CreateBean(ctx, 999, "orphan", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("foreign keys disabled on replacement connection: %v", err)
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "coffee.sqlite")
	s := openTest(t, path)
	// Simulate an inconsistent pre-migration database that causes the initial
	// script to fail after creating shops, to verify transactional DDL rollback.
	if _, err := s.db.ExecContext(ctx, `DROP TABLE variants; DROP TABLE beans; DROP TABLE shops; DELETE FROM schema_migrations; CREATE TABLE beans(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err == nil {
		t.Fatal("expected migration failure")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name='shops'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial migration retained: %d %v", count, err)
	}
}

func TestDeletedIDsAreNotReused(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "coffee.sqlite")
	s := openTest(t, path)
	old := seed(t, s)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM beans WHERE id=?`, old.BeanID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM shops`); err != nil {
		t.Fatal(err)
	}
	replacement := seed(t, s)
	if replacement.ID <= old.ID || replacement.BeanID <= old.BeanID {
		t.Fatalf("IDs reused: old=%+v new=%+v", old, replacement)
	}
	if _, err := s.UpdatePrice(ctx, old.ID, old.Version, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale request: %v", err)
	}
	got, err := s.Variant(ctx, replacement.ID)
	if err != nil || got != replacement {
		t.Fatalf("replacement changed: %+v %v", got, err)
	}
	var shopID int64
	if err := s.db.QueryRow(`SELECT shop_id FROM beans WHERE id=?`, replacement.BeanID).Scan(&shopID); err != nil || shopID <= 1 {
		t.Fatalf("shop ID reused: %d %v", shopID, err)
	}
}

func TestNameValidation(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "coffee.sqlite"))
	shop, err := s.CreateShop(ctx, "店舗", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "\t\n", "\u3000\u00a0", "x" + strings.Repeat(" ", 200), strings.Repeat("豆", 201), "a\x00b", string([]byte{0xff})} {
		if _, err := s.CreateShop(ctx, name, nil, nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("shop accepted %q: %v", name, err)
		}
		if _, err := s.CreateBean(ctx, shop, name, nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("bean accepted %q: %v", name, err)
		}
	}
	for _, name := range []string{"豆", strings.Repeat("豆", 200), " 豆 "} {
		if _, err := s.CreateShop(ctx, name, nil, nil); err != nil {
			t.Errorf("shop rejected valid name: %v", err)
		}
		if _, err := s.CreateBean(ctx, shop, name, nil); err != nil {
			t.Errorf("bean rejected valid name: %v", err)
		}
	}
}

func legacyDB(t *testing.T, path, name string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	script, err := migrations.ReadFile("migrations/001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(script)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY) STRICT; INSERT INTO schema_migrations VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO shops(id,name,shipping_yen,version) VALUES(7,?,0,3)`, name); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO beans(id,shop_id,name,status,version) VALUES(12,7,'豆','purchased',4); INSERT INTO variants(id,bean_id,weight_g,price_yen,version) VALUES(20,12,500,3600,5)`); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationPreservesDataAndStableIDs(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "coffee.sqlite")
	legacyDB(t, path, "店舗")
	s := openTest(t, path)
	v, err := s.Variant(ctx, 20)
	if err != nil || v != (Variant{ID: 20, BeanID: 12, WeightG: 500, PriceYen: 3600, Version: 5}) {
		t.Fatalf("data changed: %+v %v", v, err)
	}
	var status string
	var shopVersion, beanVersion int64
	var shipping *int64
	if err := s.db.QueryRow(`SELECT b.status,s.version,b.version,s.shipping_yen FROM beans b JOIN shops s ON s.id=b.shop_id`).Scan(&status, &shopVersion, &beanVersion, &shipping); err != nil {
		t.Fatal(err)
	}
	if status != "candidate" || shopVersion != 3 || beanVersion != 5 || shipping == nil || *shipping != 0 {
		t.Fatal("related data changed")
	}
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Error("broken foreign key after migration")
	}
	if err := rows.Err(); err != nil {
		t.Error(err)
	}
	rows.Close()
	if _, err := s.db.Exec(`DELETE FROM beans; DELETE FROM shops`); err != nil {
		t.Fatal(err)
	}
	// Open another connection after deletion to ensure the persistent sequence,
	// rather than only the maximum live row ID, protects old requests.
	other := openTest(t, path)
	replacement := seed(t, other)
	if replacement.ID <= 20 || replacement.BeanID <= 12 {
		t.Fatalf("historical IDs reused: %+v", replacement)
	}
	var shopID int64
	if err := other.db.QueryRow(`SELECT shop_id FROM beans WHERE id=?`, replacement.BeanID).Scan(&shopID); err != nil || shopID <= 7 {
		t.Fatalf("shop ID: %d %v", shopID, err)
	}
	if _, err := other.UpdatePrice(ctx, 20, 5, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale request: %v", err)
	}
}

func TestMigrationRejectsInvalidLegacyNamesWithoutDataLoss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coffee.sqlite")
	legacyDB(t, path, "\t\n")
	s, err := Open(context.Background(), path)
	if err == nil {
		s.Close()
		t.Fatal("migration accepted invalid legacy name")
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, price int
	if err := db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil || version != 1 {
		t.Fatalf("version %d: %v", version, err)
	}
	if err := db.QueryRow(`SELECT price_yen FROM variants WHERE id=20`).Scan(&price); err != nil || price != 3600 {
		t.Fatalf("data lost: %d %v", price, err)
	}
	var leftovers int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('shops_new','beans_new','variants_new')`).Scan(&leftovers); err != nil || leftovers != 0 {
		t.Fatalf("partial migration: %d %v", leftovers, err)
	}
}

func TestDatabaseNameConstraints(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "coffee.sqlite"))
	shop, err := s.CreateShop(context.Background(), "店舗", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"\t\n", "\u3000\u00a0", "x" + strings.Repeat(" ", 200), "a\x00b"} {
		if _, err := s.db.Exec(`INSERT INTO shops(name) VALUES(?)`, name); !errors.Is(classify(err), ErrInvalid) {
			t.Errorf("DB shop accepted %q: %v", name, err)
		}
		if _, err := s.db.Exec(`INSERT INTO beans(shop_id,name) VALUES(?,?)`, shop, name); !errors.Is(classify(err), ErrInvalid) {
			t.Errorf("DB bean accepted %q: %v", name, err)
		}
	}
}

func TestMigrationRejectsInvalidUTF8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coffee.sqlite")
	legacyDB(t, path, string([]byte{0xff}))
	s, err := Open(context.Background(), path)
	if err == nil {
		s.Close()
		t.Fatal("migration accepted invalid UTF-8")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBeanDeleteRejectsStaleChildSnapshot(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "coffee.sqlite"))
	v := seed(t, s)
	before, err := s.Bean(ctx, v.BeanID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdatePrice(ctx, v.ID, v.Version, 1750); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBean(ctx, before.ID, before.Version); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale parent deletion: %v", err)
	}
	got, err := s.Variant(ctx, v.ID)
	if err != nil || got.PriceYen != 1750 {
		t.Fatalf("updated child lost: %+v %v", got, err)
	}
	after, err := s.Bean(ctx, v.BeanID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBean(ctx, after.ID, after.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Variant(ctx, v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("child survived deletion: %v", err)
	}
}

func TestConcurrentBeanDeletionAndPriceUpdate(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "coffee.sqlite")
	first := openTest(t, path)
	v := seed(t, first)
	bean, err := first.Bean(ctx, v.BeanID)
	if err != nil {
		t.Fatal(err)
	}
	second := openTest(t, path)
	start := make(chan struct{})
	updated := make(chan error, 1)
	deleted := make(chan error, 1)
	go func() { <-start; _, err := first.UpdatePrice(ctx, v.ID, v.Version, 1750); updated <- err }()
	go func() { <-start; deleted <- second.DeleteBean(ctx, bean.ID, bean.Version) }()
	close(start)
	updateErr, deleteErr := <-updated, <-deleted
	switch {
	case updateErr == nil:
		if !errors.Is(deleteErr, ErrConflict) {
			t.Fatalf("successful update was deleted: %v", deleteErr)
		}
		got, err := first.Variant(ctx, v.ID)
		if err != nil || got.PriceYen != 1750 {
			t.Fatalf("updated value lost: %+v %v", got, err)
		}
	case deleteErr == nil:
		if !errors.Is(updateErr, ErrNotFound) {
			t.Fatalf("update after deletion: %v", updateErr)
		}
	default:
		t.Fatalf("unexpected outcomes: update=%v delete=%v", updateErr, deleteErr)
	}
}

func TestV2MigrationInstallsContentVersionTriggers(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "coffee.sqlite")
	legacyDB(t, path, "店")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	script, err := migrations.ReadFile("migrations/002_stable_ids_names.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(script)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations VALUES(2)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s := openTest(t, path)
	before, err := s.Bean(ctx, 12)
	if err != nil || before.Version != 5 {
		t.Fatalf("migration altered metadata: %+v %v", before, err)
	}
	if _, err := s.UpdatePrice(ctx, 20, 5, 3700); err != nil {
		t.Fatal(err)
	}
	after, err := s.Bean(ctx, 12)
	if err != nil || after.Version != 6 {
		t.Fatalf("trigger not installed: %+v %v", after, err)
	}
}

func TestSnapshotUpdatesRejectConcurrentChanges(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "coffee.sqlite"))
	v := seed(t, s)
	shop, err := s.Shop(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	changed := shop
	changed.Name = "new shop"
	if _, err := s.UpdateShop(ctx, changed); err != nil {
		t.Fatal(err)
	}
	shop.Name = "stale shop"
	actual, err := s.UpdateShop(ctx, shop)
	if !errors.Is(err, ErrConflict) || actual.Name != "new shop" {
		t.Fatalf("shop overwritten: %+v %v", actual, err)
	}
	bean, err := s.Bean(ctx, v.BeanID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdatePrice(ctx, v.ID, v.Version, 1750); err != nil {
		t.Fatal(err)
	}
	bean.Name = "stale bean"
	actualBean, err := s.UpdateBean(ctx, bean)
	if !errors.Is(err, ErrConflict) || actualBean.Name != "ブレンド" {
		t.Fatalf("bean overwritten: %+v %v", actualBean, err)
	}
	v.PriceYen = 1
	actualVariant, err := s.UpdateVariant(ctx, v)
	if !errors.Is(err, ErrConflict) || actualVariant.PriceYen != 1750 {
		t.Fatalf("variant overwritten: %+v %v", actualVariant, err)
	}
}

func TestV3MigrationResetsCandidateWithoutInferringBuyAgain(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	legacyDB(t, path, "店")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"002_stable_ids_names.sql", "003_bean_content_version.sql"} {
		script, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(script)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations VALUES(?)`, i+2); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO beans(id,shop_id,name,status,version) VALUES(13,7,'未購入','considering',8)`); err != nil {
		t.Fatal(err)
	}
	var created, updated string
	if err := db.QueryRow(`SELECT created_at,updated_at FROM beans WHERE id=12`).Scan(&created, &updated); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	beans, err := s.Beans(ctx)
	if err != nil || len(beans) != 2 {
		t.Fatalf("beans: %+v %v", beans, err)
	}
	for i, b := range beans {
		if b.Status != "candidate" || b.BuyAgain || b.ID != int64(12+i) {
			t.Fatalf("migration changed intent: %+v", b)
		}
	}
	if beans[0].Version != 5 || beans[1].Version != 9 || len(beans[0].Variants) != 1 || beans[0].Variants[0].ID != 20 || beans[0].Variants[0].PriceYen != 3600 {
		t.Fatalf("data: %+v", beans)
	}
	var legacy, gotCreated, gotUpdated string
	if err := s.db.QueryRow(`SELECT legacy_purchase_status,created_at,updated_at FROM beans WHERE id=12`).Scan(&legacy, &gotCreated, &gotUpdated); err != nil {
		t.Fatal(err)
	}
	if legacy != "purchased" || gotCreated != created || gotUpdated != updated {
		t.Fatal("lost legacy data or timestamps")
	}
	b := beans[0]
	b.Status = "excluded"
	b.BuyAgain = true
	if _, err := s.UpdateBean(ctx, b); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened := openTest(t, path)
	b, err = reopened.Bean(ctx, 12)
	if err != nil || b.Status != "excluded" || !b.BuyAgain || b.Version != 6 {
		t.Fatalf("reopen lost flags / repeated migration: %+v %v", b, err)
	}
	if _, err := reopened.UpdatePrice(ctx, 20, 5, 3700); err != nil {
		t.Fatal(err)
	}
	b, err = reopened.Bean(ctx, 12)
	if err != nil || b.Version != 7 || !b.BuyAgain || b.Status != "excluded" {
		t.Fatalf("trigger: %+v %v", b, err)
	}
}
