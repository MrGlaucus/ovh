package serverops

import (
	"context"
	"net/url"
	"time"
)

type Poster interface {
	Post(string, interface{}, interface{}) error
}

// Reboot is shared by server control and the delivery notification callback.
func Reboot(client Poster, service string) (map[string]interface{}, error) {
	result := map[string]interface{}{}
	if contextual, ok := client.(interface {
		PostWithContext(context.Context, string, interface{}, interface{}) error
	}); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := contextual.PostWithContext(ctx, "/dedicated/server/"+url.PathEscape(service)+"/reboot", map[string]interface{}{}, &result)
		return result, err
	}
	err := client.Post("/dedicated/server/"+url.PathEscape(service)+"/reboot", map[string]interface{}{}, &result)
	return result, err
}
