package catalog

import (
	"regexp"
	"strings"
)

var modelPatterns = []*regexp.Regexp{
	regexp.MustCompile(`-\d+skl[a-e]\d{2}(-v\d+)?`),
	regexp.MustCompile(`-\d+sk\d+`),
	regexp.MustCompile(`-\d+rise\d*`),
	regexp.MustCompile(`-\d+sys\w*`),
	regexp.MustCompile(`-\d+risegame\d*`),
	regexp.MustCompile(`-\d+risestor`),
	regexp.MustCompile(`-\d+skgame\d*`),
	regexp.MustCompile(`-\d+ska\d*`),
	regexp.MustCompile(`-\d+skstor\d*`),
	regexp.MustCompile(`-\d+sysstor`),
	regexp.MustCompile(`game\d*`),
	regexp.MustCompile(`stor\d*`),
	regexp.MustCompile(`-ks\d+`),
	regexp.MustCompile(`-rise`),
	regexp.MustCompile(`-\d+sysle\d+`),
	regexp.MustCompile(`-\d+skb\d+`),
	regexp.MustCompile(`-\d+skc\d+`),
	regexp.MustCompile(`-\d+sk\d+b`),
	regexp.MustCompile(`-v\d+`),
	regexp.MustCompile(`-[a-z]{3}$`),
}

var (
	reEcc       = regexp.MustCompile(`-(no)?ecc-\d+`)
	reStorSfx   = regexp.MustCompile(`-(sas|sa|ssd|nvme)$`)
	reSpecDigit = regexp.MustCompile(`-\d{4,5}$`)
)

func StandardizeConfig(config string) string {
	if config == "" {
		return ""
	}
	normalized := strings.TrimSpace(strings.ToLower(config))
	for _, p := range modelPatterns {
		normalized = p.ReplaceAllString(normalized, "")
	}
	normalized = reEcc.ReplaceAllString(normalized, "")
	normalized = reStorSfx.ReplaceAllString(normalized, "")
	normalized = reSpecDigit.ReplaceAllString(normalized, "")
	return normalized
}

var (
	reMemoryDisplay  = regexp.MustCompile(`(?i)(\d+)g(?:-(?:no)?ecc)?(?:-(\d+))?`)
	reStorageDisplay = regexp.MustCompile(`(?i)(\d+)x(\d+)(?:g|gb)?(?:-|)(ssd|nvme|hdd|sas|sa)`)
)

// FormatMemoryDisplay 只根据 OVH FQN 中明确给出的容量、ECC 与频率生成展示文案。
// DDR 代际不在 ram-32g-ecc-2133 这类 FQN 内，不能为了好看而猜成 DDR4。
func FormatMemoryDisplay(memoryCode string) string {
	m := reMemoryDisplay.FindStringSubmatch(memoryCode)
	if m == nil {
		return memoryCode
	}
	parts := []string{m[1] + "GB"}
	// noecc 是明确的非 ECC 标记；不能用宽泛的 Contains("ecc")，否则会把
	// ram-128g-noecc-2933 错写为 ECC 内存。
	if strings.Contains(strings.ToLower(memoryCode), "-ecc-") {
		parts = append(parts, "ECC")
	}
	parts = append(parts, "RAM")
	if m[2] != "" {
		parts[len(parts)-1] += "-" + m[2]
	}
	return strings.Join(parts, " ")
}

// FormatStorageDisplay 同样只转换编码中可确认的信息；sa/sas 不会被擅自标成 HDD。
func FormatStorageDisplay(storageCode string) string {
	m := reStorageDisplay.FindStringSubmatch(storageCode)
	if m == nil {
		return storageCode
	}
	capacity := m[2] + "GB"
	if len(m[2]) >= 4 && strings.HasSuffix(m[2], "000") {
		capacity = strings.TrimSuffix(m[2], "000") + "TB"
	}
	medium := strings.ToUpper(m[3])
	if strings.EqualFold(medium, "nvme") {
		medium = "NVMe"
	}
	return m[1] + "×" + capacity + " " + medium
}

func FormatConfigDisplay(memoryCode, storageCode string) string {
	mem := "默认内存"
	if memoryCode != "" {
		mem = FormatMemoryDisplay(memoryCode)
	}
	stor := "默认存储"
	if storageCode != "" {
		stor = FormatStorageDisplay(storageCode)
	}
	return mem + " · " + stor
}

func MatchConfig(userMemory, userStorage, ovhMemory, ovhStorage string) bool {
	memoryMatch := true
	if userMemory != "" && ovhMemory != "" {
		memoryMatch = StandardizeConfig(userMemory) == StandardizeConfig(ovhMemory)
	}
	storageMatch := true
	if userStorage != "" && ovhStorage != "" {
		storageMatch = StandardizeConfig(userStorage) == StandardizeConfig(ovhStorage)
	}
	return memoryMatch && storageMatch
}
