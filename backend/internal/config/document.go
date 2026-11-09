package config

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
)

const CurrentSchemaVersion = 1

// UnmarshalJSON makes every configuration ingress reject data it cannot retain.
// Plugin Config maps deliberately remain open; their values are not schema fields.
func (c *Config) UnmarshalJSON(data []byte) error {
	if b := bytes.TrimSpace(data); len(b) == 0 || b[0] != '{' {
		return fmt.Errorf("configuration must be a JSON object")
	}
	type plain Config
	var next plain
	if err := DecodeStrictJSON(data, &next); err != nil {
		return err
	}
	result := Config(next)
	if err := result.validateDocumentMetadata(); err != nil {
		return err
	}
	*c = result
	return nil
}

func (c Config) validateDocumentMetadata() error {
	if c.SchemaVersion != 0 && c.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("unsupported configuration schema version")
	}
	if c.Revision != "" {
		b, err := hex.DecodeString(c.Revision)
		if err != nil || len(b) != 16 || hex.EncodeToString(b) != c.Revision || c.SchemaVersion == 0 {
			return fmt.Errorf("invalid configuration revision")
		}
	}
	return nil
}

// Duplicate keys are ambiguous even in open plugin maps. Do not echo their
// names or values: malformed configuration can contain secrets anywhere.
func uniqueJSONKeys(d *json.Decoder, depth int) error {
	if depth > 256 {
		return fmt.Errorf("configuration nesting exceeds limit")
	}
	token, err := d.Token()
	if err != nil {
		return fmt.Errorf("invalid configuration JSON")
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return fmt.Errorf("invalid configuration JSON")
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate or invalid configuration key")
			}
			seen[name] = true
			if err := uniqueJSONKeys(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueJSONKeys(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("invalid configuration JSON")
	}
	if _, err := d.Token(); err != nil {
		return fmt.Errorf("invalid configuration JSON")
	}
	return nil
}

// DecodeDocument accepts the raw configuration and the existing backup/GET
// envelope. Metadata is explicit; it cannot hide misspelled configuration keys.
func DecodeDocument(data []byte) (Config, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Config{}, fmt.Errorf("invalid configuration JSON")
	}
	if _, wrapped := fields["config"]; !wrapped {
		var c Config
		err := json.Unmarshal(data, &c)
		return c, err
	}
	var envelope struct {
		Config    *Config `json:"config"`
		Kind      string  `json:"kind,omitempty"`
		Format    string  `json:"format,omitempty"`
		CreatedAt string  `json:"createdAt,omitempty"`
		Path      string  `json:"path,omitempty"`
	}
	if err := DecodeStrictJSON(data, &envelope); err != nil {
		return Config{}, err
	}
	if envelope.Config == nil {
		return Config{}, fmt.Errorf("configuration must be a JSON object")
	}
	return *envelope.Config, nil
}

// DecodeStrictJSON validates typed configuration documents and partial edits.
// Open map values preserve JSON numbers and exact user-defined keys.
func DecodeStrictJSON(data []byte, target interface{}) error {
	scan := json.NewDecoder(bytes.NewReader(data))
	scan.UseNumber()
	if err := uniqueJSONKeys(scan, 0); err != nil {
		return err
	}
	if _, err := scan.Token(); err != io.EOF {
		return fmt.Errorf("unexpected trailing configuration data")
	}
	if err := exactJSONFields(data, reflect.TypeOf(target)); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	return nil
}

// encoding/json matches struct names case-insensitively. Enforce exact tags at
// typed boundaries, but preserve case-sensitive keys in open plugin maps.
func exactJSONFields(data []byte, typ reflect.Type) error {
	if typ.Kind() == reflect.Pointer {
		return exactJSONFields(data, typ.Elem())
	}
	switch typ.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
			return fmt.Errorf("expected configuration object")
		}
		known := map[string]reflect.Type{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "" {
				name = f.Name
			}
			if name != "-" && f.IsExported() {
				known[name] = f.Type
			}
		}
		for key, raw := range fields {
			field, ok := known[key]
			if !ok {
				return fmt.Errorf("unknown configuration field")
			}
			// Optional pointers can be null. Struct values cannot silently become zero.
			if field.Kind() == reflect.Pointer && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				continue
			}
			if err := exactJSONFields(raw, field); err != nil {
				return err
			}
		}
	case reflect.Map:
		var values map[string]json.RawMessage
		if err := json.Unmarshal(data, &values); err != nil {
			return fmt.Errorf("expected configuration map")
		}
		for _, raw := range values {
			if err := exactJSONFields(raw, typ.Elem()); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		var values []json.RawMessage
		if err := json.Unmarshal(data, &values); err != nil {
			return fmt.Errorf("expected configuration array")
		}
		for _, raw := range values {
			if err := exactJSONFields(raw, typ.Elem()); err != nil {
				return err
			}
		}
	case reflect.Interface:
		return nil
	default:
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			return fmt.Errorf("configuration scalar cannot be null")
		}
	}
	return nil
}
