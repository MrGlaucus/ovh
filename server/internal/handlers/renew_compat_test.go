package handlers

import (
	"encoding/json"
	"fmt"
	ovhsdk "github.com/ovh/go-ovh/ovh"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRenewFallbackPayloadAndNoReplay(t *testing.T) {
	for _, status := range []int{200, 400, 403, 408, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/auth/time" {
					fmt.Fprint(w, time.Now().Unix())
					return
				}
				calls++
				if r.URL.Path == "/legacy" {
					w.WriteHeader(status)
					fmt.Fprint(w, `{"message":"Conflicting arguments: automatic, manualPayment"}`)
					return
				}
				if r.URL.Path != "/services/42" {
					t.Errorf("wrong path %s", r.URL.Path)
				}
				var body struct {
					Renew map[string]interface{} `json:"renew"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if len(body.Renew) != 2 || body.Renew["manualPayment"] != false || body.Renew["period"] != float64(1) {
					t.Errorf("bad fallback payload: %+v", body)
				}
				fmt.Fprint(w, `null`)
			}))
			defer srv.Close()
			client, err := ovhsdk.NewClient(srv.URL, "app", "secret", "consumer")
			if err != nil {
				t.Fatal(err)
			}
			next := map[string]interface{}{"automatic": true, "manualPayment": false, "deleteAtExpiration": false, "forced": false, "period": 1}
			err = putServiceRenew(client, "/legacy", map[string]interface{}{"serviceId": float64(42)}, next)
			want := 1
			if status == 400 {
				want = 2
			}
			if calls != want {
				t.Errorf("got %d calls want %d", calls, want)
			}
			if (status == 200 || status == 400) != (err == nil) {
				t.Errorf("unexpected error: %v", err)
			}
			if len(next) != 5 {
				t.Fatal("mutated original payload")
			}
		})
	}
}
