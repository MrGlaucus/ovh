package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// TelegramNotificationSession 是一条可被后续下架事件原地编辑的上架通知。
type TelegramNotificationSession struct {
	ID          string  `db:"id"`
	PlanCode    string  `db:"plan_code"`
	ConfigKey   string  `db:"config_key"`
	ChatID      string  `db:"chat_id"`
	MessageID   int64   `db:"message_id"`
	MessageText string  `db:"message_text"`
	CreatedAt   float64 `db:"created_at"`
}

// TelegramNotificationDatacenter 是聚合消息中的一个机房行。
type TelegramNotificationDatacenter struct {
	SessionID  string  `db:"session_id"`
	Datacenter string  `db:"datacenter"`
	LineText   string  `db:"line_text"`
	ButtonID   string  `db:"button_id"`
	ButtonText string  `db:"button_text"`
	OpenedAt   float64 `db:"opened_at"`
	ClosedAt   float64 `db:"closed_at"`
}

type TelegramNotificationSnapshot struct {
	Session     TelegramNotificationSession
	Datacenters []TelegramNotificationDatacenter
}

func (db *DB) CreateTelegramNotificationSession(session TelegramNotificationSession, dcs []TelegramNotificationDatacenter) error {
	if session.ID == "" || session.PlanCode == "" || session.ConfigKey == "" || session.ChatID == "" || session.MessageID == 0 {
		return fmt.Errorf("invalid telegram notification session")
	}
	if session.CreatedAt <= 0 {
		session.CreatedAt = float64(time.Now().Unix())
	}
	tx, err := db.Beginx()
	if err != nil {
		return fmt.Errorf("begin telegram notification session: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO telegram_notification_sessions (id, plan_code, config_key, chat_id, message_id, message_text, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, session.ID, session.PlanCode, session.ConfigKey, session.ChatID, session.MessageID, session.MessageText, session.CreatedAt); err != nil {
		return fmt.Errorf("insert telegram notification session: %w", err)
	}
	for _, dc := range dcs {
		if dc.Datacenter == "" || dc.LineText == "" {
			continue
		}
		if dc.OpenedAt <= 0 {
			dc.OpenedAt = session.CreatedAt
		}
		if _, err := tx.Exec(`INSERT INTO telegram_notification_datacenters
			(session_id, datacenter, line_text, button_id, button_text, opened_at, closed_at)
			VALUES (?, ?, ?, ?, ?, ?, 0)`, session.ID, dc.Datacenter, dc.LineText, dc.ButtonID, dc.ButtonText, dc.OpenedAt); err != nil {
			return fmt.Errorf("insert telegram notification datacenter: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit telegram notification session: %w", err)
	}
	return nil
}

// DeleteExpiredTelegramNotificationSessions 清理已过保留期的通知关联；原 Telegram 消息不受影响。
func (db *DB) DeleteExpiredTelegramNotificationSessions(beforeUnix float64) (int64, error) {
	res, err := db.Exec(`DELETE FROM telegram_notification_sessions WHERE created_at < ?`, beforeUnix)
	if err != nil {
		return 0, fmt.Errorf("delete expired telegram notification sessions: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// CloseTelegramNotificationDatacenter 原子关闭仍在库的机房行，并返回整条消息的最新快照。
// 找不到映射说明是升级前的旧通知，调用方应退回独立下架通知。
func (db *DB) CloseTelegramNotificationDatacenter(planCode, configKey, datacenter string, closedAt float64) (TelegramNotificationSnapshot, bool, error) {
	var out TelegramNotificationSnapshot
	if planCode == "" || configKey == "" || datacenter == "" {
		return out, false, nil
	}
	if closedAt <= 0 {
		closedAt = float64(time.Now().Unix())
	}
	tx, err := db.Beginx()
	if err != nil {
		return out, false, fmt.Errorf("begin close telegram notification datacenter: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var sessionID string
	err = tx.Get(&sessionID, `SELECT s.id
		FROM telegram_notification_sessions s
		JOIN telegram_notification_datacenters d ON d.session_id = s.id
		WHERE s.plan_code = ? AND s.config_key = ? AND d.datacenter = ? AND d.closed_at = 0
		ORDER BY s.created_at DESC LIMIT 1`, planCode, configKey, datacenter)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, fmt.Errorf("find active telegram notification datacenter: %w", err)
	}
	res, err := tx.Exec(`UPDATE telegram_notification_datacenters SET closed_at = ?
		WHERE session_id = ? AND datacenter = ? AND closed_at = 0`, closedAt, sessionID, datacenter)
	if err != nil {
		return out, false, fmt.Errorf("close telegram notification datacenter: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return out, false, nil
	}
	if err := tx.Get(&out.Session, `SELECT id, plan_code, config_key, chat_id, message_id, message_text, created_at
		FROM telegram_notification_sessions WHERE id = ?`, sessionID); err != nil {
		return out, false, fmt.Errorf("read telegram notification session: %w", err)
	}
	if err := tx.Select(&out.Datacenters, `SELECT session_id, datacenter, line_text, button_id, button_text, opened_at, closed_at
		FROM telegram_notification_datacenters WHERE session_id = ? ORDER BY datacenter`, sessionID); err != nil {
		return out, false, fmt.Errorf("read telegram notification datacenters: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return out, false, fmt.Errorf("commit close telegram notification datacenter: %w", err)
	}
	return out, true, nil
}
