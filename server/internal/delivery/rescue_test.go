package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/secret"
	ovhsdk "github.com/ovh/go-ovh/ovh"
)

type rescueTransport func(*http.Request) (*http.Response, error)

func (f rescueTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSecretRetrievalUsesAccountTransportAndFixedGateway(t *testing.T) {
	calls := 0
	c := rescueAccountClient{&ovhsdk.Client{Client: &http.Client{Transport: rescueTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://www.ovh.com/engine/api/secret/retrieve" || r.Method != "POST" || r.Header.Get("X-Ovh-Consumer") != "" {
			t.Fatal("wrong secret request route or credentials")
		}
		var body map[string]string
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil || body["id"] != testSecretID {
			t.Fatal("wrong secret request body")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"secret":"example-password"}`)), Header: http.Header{}}, nil
	})}}}
	var out struct{ Secret string }
	if e := c.PostUnAuthWithContext(context.Background(), "/secret/retrieve", map[string]string{"id": testSecretID}, &out); e != nil || out.Secret != "example-password" {
		t.Fatal("secret retrieval failed")
	}
	if e := c.PostUnAuthWithContext(context.Background(), "https://evil.example", nil, &out); e == nil {
		t.Fatal("accepted arbitrary endpoint")
	}
	if calls != 1 {
		t.Fatal("unexpected network requests")
	}
	c.Client.Client.Transport = rescueTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 307, Header: http.Header{"Location": []string{"https://evil.example/"}}, Body: io.NopCloser(strings.NewReader("sensitive-response"))}, nil
	})
	if e := c.PostUnAuthWithContext(context.Background(), "/secret/retrieve", map[string]string{"id": testSecretID}, &out); e == nil || strings.Contains(e.Error(), "sensitive-response") {
		t.Fatal("redirect accepted or response leaked")
	}
}

const testSecretID = "11111111-2222-3333-4444-555555555555"

func rescueMail(service string, when time.Time) rescueEmail {
	return rescueEmail{Subject: "[account] [" + service + "] Access settings for Linux rescue mode", Date: when.Format(time.RFC3339), Body: " - IP address: 192.0.2.1\n - user: root\n - password: https://www.ovh.com/manager/secret/#?id=" + testSecretID}
}
func TestParseRescueEmail(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	for _, tc := range []struct {
		name  string
		mail  rescueEmail
		match bool
		bad   bool
	}{
		{"valid", rescueMail("ns1", now), true, false},
		{"other server", rescueMail("ns10", now), false, false},
		{"old mail", rescueMail("ns1", now.Add(-time.Minute)), false, false},
		{"wrong type", rescueEmail{Subject: "[ns1] server available"}, false, false},
		{"bad body", rescueEmail{Subject: rescueMail("ns1", now).Subject, Date: now.Format(time.RFC3339), Body: "password: https://evil.example/"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, ok, e := parseRescueEmail(tc.mail, "ns1", now.Unix())
			if ok != tc.match || (e != nil) != tc.bad {
				t.Fatalf("match=%v error=%v", ok, e)
			}
			if ok && (c.IP != "192.0.2.1" || c.User != "root" || c.SecretID != testSecretID) {
				t.Fatal("wrong fields")
			}
		})
	}
}

type rescueFake struct {
	ids        []int64
	mails      map[string]rescueEmail
	gets       map[string]int
	posts      int
	failDetail bool
}

func (f *rescueFake) GetWithContext(_ context.Context, p string, out interface{}) error {
	f.gets[p]++
	var v interface{}
	if p == "/me/notification/email/history" {
		v = f.ids
	} else {
		if f.failDetail {
			return errors.New("proxy failed")
		}
		m, ok := f.mails[p]
		if !ok {
			return errors.New("unexpected mail")
		}
		v = m
	}
	b, _ := json.Marshal(v)
	return json.Unmarshal(b, out)
}
func (f *rescueFake) PostUnAuthWithContext(_ context.Context, p string, in, out interface{}) error {
	if p != "/secret/retrieve" {
		return errors.New("unexpected endpoint")
	}
	f.posts++
	b, _ := json.Marshal(map[string]string{"secret": "test-password<&>", "expiration": time.Now().Add(time.Hour).Format(time.RFC3339)})
	return json.Unmarshal(b, out)
}

func TestRescueJobsMultipleServersPersistentRetry(t *testing.T) {
	w, f, _, dir := fixture(t)
	t.Setenv(secret.KeyEnv, "rescue-test-key")
	if err := secret.Init(dir, filepath.Join(dir, "env")); err != nil {
		t.Fatal(err)
	}
	w.scan("a")
	f.names = append(f.names, "ns1", "ns2")
	w.scan("a")
	var rows []db.DeliveryServer
	if err := w.state.DB.Select(&rows, "SELECT * FROM delivery_servers WHERE sent_at>0"); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if ok, e := w.state.DB.ClaimDeliveryReboot(r.ID, "[1]"); e != nil || !ok {
			t.Fatal(e)
		}
	}
	now := time.Now().Add(time.Minute)
	api := &rescueFake{ids: []int64{1, 2, 3, 4}, gets: map[string]int{}, mails: map[string]rescueEmail{
		"/me/notification/email/history/2": rescueMail("ns1", now),
		"/me/notification/email/history/3": rescueMail("ns2", now),
		"/me/notification/email/history/4": rescueMail("ns-other", now),
	}}
	clientCalls, sends := 0, 0
	rw := rescueWatcher{state: w.state, client: func(id string) (rescueClient, error) {
		clientCalls++
		if id != "a" {
			t.Fatal("wrong account")
		}
		return api, nil
	}, send: func(chat, text string, msg int64) error {
		sends++
		if chat != "123" || msg != 44 || !strings.Contains(text, "<code>test-password&lt;&amp;&gt;</code>") || !strings.Contains(text, "<code>192.0.2.1</code>") || !strings.Contains(text, "<code>root</code>") {
			t.Fatal("wrong reply")
		}
		return errors.New("TG proxy failed")
	}}
	rw.scan("a", now)
	if api.gets["/me/notification/email/history"] != 1 || api.posts != 2 || sends != 2 {
		t.Fatalf("list=%d posts=%d sends=%d", api.gets["/me/notification/email/history"], api.posts, sends)
	}
	for path, count := range api.gets {
		if count != 1 || strings.HasSuffix(path, "/1") {
			t.Fatal("duplicate read or baseline mail fetched")
		}
	}
	var stored []string
	if e := w.state.DB.Select(&stored, "SELECT credentials FROM delivery_rescue_jobs"); e != nil {
		t.Fatal(e)
	}
	for _, s := range stored {
		if !strings.HasPrefix(s, secret.EncPrefix) || strings.Contains(s, "test-password") {
			t.Fatal("credentials not encrypted")
		}
	}
	w.state.DB.Close()
	var err error
	w.state.DB, err = db.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Turning delivery alerts off must not cancel feedback for an explicit reboot.
	if err := w.state.DB.SetDeliveryEnabled("a", false); err != nil {
		t.Fatal(err)
	}
	rw.client = func(string) (rescueClient, error) {
		t.Fatal("cached credentials should need no OVH calls")
		return nil, nil
	}
	rw.send = func(string, string, int64) error { sends++; return nil }
	rw.scan("a", now.Add(time.Minute))
	rw.scan("a", now.Add(2*time.Minute))
	if sends != 4 || clientCalls != 1 {
		t.Fatalf("sends=%d clients=%d", sends, clientCalls)
	}
	var pending, retained int
	w.state.DB.Get(&pending, "SELECT count(*) FROM delivery_rescue_jobs WHERE sent_at=0")
	w.state.DB.Get(&retained, "SELECT count(*) FROM delivery_rescue_jobs WHERE credentials!=''")
	if pending != 0 || retained != 0 {
		t.Fatal("completed jobs retained polling or credentials")
	}
}

func TestRescueFailedMailReadIsRetried(t *testing.T) {
	w, f, _, dir := fixture(t)
	t.Setenv(secret.KeyEnv, "rescue-test-key")
	if e := secret.Init(dir, filepath.Join(dir, "env")); e != nil {
		t.Fatal(e)
	}
	w.scan("a")
	f.names = append(f.names, "ns1")
	w.scan("a")
	var id string
	w.state.DB.Get(&id, "SELECT id FROM delivery_servers WHERE service_name='ns1'")
	if ok, e := w.state.DB.ClaimDeliveryReboot(id, "[]"); e != nil || !ok {
		t.Fatal(e)
	}
	now := time.Now().Add(time.Minute)
	api := &rescueFake{ids: []int64{2}, gets: map[string]int{}, failDetail: true, mails: map[string]rescueEmail{"/me/notification/email/history/2": rescueMail("ns1", now)}}
	sends := 0
	rw := rescueWatcher{state: w.state, client: func(string) (rescueClient, error) { return api, nil }, send: func(string, string, int64) error { sends++; return nil }}
	rw.scan("a", now)
	if sends != 0 || api.posts != 0 {
		t.Fatal("failed mail processed")
	}
	api.failDetail = false
	rw.scan("a", now.Add(time.Minute))
	if sends != 1 || api.posts != 1 {
		t.Fatal("failed mail was dropped")
	}
}
