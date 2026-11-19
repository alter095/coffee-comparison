package store

import (
	"context"
	"errors"

	"github.com/mattn/go-sqlite3"
)

var ErrInUse = errors.New("shop still contains beans")

func (s *Store) DeleteShop(ctx context.Context, id, version int64) error {
	return s.deleteVersioned(ctx, `DELETE FROM shops WHERE id=? AND version=?`, id, version, func() error { _, err := s.Shop(ctx, id); return err })
}

func (s *Store) DeleteBean(ctx context.Context, id, version int64) error {
	return s.deleteVersioned(ctx, `DELETE FROM beans WHERE id=? AND version=?`, id, version, func() error { _, err := s.Bean(ctx, id); return err })
}

func (s *Store) DeleteVariant(ctx context.Context, id, version int64) error {
	return s.deleteVersioned(ctx, `DELETE FROM variants WHERE id=? AND version=?`, id, version, func() error { _, err := s.Variant(ctx, id); return err })
}

// Statements are fixed at each call site. SQLite executes the conditional
// deletion and cascades atomically, including its foreign-key checks.
func (s *Store) deleteVersioned(ctx context.Context, statement string, id, version int64, exists func() error) error {
	if id <= 0 || version <= 0 {
		return ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, statement, id, version)
	if err != nil {
		var sqliteErr sqlite3.Error
		if errors.As(err, &sqliteErr) && (sqliteErr.ExtendedCode == sqlite3.ErrConstraintForeignKey || sqliteErr.ExtendedCode == sqlite3.ErrConstraintTrigger) {
			return ErrInUse
		}
		return classify(err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		if err := exists(); err != nil {
			return err
		}
		return ErrConflict
	}
	return nil
}
