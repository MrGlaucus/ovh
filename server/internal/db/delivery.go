package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const deliverySchema = `
CREATE TABLE IF NOT EXISTS delivery_accounts (
 account_id TEXT PRIMARY KEY REFERENCES ovh_accounts(id) ON DELETE CASCADE,
 enabled INTEGER NOT NULL DEFAULT 1, initialized INTEGER NOT NULL DEFAULT 0,
 generation INTEGER NOT NULL DEFAULT 0,
 last_check INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS delivery_servers (
 id TEXT PRIMARY KEY, account_id TEXT NOT NULL REFERENCES ovh_accounts(id) ON DELETE CASCADE,
 service_name TEXT NOT NULL, discovered_at INTEGER NOT NULL, payload TEXT NOT NULL DEFAULT '',
 next_attempt INTEGER NOT NULL DEFAULT 0, sent_at INTEGER NOT NULL DEFAULT 0,
 chat_id TEXT NOT NULL DEFAULT '', message_id INTEGER NOT NULL DEFAULT 0,
 reboot_claimed INTEGER NOT NULL DEFAULT 0,
 previous_received_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS delivery_pending ON delivery_servers(account_id, sent_at, next_attempt);
CREATE TABLE IF NOT EXISTS delivery_presence (
 account_id TEXT NOT NULL REFERENCES ovh_accounts(id) ON DELETE CASCADE,
 service_name TEXT NOT NULL, missing_count INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(account_id, service_name)
);
CREATE TABLE IF NOT EXISTS delivery_reboot_replies (
 delivery_id TEXT PRIMARY KEY REFERENCES delivery_servers(id) ON DELETE CASCADE,
 text TEXT NOT NULL, next_attempt INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS delivery_rescue_jobs (
 delivery_id TEXT PRIMARY KEY REFERENCES delivery_servers(id) ON DELETE CASCADE,
 baseline TEXT NOT NULL, started_at INTEGER NOT NULL,
 next_attempt INTEGER NOT NULL DEFAULT 0, credentials TEXT NOT NULL DEFAULT '',
 sent_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS delivery_command_replies (
 delivery_id TEXT NOT NULL REFERENCES delivery_servers(id) ON DELETE CASCADE,
 action TEXT NOT NULL, command TEXT NOT NULL, next_attempt INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(delivery_id, action)
);
`

type DeliverySettings struct {
	AccountID   string `db:"account_id" json:"accountId"`
	Enabled     bool   `db:"enabled" json:"enabled"`
	Initialized bool   `db:"initialized" json:"initialized"`
	LastCheck   int64  `db:"last_check" json:"lastCheck"`
	LastError   string `db:"last_error" json:"lastError"`
	Generation  int64  `db:"generation" json:"-"`
}

// Rebuild only the legacy unique table, preserving event IDs and all child references.
// foreign_keys is connection-local and must be disabled before beginning the transaction.
func (db *DB) migrateDeliveryEvents() error {
	ctx := context.Background()
	conn, err := db.Connx(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var schema string
	if err = conn.GetContext(ctx, &schema, "SELECT sql FROM sqlite_master WHERE type='table' AND name='delivery_servers'"); err != nil {
		return err
	}
	if !strings.Contains(schema, "UNIQUE(account_id, service_name)") {
		return nil
	}
	if _, err = conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}
	defer conn.ExecContext(ctx, "PRAGMA foreign_keys=ON")
	tx, err := conn.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	start := strings.Index(deliverySchema, "CREATE TABLE IF NOT EXISTS delivery_servers")
	definition := deliverySchema[start:]
	definition = definition[:strings.Index(definition, ";")+1]
	definition = strings.Replace(definition, "IF NOT EXISTS delivery_servers", "delivery_servers_new", 1)
	for _, stmt := range []string{
		definition,
		`INSERT INTO delivery_servers_new(id,account_id,service_name,discovered_at,payload,next_attempt,sent_at,chat_id,message_id,reboot_claimed)
		 SELECT id,account_id,service_name,discovered_at,payload,next_attempt,sent_at,chat_id,message_id,reboot_claimed FROM delivery_servers`,
		`INSERT OR IGNORE INTO delivery_presence(account_id,service_name) SELECT account_id,service_name FROM delivery_servers WHERE account_id IN (SELECT account_id FROM delivery_accounts WHERE initialized=1 AND enabled=1)`,
		"DROP TABLE delivery_servers",
		"ALTER TABLE delivery_servers_new RENAME TO delivery_servers",
		"CREATE INDEX delivery_pending ON delivery_servers(account_id,sent_at,next_attempt)",
	} {
		if _, err = tx.Exec(stmt); err != nil {
			return err
		}
	}
	rows, err := tx.Query("PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	hasViolation := rows.Next()
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return readErr
	}
	if hasViolation {
		return fmt.Errorf("delivery migration foreign key check failed")
	}
	return tx.Commit()
}

type DeliveryServer struct {
	PreviousReceivedAt int64  `db:"previous_received_at"`
	ID                 string `db:"id"`
	AccountID          string `db:"account_id"`
	ServiceName        string `db:"service_name"`
	DiscoveredAt       int64  `db:"discovered_at"`
	Payload            string `db:"payload"`
	NextAttempt        int64  `db:"next_attempt"`
	SentAt             int64  `db:"sent_at"`
	ChatID             string `db:"chat_id"`
	MessageID          int64  `db:"message_id"`
	RebootClaimed      int64  `db:"reboot_claimed"`
}

func (db *DB) DeliverySettings(account string) (DeliverySettings, error) {
	if _, err := db.Exec("INSERT OR IGNORE INTO delivery_accounts(account_id,enabled) VALUES(?,1)", account); err != nil {
		return DeliverySettings{}, err
	}
	s := DeliverySettings{AccountID: account}
	err := db.Get(&s, "SELECT * FROM delivery_accounts WHERE account_id=?", account)
	if err == sql.ErrNoRows {
		err = nil
	}
	return s, err
}
func (db *DB) SetDeliveryEnabled(account string, enabled bool) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO delivery_accounts(account_id,enabled) VALUES(?,?) ON CONFLICT(account_id) DO UPDATE SET enabled=excluded.enabled`, account, enabled); err != nil {
		return err
	}
	if !enabled {
		if _, err = tx.Exec("DELETE FROM delivery_presence WHERE account_id=?", account); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE delivery_accounts SET initialized=0,last_check=0,last_error='',generation=generation+1 WHERE account_id=?", account); err != nil {
			return err
		}
		// Keep sent messages for their existing action buttons, but remove baseline and unsent events.
		if _, err = tx.Exec("DELETE FROM delivery_servers WHERE account_id=? AND sent_at<=0", account); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Baseline and new events are committed together. An unsuccessful list query never calls this.
func (db *DB) ObserveDeliveryServers(account string, names []string, now int64, generation ...int64) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Acquire the write lock before reading the snapshot (avoid WAL snapshot upgrade failures).
	if _, err = tx.Exec("UPDATE delivery_accounts SET last_check=last_check WHERE account_id=?", account); err != nil {
		return err
	}
	var s DeliverySettings
	if err = tx.Get(&s, "SELECT * FROM delivery_accounts WHERE account_id=?", account); err != nil {
		return err
	}
	if !s.Enabled || (len(generation) > 0 && s.Generation != generation[0]) {
		return nil
	}
	sent := int64(0)
	if !s.Initialized {
		sent = -1
	}
	var present []struct {
		Name    string `db:"service_name"`
		Missing int    `db:"missing_count"`
	}
	if err = tx.Select(&present, "SELECT service_name,missing_count FROM delivery_presence WHERE account_id=?", account); err != nil {
		return err
	}
	seen := make(map[string]bool)
	old := make(map[string]int)
	for _, p := range present {
		old[p.Name] = p.Missing
	}
	for _, name := range names {
		if name == "" {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		missing, exists := old[name]
		if !exists || missing >= 2 {
			var previous int64
			if err = tx.Get(&previous, "SELECT COALESCE(MAX(discovered_at),0) FROM delivery_servers WHERE account_id=? AND service_name=? AND sent_at>0", account, name); err != nil {
				return err
			}
			if _, err = tx.Exec(`INSERT INTO delivery_servers(id,account_id,service_name,discovered_at,sent_at,previous_received_at) VALUES(?,?,?,?,?,?)`, uuid.NewString(), account, name, now, sent, previous); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(`INSERT INTO delivery_presence(account_id,service_name,missing_count) VALUES(?,?,0) ON CONFLICT(account_id,service_name) DO UPDATE SET missing_count=0`, account, name); err != nil {
			return err
		}
	}
	for _, p := range present {
		if !seen[p.Name] {
			if _, err = tx.Exec("UPDATE delivery_presence SET missing_count=MIN(missing_count+1,2) WHERE account_id=? AND service_name=?", account, p.Name); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(`UPDATE delivery_accounts SET initialized=1,last_check=?,last_error='' WHERE account_id=?`, now, account); err != nil {
		return err
	}
	return tx.Commit()
}
func (db *DB) PendingDeliveries(account string, now int64) ([]DeliveryServer, error) {
	var rows []DeliveryServer
	err := db.Select(&rows, `SELECT * FROM delivery_servers WHERE account_id=? AND sent_at=0 AND next_attempt<=? ORDER BY discovered_at,id LIMIT 20`, account, now)
	return rows, err
}
func (db *DB) GetDelivery(id string) (DeliveryServer, error) {
	var r DeliveryServer
	err := db.Get(&r, "SELECT * FROM delivery_servers WHERE id=?", id)
	return r, err
}
func (db *DB) ClaimDeliveryReboot(id string, emailBaseline ...string) (bool, error) {
	// A notification permits a single reboot. Persist before requesting it: uncertain outcomes are never retried automatically.
	tx, err := db.Beginx()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	now := time.Now()
	r, err := tx.Exec("UPDATE delivery_servers SET reboot_claimed=? WHERE id=? AND reboot_claimed=0 AND sent_at>0", now.Unix(), id)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	// If the process stops during the OVH call, eventually report uncertainty instead of replaying it.
	_, err = tx.Exec(`INSERT INTO delivery_reboot_replies(delivery_id,text,next_attempt)
 SELECT id, '⚠️ 重启请求结果未确认：' || service_name || char(10) || '请在服务器控制页核实，本按钮不会自动重复提交。', ? FROM delivery_servers WHERE id=?`, now.Add(2*time.Minute).Unix(), id)
	if err != nil {
		return false, err
	}
	if len(emailBaseline) > 0 {
		if _, err = tx.Exec(`INSERT INTO delivery_rescue_jobs(delivery_id,baseline,started_at,next_attempt) VALUES(?,?,?,?)`, id, emailBaseline[0], now.Unix(), now.Add(30*time.Second).Unix()); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

func (db *DB) SaveDeliveryRebootReply(id, text string) error {
	_, err := db.Exec(`INSERT INTO delivery_reboot_replies(delivery_id,text,next_attempt) VALUES(?,?,0)
 ON CONFLICT(delivery_id) DO UPDATE SET text=excluded.text,next_attempt=0`, id, text)
	return err
}

// Repeated clicks share a pending send; a later click after success can send again.
func (db *DB) QueueDeliveryCommand(id, action, command string) error {
	_, err := db.Exec(`INSERT OR IGNORE INTO delivery_command_replies(delivery_id,action,command) VALUES(?,?,?)`, id, action, command)
	return err
}
