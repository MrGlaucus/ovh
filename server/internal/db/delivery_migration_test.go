package db

import (
	"github.com/ovh-buy/server/internal/types"
	"strings"
	"testing"
)

func TestLegacyDeliveryMigrationPreservesCallbacks(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.UpsertAccount(types.OVHAccount{ID: "a", Name: "A"}); err != nil {
		t.Fatal(err)
	}
	// Recreate the exact legacy parent schema before populating its child tables.
	if _, err = d.Exec("DROP TABLE delivery_servers"); err != nil {
		t.Fatal(err)
	}
	legacy := deliverySchema[strings.Index(deliverySchema, "CREATE TABLE IF NOT EXISTS delivery_servers"):]
	legacy = legacy[:strings.Index(legacy, ";")+1]
	legacy = strings.Replace(legacy, "previous_received_at INTEGER NOT NULL DEFAULT 0", "UNIQUE(account_id, service_name)", 1)
	if _, err = d.Exec(legacy); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO delivery_accounts(account_id,initialized) VALUES('a',1)`,
		`INSERT INTO delivery_servers(id,account_id,service_name,discovered_at,sent_at,message_id) VALUES('old','a','machine',100,110,42)`,
		`INSERT INTO delivery_reboot_replies VALUES('old','reply',0)`,
		`INSERT INTO delivery_rescue_jobs(delivery_id,baseline,started_at) VALUES('old','[]',120)`,
		`INSERT INTO delivery_command_replies VALUES('old','dn','command',0)`,
	} {
		if _, err = d.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	d.Close()
	d, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, table := range []string{"delivery_reboot_replies", "delivery_rescue_jobs", "delivery_command_replies"} {
		var n int
		if err = d.Get(&n, "SELECT COUNT(*) FROM "+table); err != nil || n != 1 {
			t.Fatalf("child lost %s: %v", table, err)
		}
	}
	for _, names := range [][]string{{"machine"}, {}, {}, {"machine"}, {"machine"}} {
		if err = d.ObserveDeliveryServers("a", names, 200); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := d.PendingDeliveries("a", 300)
	if err != nil || len(pending) != 1 || pending[0].PreviousReceivedAt != 100 {
		t.Fatalf("bad return event: %+v %v", pending, err)
	}
	old, err := d.GetDelivery("old")
	if err != nil || old.MessageID != 42 {
		t.Fatal("old callback lost", err)
	}
	if err = d.migrateDeliveryEvents(); err != nil {
		t.Fatal("migration not idempotent", err)
	}
}
