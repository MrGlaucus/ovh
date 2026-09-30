package delivery

import (
	"strings"
	"testing"
	"time"

	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/telegram"
)

func TestRebootReplyPersistentRetryWithoutReboot(t *testing.T) {
	w, f, _, dir := fixture(t)
	w.scan("a")
	f.names = append(f.names, "new")
	w.scan("a")
	var id string
	if err := w.state.DB.Get(&id, "SELECT id FROM delivery_servers WHERE service_name='new'"); err != nil {
		t.Fatal(err)
	}
	if ok, err := w.state.DB.ClaimDeliveryReboot(id); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if ok, err := w.state.DB.ClaimDeliveryReboot(id); err != nil || ok {
		t.Fatalf("duplicate claim: %v %v", ok, err)
	}
	calls := 0
	flushRebootReplies(w.state, time.Now(), func(rebootReply) error { calls++; return nil })
	if calls != 0 {
		t.Fatal("uncertainty reply sent before OVH call could finish")
	}
	const result = "✅ 重启请求已提交\n设备：new\n任务：123"
	if err := w.state.DB.SaveDeliveryRebootReply(id, result); err != nil {
		t.Fatal(err)
	}
	if err := w.state.DB.SetDeliveryEnabled("a", false); err != nil {
		t.Fatal(err)
	}
	flushRebootReplies(w.state, time.Now(), func(r rebootReply) error {
		calls++
		if r.Text != result || r.ChatID != "123" || r.MessageID != 44 {
			t.Fatalf("wrong reply: %+v", r)
		}
		return &telegram.SendError{Message: "rate limited", Temporary: true, RetryAfter: 90 * time.Second}
	})
	var next int64
	if err := w.state.DB.Get(&next, "SELECT next_attempt FROM delivery_reboot_replies WHERE delivery_id=?", id); err != nil {
		t.Fatal(err)
	}
	if next < time.Now().Add(89*time.Second).Unix() {
		t.Fatal("retry_after ignored")
	}
	w.state.DB.Close()
	var err error
	w.state.DB, err = db.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	flushRebootReplies(w.state, time.Now(), func(rebootReply) error { t.Fatal("retried too early"); return nil })
	flushRebootReplies(w.state, time.Unix(next, 0), func(r rebootReply) error {
		calls++
		if r.Text != result {
			t.Fatal("lost result")
		}
		return nil
	})
	flushRebootReplies(w.state, time.Unix(next+100, 0), func(rebootReply) error { t.Fatal("successful reply repeated"); return nil })
	if calls != 2 {
		t.Fatalf("sends=%d", calls)
	}
	if ok, err := w.state.DB.ClaimDeliveryReboot(id); err != nil || ok {
		t.Fatal("retry unlocked reboot")
	}
}

func TestRebootInterruptedCallReportsUncertainty(t *testing.T) {
	w, f, _, _ := fixture(t)
	w.scan("a")
	f.names = append(f.names, "new")
	w.scan("a")
	var id string
	if err := w.state.DB.Get(&id, "SELECT id FROM delivery_servers WHERE service_name='new'"); err != nil {
		t.Fatal(err)
	}
	if ok, err := w.state.DB.ClaimDeliveryReboot(id); err != nil || !ok {
		t.Fatal(err)
	}
	calls := 0
	flushRebootReplies(w.state, time.Now().Add(3*time.Minute), func(r rebootReply) error {
		calls++
		if !strings.Contains(r.Text, "未确认") || !strings.Contains(r.Text, "new") {
			t.Fatal(r.Text)
		}
		return nil
	})
	if calls != 1 {
		t.Fatal("missing interrupted-operation feedback")
	}
}
