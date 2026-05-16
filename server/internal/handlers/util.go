package handlers

import (
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	ovhsdk "github.com/ovh/go-ovh/ovh"
)

// parallelGetDetails 通用并发 GET helper。对 keys[i] 用 pathFn(keys[i]) 拼出路径，
// 并发拉到 detail。最多 concurrency 个并发。结果按索引对齐，失败位 nil。
// 这是 1:1 串行 for 循环的并发替代版本，仅把网络 IO 并发化。
func parallelGetDetails(client *ovhsdk.Client, keys []interface{}, pathFn func(interface{}) string, concurrency int) []map[string]interface{} {
	if concurrency <= 0 {
		concurrency = 10
	}
	results := make([]map[string]interface{}, len(keys))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, k := range keys {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, key interface{}) {
			defer wg.Done()
			defer func() { <-sem }()
			var d map[string]interface{}
			if err := client.Get(pathFn(key), &d); err == nil {
				results[idx] = d
			}
		}(i, k)
	}
	wg.Wait()
	return results
}

// parallelGetStrings string 版本简化调用
func parallelGetStringKeys(client *ovhsdk.Client, keys []string, pathFn func(string) string, concurrency int) []map[string]interface{} {
	if concurrency <= 0 {
		concurrency = 10
	}
	results := make([]map[string]interface{}, len(keys))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, k := range keys {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, key string) {
			defer wg.Done()
			defer func() { <-sem }()
			var d map[string]interface{}
			if err := client.Get(pathFn(key), &d); err == nil {
				results[idx] = d
			}
		}(i, k)
	}
	wg.Wait()
	return results
}

// defaultZero 数字字段缺失时返回 0 而不是 null（OVH 偶尔不返回某些字段）
func defaultZero(v interface{}) interface{} {
	if v == nil {
		return 0
	}
	return v
}

// defaultObj 字典字段缺失时返回 {} 而不是 null
func defaultObj(v interface{}) interface{} {
	if v == nil {
		return map[string]interface{}{}
	}
	return v
}

// defaultArr 数组字段缺失时返回 [] 而不是 null
func defaultArr(v interface{}) interface{} {
	if v == nil {
		return []interface{}{}
	}
	return v
}

// isNonEmptyStorage 对应 Python "if data.get('storageConfig'):" 的 falsy 语义
// 空数组 / 空字典 / nil / false / 0 都视为"未提供自定义 storage"
func isNonEmptyStorage(v interface{}) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case []interface{}:
		return len(x) > 0
	case map[string]interface{}:
		return len(x) > 0
	case string:
		return x != ""
	}
	return true
}

func jsonUnmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// idToString 把 OVH 返回的 ID（可能是 string 或数字）转成字符串
// 用于 /me/refund、/me/bill、/me/task/* 等返回数组里既可能是 ["xx","yy"] 也可能是 [1,2] 的端点
func idToString(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case json.Number:
		return x.String()
	default:
		return fmt.Sprintf("%v", x)
	}
}
