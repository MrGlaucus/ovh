package delivery

import (
	"github.com/ovh-buy/server/internal/catalog"
	"strings"
)

// Keep the physical datacenter identifier; unknown locations must not be guessed from IP or switch names.
func datacenterLocation(value interface{}) string {
	raw, _ := value.(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "未获取到"
	}
	code := strings.ToLower(raw)
	cityCode := strings.TrimRight(code, "0123456789")
	if cityCode == "eri" || cityCode == "lon" {
		return "LON 🇬🇧 英国·伦敦（" + strings.ToUpper(raw) + "）"
	}
	city, country, ok := catalog.DatacenterLocation(code)
	if !ok {
		return raw
	}
	location := country
	if city != country {
		location += "·" + city
	}
	return location + "（" + strings.ToUpper(raw) + "）"
}
