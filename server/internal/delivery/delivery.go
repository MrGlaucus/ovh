// Package delivery watches authenticated server lists; it never reads stock or email APIs.
package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/telegram"
	"github.com/ovh-buy/server/internal/types"
)

type Getter interface {
	Get(string, interface{}) error
}
type watcher struct {
	state  *app.State
	client func(string) (Getter, error)
	send   func(string, map[string]interface{}) (telegram.MessageRef, error)
}

// Signed calls always use the account's guarded transport; no proxy fallback.
func Run(state *app.State) {
	w := watcher{state: state, client: func(id string) (Getter, error) { return state.OVH.ClientFor(id) }, send: func(msg string, markup map[string]interface{}) (telegram.MessageRef, error) {
		return telegram.SendMessageWithRef(state, msg, markup)
	}}
	var active sync.Map
	var next sync.Map
	var nextList sync.Map
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		var accounts []string
		if err := state.DB.Select(&accounts, "SELECT a.id FROM ovh_accounts a LEFT JOIN delivery_accounts d ON d.account_id=a.id WHERE COALESCE(d.enabled,1)=1"); err != nil {
			state.Logger.Warn("读取发货通知开关失败: "+err.Error(), "delivery")
		}
		for _, id := range accounts {
			if due, ok := next.Load(id); ok && time.Now().Before(due.(time.Time)) {
				continue
			}
			if _, busy := active.LoadOrStore(id, true); busy {
				continue
			}
			go func(id string) {
				defer active.Delete(id)
				defer func() { next.Store(id, time.Now().Add(30*time.Second)) }()
				defer func() {
					if r := recover(); r != nil {
						state.Logger.Error(fmt.Sprintf("发货检测异常: %v", r), "delivery")
					}
				}()
				// 待发消息仍每 30 秒重试，服务器名单独立按 60 秒检测。
				due, exists := nextList.Load(id)
				checkList := !exists || !time.Now().Before(due.(time.Time))
				if checkList {
					nextList.Store(id, time.Now().Add(time.Minute))
				}
				w.scanWithList(id, checkList)
			}(id)
		}
		<-tick.C
	}
}
func (w *watcher) enabled(id string) bool {
	s, err := w.state.DB.DeliverySettings(id)
	return err == nil && s.Enabled
}
func (w *watcher) failure(id string, err error) {
	w.state.Logger.Warn("账户 "+id+" 发货检测/通知未完成: "+err.Error(), "delivery")
	// Do not expose transport errors (which can contain account proxy credentials) in the page.
	_, e := w.state.DB.Exec("UPDATE delivery_accounts SET last_error=? WHERE account_id=?", "检测或通知失败，系统将自动重试，请查看服务器日志", id)
	if e != nil {
		w.state.Logger.Error("保存发货诊断失败: "+e.Error(), "delivery")
	}
}
func (w *watcher) scan(id string) {
	w.scanWithList(id, true)
}

func (w *watcher) scanWithList(id string, checkList bool) {
	settings, settingsErr := w.state.DB.DeliverySettings(id)
	if settingsErr != nil || !settings.Enabled {
		return
	}
	current := func() bool {
		s, e := w.state.DB.DeliverySettings(id)
		return e == nil && s.Enabled && s.Generation == settings.Generation
	}
	account, ok := w.state.FindAccount(id)
	if !ok {
		return
	}
	client, err := w.client(id)
	if err != nil {
		w.failure(id, err)
	} else {
		client = &guardedGetter{w: w, account: id, client: client, generation: settings.Generation}
		if checkList {
			var names []string
			if err = client.Get("/dedicated/server", &names); err != nil {
				w.failure(id, err)
			} else if names == nil {
				w.failure(id, fmt.Errorf("服务器列表返回 null，保留原基线"))
			} else if err = w.state.DB.ObserveDeliveryServers(id, names, time.Now().Unix(), settings.Generation); err != nil {
				w.failure(id, err)
				return
			}
		}
	}
	// Persisted pending deliveries are independent of later server-list failures/removals.
	rows, err := w.state.DB.PendingDeliveries(id, time.Now().Unix())
	if err != nil {
		w.failure(id, err)
		return
	}
	for _, row := range rows {
		if !current() {
			return
		}
		// Reserve this attempt before any network calls. A crash leaves it retryable.
		if _, err = w.state.DB.Exec("UPDATE delivery_servers SET next_attempt=? WHERE id=?", time.Now().Add(30*time.Second).Unix(), row.ID); err != nil {
			w.failure(id, err)
			return
		}
		msg := row.Payload
		if msg == "" {
			if client == nil {
				continue
			}
			msg, err = build(client, account, row)
			if err != nil {
				w.failure(id, err)
				continue
			}
			if _, err = w.state.DB.Exec("UPDATE delivery_servers SET payload=? WHERE id=?", msg, row.ID); err != nil {
				w.failure(id, err)
				continue
			}
		}
		if !current() {
			return
		}
		ref, sendErr := w.send(msg, buttons(row.ID))
		if sendErr != nil {
			delay := 30 * time.Second
			var failure *telegram.SendError
			if errors.As(sendErr, &failure) && failure.RetryAfter > delay {
				delay = failure.RetryAfter
			}
			if _, err = w.state.DB.Exec("UPDATE delivery_servers SET next_attempt=? WHERE id=?", time.Now().Add(delay).Unix(), row.ID); err != nil {
				w.failure(id, err)
			}
			w.failure(id, sendErr)
			continue
		}
		if _, err = w.state.DB.Exec("UPDATE delivery_servers SET sent_at=?,chat_id=?,message_id=? WHERE id=?", time.Now().Unix(), ref.ChatID, ref.MessageID, row.ID); err != nil {
			w.failure(id, err)
		}
	}
}

type guardedGetter struct {
	w          *watcher
	account    string
	client     Getter
	generation int64
}

func (g *guardedGetter) Get(path string, result interface{}) error {
	s, err := g.w.state.DB.DeliverySettings(g.account)
	if err != nil || !s.Enabled || s.Generation != g.generation {
		return fmt.Errorf("发货通知已关闭")
	}
	// SDK supports context requests. Preserve its account transport, adding a bounded deadline only.
	if c, ok := g.client.(interface {
		GetWithContext(context.Context, string, interface{}) error
	}); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return c.GetWithContext(ctx, path, result)
	}
	return g.client.Get(path, result)
}

func buttons(id string) map[string]interface{} {
	row := []map[string]string{}
	for _, b := range [][2]string{{"🔄 重启", "dr"}, {"🌐 测速", "dn"}, {"🛠 硬件", "dh"}} {
		data, _ := json.Marshal(map[string]string{"a": b[1], "u": id})
		row = append(row, map[string]string{"text": b[0], "callback_data": string(data)})
	}
	return map[string]interface{}{"inline_keyboard": [][]map[string]string{row}}
}
func text(v interface{}) string {
	if v == nil || v == "" {
		return "未获取到"
	}
	return fmt.Sprint(v)
}
func obj(v interface{}) map[string]interface{} { m, _ := v.(map[string]interface{}); return m }
func quantity(v interface{}) string {
	m := obj(v)
	if m["value"] == nil {
		return "未获取到"
	}
	unit, _ := m["unit"].(string)
	return strings.TrimSpace(fmt.Sprintf("%v %s", m["value"], unit))
}

func bandwidth(v interface{}) string {
	if n, ok := v.(float64); ok {
		if n >= 1e9 {
			return fmt.Sprintf("%.1f Gbps", n/1e9)
		}
		if n >= 1e6 {
			return fmt.Sprintf("%.0f Mbps", n/1e6)
		}
		return fmt.Sprintf("%.0f bps", n)
	}
	return quantity(v)
}
func build(client Getter, account types.OVHAccount, row db.DeliveryServer) (string, error) {
	base := "/dedicated/server/" + url.PathEscape(row.ServiceName)
	info, hw, network := map[string]interface{}{}, map[string]interface{}{}, map[string]interface{}{}
	for _, q := range []struct {
		path string
		out  *map[string]interface{}
	}{{base, &info}, {base + "/specifications/hardware", &hw}, {base + "/specifications/network", &network}} {
		if err := client.Get(q.path, q.out); err != nil {
			return "", err
		}
		if *q.out == nil {
			return "", fmt.Errorf("服务器详情暂为空")
		}
	}
	disks := []string{}
	if groups, ok := hw["diskGroups"].([]interface{}); ok {
		for _, g := range groups {
			d := obj(g)
			disks = append(disks, fmt.Sprintf("%s × %s %s", text(d["numberOfDisks"]), quantity(d["diskSize"]), text(d["diskType"])))
		}
	}
	diskText := strings.Join(disks, " / ")
	if diskText == "" {
		diskText = "未获取到"
	}
	return fmt.Sprintf("🎉 服务器已发货\n\n👤 所属账户：%s · %s\n\n🖥 设备 ID：%s\n📦 设备型号：%s\n⚙️ CPU：%s\n💾 内存：%s\n💽 硬盘：%s\n🌐 带宽：%s\n\n📍 机房：%s\n🔗 IP：%s\n🔀 交换机：%s\n\n🕒 发现时间：%s（北京时间）\n来源：已购服务器列表", account.Name, account.Zone, row.ServiceName, text(info["commercialRange"]), text(hw["processorName"]), quantity(hw["memorySize"]), diskText, bandwidth(obj(network["bandwidth"])["OvhToInternet"]), text(info["datacenter"]), text(info["ip"]), text(obj(network["switching"])["name"]), time.Unix(row.DiscoveredAt, 0).In(types.NowChina().Location()).Format("2006-01-02 15:04:05")), nil
}
