package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
)

const secretReferencePrefix = "__router_secret_ref_v1_"

// IsSecretKey is shared by plugin redaction, preservation and explicit intent.
func IsSecretKey(key string) bool {
	key = strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(key))
	for _, marker := range []string{"password", "passwd", "psk", "secret", "token", "authkey", "privatekey", "accesskey", "apikey"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func secretReference(revision string, path []string) string {
	encoded, _ := json.Marshal(path)
	digest := sha256.Sum256(append([]byte(revision+"\x00"), encoded...))
	return secretReferencePrefix + hex.EncodeToString(digest[:]) + "__"
}

func copySecretValue(v interface{}) interface{} {
	switch value := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(value))
		for k, x := range value {
			out[k] = copySecretValue(x)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(value))
		for i, x := range value {
			out[i] = copySecretValue(x)
		}
		return out
	default:
		return v
	}
}

func redactPluginValue(v interface{}, revision string, path []string, secret bool, refs map[string]interface{}) interface{} {
	if secret && v != nil && v != "" {
		ref := secretReference(revision, path)
		if refs != nil {
			refs[ref] = v
		}
		return ref
	}
	switch value := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(value))
		for k, x := range value {
			out[k] = redactPluginValue(x, revision, append(append([]string{}, path...), k), IsSecretKey(k), refs)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(value))
		for i, x := range value {
			out[i] = redactPluginValue(x, revision, append(append([]string{}, path...), strconv.Itoa(i)), false, refs)
		}
		return out
	default:
		return v
	}
}

func redactPlugins(c Config, refs map[string]interface{}) map[string]PluginConfig {
	if c.Plugins == nil {
		return nil
	}
	out := make(map[string]PluginConfig, len(c.Plugins))
	for id, p := range c.Plugins {
		if p.Config != nil {
			p.Config = redactPluginValue(p.Config, c.Revision, []string{"plugins", id, "config"}, false, refs).(map[string]interface{})
		}
		out[id] = p
	}
	return out
}

// publicShape matches array items only when all non-secret fields agree. It
// never guesses by array position, and duplicate shapes remain ambiguous.
func publicShape(v interface{}) interface{} {
	switch x := v.(type) {
	case map[string]interface{}:
		out := map[string]interface{}{}
		for k, value := range x {
			if !IsSecretKey(k) {
				out[k] = publicShape(value)
			}
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(x))
		for i, value := range x {
			out[i] = publicShape(value)
		}
		return out
	default:
		return v
	}
}

func preserveArraySecrets(old, incoming interface{}, inArray, ambiguous bool) (interface{}, error) {
	switch value := incoming.(type) {
	case map[string]interface{}:
		previous, _ := old.(map[string]interface{})
		out := map[string]interface{}{}
		for key, item := range value {
			if inArray && IsSecretKey(key) && (item == nil || item == "" || item == "****") {
				if saved, exists := previous[key]; exists {
					out[key] = copySecretValue(saved)
					continue
				}
				if ambiguous || item == "****" {
					return nil, invalidField("plugins", "ambiguous array secret; keep its reference or use explicit clearing")
				}
			}
			next, err := preserveArraySecrets(previous[key], item, inArray, ambiguous)
			if err != nil {
				return nil, err
			}
			out[key] = next
		}
		if inArray {
			for key, item := range previous {
				if _, exists := out[key]; !exists && IsSecretKey(key) {
					out[key] = copySecretValue(item)
				}
			}
		}
		return out, nil
	case []interface{}:
		previous, _ := old.([]interface{})
		type match struct {
			count int
			value interface{}
		}
		candidates := map[string]match{}
		for _, candidate := range previous {
			shape, _ := json.Marshal(publicShape(candidate))
			entry := candidates[string(shape)]
			entry.count++
			entry.value = candidate
			candidates[string(shape)] = entry
		}
		out := make([]interface{}, len(value))
		for i, item := range value {
			shape, _ := json.Marshal(publicShape(item))
			entry := candidates[string(shape)]
			matches, matched := entry.count, entry.value
			if matches != 1 {
				matched = nil
			}
			next, err := preserveArraySecrets(matched, item, true, ambiguous || (matches != 1 && len(previous) > 0))
			if err != nil {
				return nil, err
			}
			out[i] = next
		}
		return out, nil
	default:
		return incoming, nil
	}
}

// ResolvePluginSecrets uses only the current draft's reference table. Callers
// must compare If-Match before calling, and publish only after full validation.
func ResolvePluginSecrets(old Config, incoming map[string]PluginConfig) (map[string]PluginConfig, error) {
	refs := map[string]interface{}{}
	redactPlugins(old, refs)
	var resolve func(interface{}, bool) (interface{}, error)
	resolve = func(v interface{}, secret bool) (interface{}, error) {
		if str, ok := v.(string); ok && strings.HasPrefix(str, secretReferencePrefix) {
			original, found := refs[str]
			if !secret || !found {
				return nil, invalidField("plugins", "invalid or expired secret reference; reload configuration")
			}
			return copySecretValue(original), nil
		}
		switch value := v.(type) {
		case map[string]interface{}:
			out := make(map[string]interface{}, len(value))
			for k, x := range value {
				next, err := resolve(x, IsSecretKey(k))
				if err != nil {
					return nil, err
				}
				out[k] = next
			}
			return out, nil
		case []interface{}:
			out := make([]interface{}, len(value))
			for i, x := range value {
				next, err := resolve(x, false)
				if err != nil {
					return nil, err
				}
				out[i] = next
			}
			return out, nil
		default:
			return v, nil
		}
	}
	if incoming == nil {
		return nil, nil
	}
	out := make(map[string]PluginConfig, len(incoming))
	for id, p := range incoming {
		if p.Config != nil {
			prepared, err := preserveArraySecrets(old.Plugins[id].Config, p.Config, false, false)
			if err != nil {
				return nil, err
			}
			value, err := resolve(prepared, false)
			if err != nil {
				return nil, err
			}
			p.Config = value.(map[string]interface{})
		}
		out[id] = p
	}
	return out, nil
}
