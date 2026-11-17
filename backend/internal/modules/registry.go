// Package modules defines compiled-in, side-effect-free feature contracts.
// Activation belongs to the core transaction; modules cannot execute commands.
package modules

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
)

type Descriptor struct {
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	Version      string          `json:"version"`
	Path         string          `json:"path,omitempty"`
	Runtime      bool            `json:"runtimeSupported"`
	Ownership    string          `json:"ownership"`
	Capabilities []string        `json:"capabilities"`
	ConfigKeys   []string        `json:"configKeys,omitempty"`
	ConfigSchema json.RawMessage `json:"configSchema,omitempty"`
	validate     func(map[string]interface{}, bool) error
	renderDNS    func(map[string]interface{}) (string, error)
}

type Registry struct{ items map[string]Descriptor }

var validID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

func ValidID(id string) bool { return validID.MatchString(id) }

// ValidateConfig checks editable structure even when the module is disabled.
// System-owned/unsupported configurations remain opaque archival data.
func ValidateConfig(id string, values map[string]interface{}) error {
	d, ok := Lookup(id)
	if !ok || d.validate == nil {
		return nil
	}
	return d.validate(values, true)
}

func NewRegistry(items []Descriptor) (*Registry, error) {
	r := &Registry{items: map[string]Descriptor{}}
	paths := map[string]bool{}
	for _, d := range items {
		if !validID.MatchString(d.ID) {
			return nil, fmt.Errorf("invalid module ID")
		}
		if _, exists := r.items[d.ID]; exists {
			return nil, fmt.Errorf("duplicate module ID")
		}
		if d.Path != "" {
			if paths[d.Path] {
				return nil, fmt.Errorf("conflicting module route")
			}
			paths[d.Path] = true
		}
		d.ConfigSchema = append(json.RawMessage(nil), d.ConfigSchema...)
		d.Capabilities = append([]string{}, d.Capabilities...)
		d.ConfigKeys = append([]string{}, d.ConfigKeys...)
		r.items[d.ID] = d
	}
	return r, nil
}

var builtins = mustRegistry([]Descriptor{
	{ID: "adblock", Title: "广告拦截", Version: "1.0.0", Path: "/adblock", Runtime: true, Ownership: "router", Capabilities: []string{"managed-dns"}, ConfigKeys: []string{"entries"},
		ConfigSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"entries":{"type":"array","maxItems":4096,"items":{"type":"string","minLength":1,"maxLength":254}},"lists":{"type":"array","maxItems":0}}}`),
		validate: func(values map[string]interface{}, dns bool) error {
			if !dns {
				return invalid("adblock", "enable", "requires router-managed local DNS")
			}
			_, err := RenderDNS(values)
			return err
		}, renderDNS: RenderDNS},
	{ID: "traffic", Title: "流量统计", Version: "0.1.0", Path: "/traffic", Ownership: "unsupported"},
	{ID: "mihomo", Title: "Mihomo", Version: "0.1.0", Path: "/mihomo", Ownership: "system"},
	{ID: "vlan", Title: "VLAN", Version: "0.1.0", Path: "/vlan", Ownership: "unsupported"},
	{ID: "tailscale", Title: "Tailscale", Version: "0.1.0", Path: "/tailscale", Ownership: "system"},
	{ID: "zerotier", Title: "ZeroTier", Version: "0.1.0", Path: "/zerotier", Ownership: "system"},
})

func mustRegistry(items []Descriptor) *Registry {
	r, e := NewRegistry(items)
	if e != nil {
		panic(e)
	}
	return r
}
func Lookup(id string) (Descriptor, bool) {
	d, ok := builtins.items[id]
	d.ConfigSchema = append(json.RawMessage(nil), d.ConfigSchema...)
	d.Capabilities = append([]string{}, d.Capabilities...)
	d.ConfigKeys = append([]string{}, d.ConfigKeys...)
	return d, ok
}
func All() []Descriptor {
	result := []Descriptor{}
	for id := range builtins.items {
		d, _ := Lookup(id)
		result = append(result, d)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// FieldError is independent of the configuration package to avoid a dependency
// cycle. The config boundary adapts it to its public typed validation error.
type FieldError struct{ Path, Message string }

func (e *FieldError) Error() string { return e.Path + ": " + e.Message }
func invalid(id, path, message string) error {
	if path != "" {
		path = "." + path
	}
	return &FieldError{Path: "plugins." + id + path, Message: message}
}

func Validate(id string, enabled bool, values map[string]interface{}, localDNS bool) error {
	// Disabled historical IDs are opaque, never routed or executed.
	if !enabled {
		return nil
	}
	if !validID.MatchString(id) {
		return invalid("", "", "invalid module ID")
	}
	d, ok := Lookup(id)
	if !ok || !d.Runtime {
		return invalid(id, "enable", "module is not available for router-managed runtime")
	}
	if d.validate == nil {
		return invalid(id, "enable", "module implementation unavailable")
	}
	return d.validate(values, localDNS)
}

func ValidateAllowed(ids []string) error {
	seen := map[string]bool{}
	for _, id := range ids {
		d, ok := Lookup(id)
		if !ok || !d.Runtime || seen[id] {
			return fmt.Errorf("invalid, duplicate or unsupported allowed module")
		}
		seen[id] = true
	}
	return nil
}

// Render returns DNS contributions from a compiled-in implementation only.
// The caller validates the whole configuration before beginning a transaction.
func Render(id string, values map[string]interface{}) (string, error) {
	d, ok := Lookup(id)
	if !ok || d.renderDNS == nil {
		return "", invalid(id, "config", "module DNS contribution unavailable")
	}
	return d.renderDNS(values)
}
