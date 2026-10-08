package ovh

import (
	"fmt"
	ovhsdk "github.com/ovh/go-ovh/ovh"
	"testing"
)

func TestRebootAlreadyRequestedIsNotGenericForbidden(t *testing.T) {
	if !IsRebootAlreadyRequested(fmt.Errorf("wrapped: %w", &ovhsdk.APIError{Code: 403, Message: "A reboot has already been requested"})) {
		t.Fatal("known repeat not recognized")
	}
	for _, err := range []error{nil, &ovhsdk.APIError{Code: 403, Message: "This call has not been granted"}, &ovhsdk.APIError{Code: 500, Message: "A reboot has already been requested"}} {
		if IsRebootAlreadyRequested(err) {
			t.Fatalf("unexpected success: %v", err)
		}
	}
}
