package db

import (
	"strings"

	"github.com/ovh-buy/server/internal/types"
)

// ListServerFavorites 返回全部全局收藏，按收藏时间排序。
func (db *DB) ListServerFavorites() ([]types.ServerFavorite, error) {
	var items []types.ServerFavorite
	if err := db.Select(&items, `SELECT plan_code, display_name, created_at FROM server_favorites ORDER BY created_at`); err != nil {
		return nil, err
	}
	if items == nil {
		items = []types.ServerFavorite{}
	}
	return items, nil
}

// AddServerFavorite 将型号加入全局收藏；重复收藏时刷新展示名称。
func (db *DB) AddServerFavorite(planCode, displayName, createdAt string) error {
	_, err := db.Exec(`INSERT INTO server_favorites(plan_code, display_name, created_at) VALUES(?, ?, ?)
		ON CONFLICT(plan_code) DO UPDATE SET display_name = excluded.display_name`, strings.TrimSpace(planCode), strings.TrimSpace(displayName), createdAt)
	return err
}

// DeleteServerFavorite 取消收藏，返回是否实际删除了一条记录。
func (db *DB) DeleteServerFavorite(planCode string) (bool, error) {
	result, err := db.Exec(`DELETE FROM server_favorites WHERE plan_code = ?`, strings.TrimSpace(planCode))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

// IsServerFavorite 判断型号是否已收藏。
func (db *DB) IsServerFavorite(planCode string) (bool, error) {
	var count int
	if err := db.Get(&count, `SELECT COUNT(*) FROM server_favorites WHERE plan_code = ?`, strings.TrimSpace(planCode)); err != nil {
		return false, err
	}
	return count > 0, nil
}
