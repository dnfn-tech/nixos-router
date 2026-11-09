package config

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// ClearSecrets edits a detached candidate only. Full validation remains the
// caller's responsibility, so required active credentials cannot be removed.
func ClearSecrets(c Config, paths [][]string) (Config, error) {
	if len(paths) == 0 {
		return c, nil
	}
	bad := func() (Config, error) {
		return Config{}, invalidField("clearSecrets", "invalid or ambiguous secret path")
	}
	if len(paths) > 256 {
		return bad()
	}
	seen := map[string]bool{}
	for _, path := range paths {
		if len(path) == 0 || len(path) > 64 {
			return bad()
		}
		for _, part := range path {
			if len(part) == 0 || len(part) > 1024 {
				return bad()
			}
		}
		encoded, _ := json.Marshal(path)
		if seen[string(encoded)] || !clearableSecretPath(path) {
			return bad()
		}
		seen[string(encoded)] = true
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return Config{}, err
	}
	var root map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&root); err != nil {
		return Config{}, err
	}
	for _, path := range paths {
		var current interface{} = root
		for _, part := range path[:len(path)-1] {
			switch value := current.(type) {
			case map[string]interface{}:
				next, exists := value[part]
				if !exists {
					return bad()
				}
				current = next
			case []interface{}:
				index, e := strconv.Atoi(part)
				if e != nil || index < 0 || index >= len(value) || strconv.Itoa(index) != part {
					return bad()
				}
				current = value[index]
			default:
				return bad()
			}
		}
		parent, ok := current.(map[string]interface{})
		if !ok {
			return bad()
		}
		key := path[len(path)-1]
		if _, exists := parent[key]; !exists {
			return bad()
		}
		parent[key] = ""
	}
	raw, err = json.Marshal(root)
	if err != nil {
		return Config{}, err
	}
	var out Config
	if err = json.Unmarshal(raw, &out); err != nil {
		return Config{}, err
	}
	return out, nil
}

func clearableSecretPath(path []string) bool {
	if len(path) >= 4 && path[0] == "plugins" && path[2] == "config" {
		return IsSecretKey(path[len(path)-1])
	}
	if len(path) == 4 && path[0] == "wifi" && path[1] == "aps" && path[3] == "psk" {
		return true
	}
	switch strings.Join(path, ".") {
	case "wan.pppoe.password", "ddns.cloudflare.apiToken", "ddns.duckdns.token", "ddns.aliyun.accessKey", "ddns.aliyun.secretKey", "ddns.custom.token":
		return true
	}
	return false
}
