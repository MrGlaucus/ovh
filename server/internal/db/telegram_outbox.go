package db

type TelegramOutboxEntry struct {
	ID        string `db:"id"`
	PlanCode  string `db:"plan_code"`
	Payload   string `db:"payload"`
	CreatedAt int64  `db:"created_at"`
}

func (db *DB) SaveTelegramOutbox(e TelegramOutboxEntry) error {
	_, err := db.Exec(`INSERT INTO telegram_availability_outbox (id, plan_code, payload, created_at)
	VALUES (?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, e.ID, e.PlanCode, e.Payload, e.CreatedAt)
	return err
}

func (db *DB) ListTelegramOutbox(plan string) ([]TelegramOutboxEntry, error) {
	var entries []TelegramOutboxEntry
	err := db.Select(&entries, `SELECT * FROM telegram_availability_outbox WHERE plan_code=? ORDER BY created_at, id`, plan)
	return entries, err
}

func (db *DB) DeleteTelegramOutbox(id string) error {
	_, err := db.Exec(`DELETE FROM telegram_availability_outbox WHERE id=?`, id)
	return err
}

func (db *DB) ExpireTelegramOutbox(before int64) error {
	_, err := db.Exec(`DELETE FROM telegram_availability_outbox WHERE created_at < ?`, before)
	return err
}
