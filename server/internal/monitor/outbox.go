package monitor

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/telegram"
)

const availabilityRetryTTL = 15 * time.Minute

type pendingAvailability struct {
	Config                map[string]interface{}   `json:"config"`
	DCs                   []map[string]interface{} `json:"dcs"`
	ServerName            string                   `json:"serverName"`
	PriceError            string                   `json:"priceError"`
	Destination           string                   `json:"destination"`
	SubscriptionCreatedAt string                   `json:"subscriptionCreatedAt"`
	NextAttempt           int64                    `json:"nextAttempt"`
}

func (m *Monitor) telegramDestination() string {
	cfg := m.state.Config.Get()
	return fmt.Sprintf("%x", sha256.Sum256([]byte(cfg.TgToken+"\x00"+cfg.TgChatID)))
}

func (m *Monitor) outboxError(err error) {
	if err != nil {
		m.state.Logger.Error("Telegram 待补发记录操作失败: "+err.Error(), "monitor")
	}
}

func (m *Monitor) savePending(entry db.TelegramOutboxEntry, pending pendingAvailability) {
	data, err := json.Marshal(pending)
	if err == nil {
		entry.Payload = string(data)
		err = m.state.DB.SaveTelegramOutbox(entry)
	}
	m.outboxError(err)
}

// Persist before sending, so restarting the container does not discard failures.
func (m *Monitor) enqueueAvailability(plan string, dcs []map[string]interface{}, config map[string]interface{}, name, priceError string) (db.TelegramOutboxEntry, pendingAvailability) {
	var entry db.TelegramOutboxEntry
	var pending pendingAvailability
	if m.state.DB == nil || m.state.Config == nil {
		return entry, pending
	}
	cfg := m.state.Config.Get()
	if cfg.TgToken == "" || cfg.TgChatID == "" {
		return entry, pending
	}
	sub := m.FindSubscription(plan)
	if sub == nil {
		return entry, pending
	}
	entry = db.TelegramOutboxEntry{ID: uuid.NewString(), PlanCode: plan, CreatedAt: time.Now().Unix()}
	pending = pendingAvailability{Config: config, DCs: dcs, ServerName: name, PriceError: priceError,
		Destination: m.telegramDestination(), SubscriptionCreatedAt: sub.CreatedAt}
	m.savePending(entry, pending)
	return entry, pending
}

func (m *Monitor) finishPending(entry db.TelegramOutboxEntry, pending pendingAvailability, ref *telegram.MessageRef, err error) {
	if entry.ID == "" {
		return
	}
	if ref != nil {
		m.outboxError(m.state.DB.DeleteTelegramOutbox(entry.ID))
		return
	}
	delay := time.Minute
	var failure *telegram.SendError
	if errors.As(err, &failure) && failure.RetryAfter > delay {
		delay = failure.RetryAfter
	}
	pending.NextAttempt = time.Now().Add(delay).Unix()
	m.savePending(entry, pending)
	m.state.Logger.Warn("Telegram 上货通知未送达，已保留待补发记录: "+entry.PlanCode, "monitor")
}

// Fresh checked statuses only: never use last round's available state to resend.
// Missing/unmonitored/offline DCs are discarded; failed price checks wait for a
// later successful check. Newly detected restocks supersede the old event.
func filterPendingDCs(dcs []map[string]interface{}, key string, checked map[string]string, restocked map[string]bool) (keep, ready []map[string]interface{}) {
	for _, dc := range dcs {
		name, _ := dc["dc"].(string)
		statusKey := name + "|" + key
		status := checked[statusKey]
		if restocked[statusKey] || (status != "available" && status != "price_check_failed") {
			continue
		}
		keep = append(keep, dc)
		if status == "available" {
			ready = append(ready, dc)
		}
	}
	return
}

func (m *Monitor) retryPendingAvailability(sub *Subscription, entries []db.TelegramOutboxEntry, checked map[string]string, restocked map[string]bool) {
	if m.state.DB == nil || !m.stillInSubscriptions(sub) {
		return
	}
	cfg := sub.checkConfig()
	for _, entry := range entries {
		var pending pendingAvailability
		err := json.Unmarshal([]byte(entry.Payload), &pending)
		if err != nil || !cfg.NotifyAvailable || pending.SubscriptionCreatedAt != sub.CreatedAt ||
			pending.Destination != m.telegramDestination() || time.Since(time.Unix(entry.CreatedAt, 0)) > availabilityRetryTTL {
			m.outboxError(m.state.DB.DeleteTelegramOutbox(entry.ID))
			continue
		}
		key, _ := pending.Config["config_key"].(string)
		keep, ready := filterPendingDCs(pending.DCs, key, checked, restocked)
		pending.DCs = keep
		if len(keep) == 0 {
			m.outboxError(m.state.DB.DeleteTelegramOutbox(entry.ID))
			continue
		}
		if len(ready) == 0 || time.Now().Unix() < pending.NextAttempt {
			m.savePending(entry, pending)
			continue
		}
		msg, markup := m.buildAvailabilityAlert(entry.PlanCode, ready, pending.Config, pending.ServerName, pending.PriceError, "", "")
		ref, sendErr := telegram.SendMessageWithRef(m.state, msg, markup)
		if sendErr != nil {
			m.state.Logger.Warn("Telegram 上货通知补发失败: "+sendErr.Error(), "monitor")
			m.finishPending(entry, pending, nil, sendErr)
			continue
		}
		m.saveTelegramAvailabilitySession(entry.PlanCode, pending.Config, msg, ref, ready)
		// Only remove DCs included in this successful message.
		remaining := make([]map[string]interface{}, 0)
		for _, dc := range keep {
			name, _ := dc["dc"].(string)
			if checked[name+"|"+key] != "available" {
				remaining = append(remaining, dc)
			}
		}
		if len(remaining) == 0 {
			m.finishPending(entry, pending, &ref, nil)
		} else {
			pending.DCs = remaining
			m.savePending(entry, pending)
		}
		m.state.Logger.Info("Telegram 上货通知补发成功: "+entry.PlanCode, "monitor")
	}
}
