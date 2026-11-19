package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alter095/coffee-comparison/internal/store"
)

func TestLiveSnapshotRestoresCommittedWALAndRejectsOverwrite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.sqlite")
	live, err := store.Open(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	shop, err := live.CreateShop(ctx, "backup shop", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	bean, err := live.CreateBean(ctx, shop, "backup bean", nil)
	if err != nil {
		t.Fatal(err)
	}
	variant, err := live.CreateVariant(ctx, bean, 200, 1600)
	if err != nil {
		t.Fatal(err)
	}
	b, err := live.Bean(ctx, bean)
	if err != nil {
		t.Fatal(err)
	}
	b.Status = "excluded"
	b.BuyAgain = true
	if _, err := live.UpdateBean(ctx, b); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(dir, "snapshot.sqlite")
	if err := Snapshot(ctx, source, snapshot); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(snapshot); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private snapshot: %v %v", info, err)
	}
	if _, err := live.UpdatePrice(ctx, variant.ID, variant.Version, 1800); err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(ctx, source, snapshot); !errors.Is(err, os.ErrExist) {
		t.Fatalf("overwrite allowed: %v", err)
	}
	restored := filepath.Join(dir, "restored.sqlite")
	if err := Snapshot(ctx, snapshot, restored); err != nil {
		t.Fatal(err)
	}
	restoredStore, err := store.Open(ctx, restored)
	if err != nil {
		t.Fatal(err)
	}
	defer restoredStore.Close()
	restoredBean, err := restoredStore.Bean(ctx, bean)
	if err != nil || restoredBean.Status != "excluded" || !restoredBean.BuyAgain {
		t.Fatalf("lost flags: %+v %v", restoredBean, err)
	}
	got, err := restoredStore.Variant(ctx, variant.ID)
	if err != nil || got.PriceYen != 1600 {
		t.Fatalf("snapshot lost WAL state: %+v %v", got, err)
	}
	if _, err := restoredStore.UpdatePrice(ctx, got.ID, got.Version, 1900); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotFailureDoesNotPublishOrCreateSource(t *testing.T) {
	for _, scenario := range []string{"missing", "corrupt", "canceled", "sidecar"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			source, dest := filepath.Join(dir, "source.sqlite"), filepath.Join(dir, "dest.sqlite")
			switch scenario {
			case "corrupt":
				if err := os.WriteFile(source, []byte("not a database"), 0600); err != nil {
					t.Fatal(err)
				}
			case "canceled", "sidecar":
				s, err := store.Open(ctx, source)
				if err != nil {
					t.Fatal(err)
				}
				s.Close()
				if scenario == "canceled" {
					c, cancel := context.WithCancel(ctx)
					cancel()
					ctx = c
				} else {
					if err := os.WriteFile(dest+"-wal", []byte("old"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := Snapshot(ctx, source, dest); err == nil {
				t.Fatal("expected failure")
			}
			if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("published failed snapshot: %v", err)
			}
			if scenario == "missing" {
				if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("source was created")
				}
			}
		})
	}
}
