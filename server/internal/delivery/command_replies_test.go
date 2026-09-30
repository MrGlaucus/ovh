package delivery

import (
	"testing"
	"time"

	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/telegram"
)

func TestCommandRepliesPersistRetryAndDeduplicate(t *testing.T) {
	w, f, _, dir := fixture(t)
	w.scan("a")
	f.names = append(f.names, "new")
	w.scan("a")
	var id string
	if err := w.state.DB.Get(&id, "SELECT id FROM delivery_servers WHERE service_name='new'"); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"dn", "dn", "dh"} {
		if err := w.state.DB.QueueDeliveryCommand(id, action, "command-"+action); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	flushCommandReplies(w.state, time.Now(), func(r commandReply) error {
		calls++
		if r.ChatID != "123" || r.MessageID != 44 || r.Command != "command-"+r.Action {
			t.Fatal("wrong command target")
		}
		return &telegram.SendError{Message: "rate limited", Temporary: true, RetryAfter: 90 * time.Second}
	})
	if calls != 2 {
		t.Fatalf("duplicate clicks queued %d sends", calls)
	}
	var due int64
	if err := w.state.DB.Get(&due, "SELECT min(next_attempt) FROM delivery_command_replies"); err != nil {
		t.Fatal(err)
	}
	if due < time.Now().Add(89*time.Second).Unix() {
		t.Fatal("retry_after ignored")
	}
	w.state.DB.Close()
	var err error
	w.state.DB, err = db.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	flushCommandReplies(w.state, time.Now(), func(commandReply) error { t.Fatal("retried too early"); return nil })
	flushCommandReplies(w.state, time.Unix(due+1, 0), func(commandReply) error { calls++; return nil })
	flushCommandReplies(w.state, time.Unix(due+100, 0), func(commandReply) error { t.Fatal("successful send repeated"); return nil })
	if calls != 4 {
		t.Fatal("pending sends lost on restart")
	}
	if err := w.state.DB.QueueDeliveryCommand(id, "dn", "command-dn"); err != nil {
		t.Fatal(err)
	}
	flushCommandReplies(w.state, time.Now(), func(commandReply) error { calls++; return nil })
	if calls != 5 {
		t.Fatal("later intentional click blocked")
	}
}
