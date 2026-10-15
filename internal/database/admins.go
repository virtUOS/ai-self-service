package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SetUserAdmin gives an account the admin panel, or takes it away. An id that
// names no account is an error wrapping sql.ErrNoRows, so a stale form cannot
// report a promotion that did not happen.
func (s *Store) SetUserAdmin(ctx context.Context, userID int64, admin bool) error {
	res, err := s.db.NewUpdate().Model((*User)(nil)).
		Set("is_admin = ?", admin).
		Set("updated_at = ?", time.Now()).
		Where("id = ?", userID).Exec(ctx)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("user %d: %w", userID, sql.ErrNoRows)
	}
	return nil
}

// ListAdmins returns every account that holds the admin panel, oldest first.
func (s *Store) ListAdmins(ctx context.Context) ([]User, error) {
	var users []User
	err := s.db.NewSelect().Model(&users).Where("is_admin <> 0").Order("id ASC").Scan(ctx)
	return users, err
}

// CountAdmins reports how many accounts hold the admin panel, so startup can
// say when nobody does.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	n, err := s.db.NewSelect().Model((*User)(nil)).Where("is_admin <> 0").Count(ctx)
	return int(n), err
}
