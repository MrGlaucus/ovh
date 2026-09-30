package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/secret"
	"github.com/ovh-buy/server/internal/telegram"
	ovhsdk "github.com/ovh/go-ovh/ovh"
)

type rescueClient interface {
	GetWithContext(context.Context, string, interface{}) error
	PostUnAuthWithContext(context.Context, string, interface{}, interface{}) error
}

type rescueAccountClient struct{ *ovhsdk.Client }

// The www.ovh.com password page uses this gateway, including for CA accounts
// whose regional API does not expose /secret. Reuse the exact account transport,
// without sending account credentials or following redirects to another host.
func (c rescueAccountClient) PostUnAuthWithContext(ctx context.Context, path string, in, out interface{}) error {
	if path != "/secret/retrieve" {
		return fmt.Errorf("unsupported secret endpoint")
	}
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("invalid secret request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.ovh.com/engine/api/secret/retrieve", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("invalid secret request")
	}
	req.Header.Set("Content-Type", "application/json")
	client := *c.Client.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("secret request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("secret API returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(out); err != nil {
		return fmt.Errorf("invalid secret response")
	}
	return nil
}

type rescueJob struct {
	ID          string `db:"delivery_id"`
	Baseline    string `db:"baseline"`
	Started     int64  `db:"started_at"`
	Credentials string `db:"credentials"`
	Service     string `db:"service_name"`
	Chat        string `db:"chat_id"`
	Message     int64  `db:"message_id"`
}
type rescueCredentials struct {
	IP         string
	User       string
	SecretID   string
	Password   string
	Expiration string
}
type rescueEmail struct {
	Subject string
	Body    string
	Date    string
}
type rescueWatcher struct {
	state  *app.State
	client func(string) (rescueClient, error)
	send   func(string, string, int64) error
}

func runRescueJobs(state *app.State) {
	w := rescueWatcher{state: state, client: func(id string) (rescueClient, error) {
		client, err := state.OVH.ClientFor(id)
		if err != nil {
			return nil, err
		}
		return rescueAccountClient{client}, nil
	},
		send: func(chat, text string, message int64) error {
			_, err := telegram.SendHTMLReplyChecked(state, chat, text, message)
			return err
		}}
	var active sync.Map
	var next sync.Map
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		var accounts []string
		if err := state.DB.Select(&accounts, `SELECT DISTINCT s.account_id FROM delivery_rescue_jobs r JOIN delivery_servers s ON s.id=r.delivery_id WHERE r.sent_at=0 AND r.next_attempt<=?`, time.Now().Unix()); err != nil {
			state.Logger.Warn("读取救援通知任务失败", "delivery")
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
					if recover() != nil {
						state.Logger.Error("救援通知处理异常，将自动重试", "delivery")
					}
				}()
				w.scan(id, time.Now())
			}(id)
		}
		<-ticker.C
	}
}

// Per-account scan: one mail list request, each new mail detail fetched at most once
// per scan, independent jobs for every server. Cached credentials need no OVH calls.
func (w *rescueWatcher) scan(account string, now time.Time) {
	var jobs []rescueJob
	err := w.state.DB.Select(&jobs, `SELECT r.delivery_id,r.baseline,r.started_at,r.credentials,s.service_name,s.chat_id,s.message_id
 FROM delivery_rescue_jobs r JOIN delivery_servers s ON s.id=r.delivery_id
 WHERE s.account_id=? AND r.sent_at=0 AND r.next_attempt<=?`, account, now.Unix())
	if err != nil || len(jobs) == 0 {
		return
	}
	a, ok := w.state.FindAccount(account)
	if !ok {
		return
	}
	// No network reads or secret consumption unless encrypted persistence is available.
	if !secret.Enabled() {
		w.state.Logger.Warn("救援通知等待数据库加密恢复", "delivery")
		w.deferJobs(jobs, now)
		return
	}
	if !w.deferJobs(jobs, now) {
		return
	}
	// A slow or failing proxy must not hold an account's mail scan indefinitely.
	apiContext, stopAPI := context.WithTimeout(context.Background(), 25*time.Second)
	defer stopAPI()
	var client rescueClient
	var clientErr error
	getClient := func() rescueClient {
		if client == nil && clientErr == nil {
			client, clientErr = w.client(account)
		}
		return client
	}
	var ids []int64
	listLoaded := false
	mailCache := map[int64]rescueEmail{}
	mailFailed := map[int64]bool{}
	attempts := 0
	for _, j := range jobs {
		var cred rescueCredentials
		if j.Credentials != "" {
			plain, e := secret.Decrypt(j.Credentials)
			if e != nil || json.Unmarshal([]byte(plain), &cred) != nil {
				w.failure()
				continue
			}
		} else {
			c := getClient()
			if c == nil {
				w.failure()
				continue
			}
			if !listLoaded {
				listLoaded = true
				ctx, cancel := context.WithTimeout(apiContext, 20*time.Second)
				e := c.GetWithContext(ctx, "/me/notification/email/history", &ids)
				cancel()
				if e != nil {
					ids = nil
					w.failure()
				}
				sort.Slice(ids, func(i, k int) bool { return ids[i] > ids[k] })
			}
			if ids == nil {
				continue
			}
			var baseline []int64
			if json.Unmarshal([]byte(j.Baseline), &baseline) != nil {
				w.failure()
				continue
			}
			seen := map[int64]bool{}
			for _, id := range baseline {
				seen[id] = true
			}
			// Bound detail requests per scan, without dropping overflow or failed reads.
			for _, id := range ids {
				if seen[id] {
					continue
				}
				mail, loaded := mailCache[id]
				if !loaded {
					if mailFailed[id] {
						continue
					}
					if attempts >= 50 || apiContext.Err() != nil {
						break
					}
					attempts++
					ctx, cancel := context.WithTimeout(apiContext, 20*time.Second)
					e := c.GetWithContext(ctx, "/me/notification/email/history/"+strconv.FormatInt(id, 10), &mail)
					cancel()
					if e != nil {
						mailFailed[id] = true
						w.failure()
						continue
					}
					mailCache[id] = mail
				}
				parsed, match, e := parseRescueEmail(mail, j.Service, j.Started)
				if e != nil {
					w.failure()
					continue
				}
				baseline = append(baseline, id)
				if match {
					cred = parsed
					break
				}
			}
			if cred.SecretID != "" {
				// Save the link identity before fetching the secret. Never log mail bodies or IDs.
				if !w.save(j.ID, cred) {
					continue
				}
			} else {
				b, _ := json.Marshal(baseline)
				if _, e := w.state.DB.Exec("UPDATE delivery_rescue_jobs SET baseline=? WHERE delivery_id=?", string(b), j.ID); e != nil {
					w.failure()
				}
				continue
			}
		}
		if cred.Password == "" {
			c := getClient()
			if c == nil {
				w.failure()
				continue
			}
			var result struct {
				Secret     string
				Expiration string
			}
			ctx, cancel := context.WithTimeout(apiContext, 20*time.Second)
			// Fixed API path; never follow arbitrary links from an email. This SDK client
			// retains the account's transport even for this unauthenticated endpoint.
			e := c.PostUnAuthWithContext(ctx, "/secret/retrieve", map[string]string{"id": cred.SecretID}, &result)
			cancel()
			if e != nil || result.Secret == "" {
				w.failure()
				continue
			}
			cred.Password = result.Secret
			cred.Expiration = result.Expiration
			cred.SecretID = ""
			if !w.save(j.ID, cred) {
				continue
			}
		}
		text := fmt.Sprintf("🛟 救援模式登录信息\n\n👤 所属账户：%s · %s\n🖥 设备 ID：%s\n🌐 IP：<code>%s</code>\n👤 用户名：<code>%s</code>\n🔑 密码：<code>%s</code>\n\n连接命令：<code>ssh %s@%s</code>",
			html.EscapeString(a.Name), html.EscapeString(a.Zone), html.EscapeString(j.Service),
			html.EscapeString(cred.IP), html.EscapeString(cred.User), html.EscapeString(cred.Password),
			html.EscapeString(cred.User), html.EscapeString(cred.IP))
		if e := w.send(j.Chat, text, j.Message); e != nil {
			delay := 30 * time.Second
			var se *telegram.SendError
			if errors.As(e, &se) && se.RetryAfter > delay {
				delay = se.RetryAfter
			}
			if _, err := w.state.DB.Exec("UPDATE delivery_rescue_jobs SET next_attempt=? WHERE delivery_id=?", time.Now().Add(delay).Unix(), j.ID); err != nil {
				w.failure()
			}
			w.state.Logger.Warn("救援凭据 TG 发送失败，将自动补发", "delivery")
			continue
		}
		if _, e := w.state.DB.Exec("UPDATE delivery_rescue_jobs SET sent_at=?,credentials='',baseline='[]' WHERE delivery_id=?", time.Now().Unix(), j.ID); e != nil {
			w.failure()
		}
	}
}

func (w *rescueWatcher) deferJobs(jobs []rescueJob, now time.Time) bool {
	for _, j := range jobs {
		if _, err := w.state.DB.Exec("UPDATE delivery_rescue_jobs SET next_attempt=? WHERE delivery_id=?", now.Add(30*time.Second).Unix(), j.ID); err != nil {
			w.failure()
			return false
		}
	}
	return true
}
func (w *rescueWatcher) failure() {
	w.state.Logger.Warn("救援邮件或凭据获取/保存失败，将自动重试（敏感响应不记录）", "delivery")
}
func (w *rescueWatcher) save(id string, c rescueCredentials) bool {
	b, e := json.Marshal(c)
	if e != nil {
		return false
	}
	encrypted := secret.Encrypt(string(b))
	if !strings.HasPrefix(encrypted, secret.EncPrefix) {
		w.failure()
		return false
	}
	_, e = w.state.DB.Exec("UPDATE delivery_rescue_jobs SET credentials=?,baseline='[]' WHERE delivery_id=?", encrypted, id)
	if e != nil {
		w.failure()
		return false
	}
	return true
}

var rescueIP = regexp.MustCompile(`(?mi)^\s*-?\s*IP address\s*:\s*(\S+)`)
var rescueUser = regexp.MustCompile(`(?mi)^\s*-?\s*user\s*:\s*([a-zA-Z0-9_.-]+)\s*$`)
var rescueLink = regexp.MustCompile(`https://(?:www\.)?ovh\.com/manager/secret/[^\s<>"']+`)
var rescueSecretID = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

func parseRescueEmail(m rescueEmail, service string, started int64) (rescueCredentials, bool, error) {
	var c rescueCredentials
	if !strings.Contains(m.Subject, "["+service+"]") || !strings.Contains(strings.ToLower(m.Subject), "access settings for linux rescue mode") {
		return c, false, nil
	}
	date, e := time.Parse(time.RFC3339, m.Date)
	if e != nil {
		return c, false, fmt.Errorf("invalid mail date")
	}
	if date.Unix() < started {
		return c, false, nil
	}
	body := html.UnescapeString(strings.ReplaceAll(m.Body, "\r\n", "\n"))
	ip, user := rescueIP.FindStringSubmatch(body), rescueUser.FindStringSubmatch(body)
	if len(ip) != 2 || net.ParseIP(ip[1]) == nil || len(user) != 2 {
		return c, false, fmt.Errorf("invalid rescue fields")
	}
	for _, link := range rescueLink.FindAllString(body, -1) {
		u, e := url.Parse(link)
		if e != nil || u.Host != "www.ovh.com" && u.Host != "ovh.com" || u.Path != "/manager/secret/" {
			continue
		}
		values, e := url.ParseQuery(strings.TrimPrefix(u.Fragment, "?"))
		if e != nil {
			continue
		}
		id := values.Get("id")
		if !rescueSecretID.MatchString(id) {
			continue
		}
		return rescueCredentials{IP: ip[1], User: user[1], SecretID: id}, true, nil
	}
	return c, false, fmt.Errorf("missing supported secret link")
}
