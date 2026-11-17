package config

import (
	"github.com/dnfn-tech/nixos-router/backend/internal/modules"
	"reflect"
)

// ValidateModuleEdits distinguishes archival compatibility from accepting new
// invalid data. Read-only legacy loading deliberately does not call this.
func (c Config) ValidateModuleEdits(previous Config) error {
	for id, pc := range c.Plugins {
		old, existed := previous.Plugins[id]
		if !existed && !modules.ValidID(id) {
			return invalidField("plugins", "invalid module ID")
		}
		if _, known := modules.Lookup(id); known && (!existed || !reflect.DeepEqual(old.Config, pc.Config)) {
			if err := modules.ValidateConfig(id, pc.Config); err != nil {
				if field, ok := err.(*modules.FieldError); ok {
					return invalidField(field.Path, field.Message)
				}
				return err
			}
		}
	}
	return nil
}
