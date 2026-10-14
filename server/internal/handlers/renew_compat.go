package handlers

import (
	"errors"
	"fmt"
	"github.com/ovh-buy/server/internal/numconv"
	ovhsdk "github.com/ovh/go-ovh/ovh"
	"net/http"
	"strings"
)

// Only an explicit payload rejection allows switching routes. Timeouts and
// permission failures never replay a write with an uncertain outcome.
func renewPayloadRejected(err error) bool {
	var apiErr *ovhsdk.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != http.StatusBadRequest {
		return false
	}
	message := strings.ToLower(apiErr.Message)
	for _, field := range []string{"manualpayment", "deleteatexpiration", "automatic", "renew"} {
		if strings.Contains(message, field) {
			return true
		}
	}
	return false
}

func putServiceRenew(client *ovhsdk.Client, legacyPath string, info, next map[string]interface{}) error {
	first := client.Put(legacyPath, map[string]interface{}{"renew": next}, nil)
	if first == nil || !renewPayloadRejected(first) {
		return first
	}
	sid, ok := numconv.ToInt64(info["serviceId"])
	if !ok || sid <= 0 {
		return first
	}
	payload := map[string]interface{}{}
	for _, key := range []string{"manualPayment", "period"} {
		if value, ok := next[key]; ok {
			payload[key] = value
		}
	}
	path := fmt.Sprintf("/services/%d", sid)
	if err := client.Put(path, map[string]interface{}{"renew": payload}, nil); err != nil {
		return fmt.Errorf("续费更新失败：%s: %v；%s: %w", legacyPath, first, path, err)
	}
	return nil
}
