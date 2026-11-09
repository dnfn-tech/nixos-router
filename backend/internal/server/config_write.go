package server

import (
	"encoding/json"
	"errors"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

// Clearing is request metadata, never part of a persisted/backup Config.
func decodeConfigWrite(data []byte) (config.Config, [][]string, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return config.Config{}, nil, errors.New("invalid configuration document")
	}
	if _, ok := keys["clearSecrets"]; !ok {
		c, err := config.DecodeDocument(data)
		return c, nil, err
	}
	var request struct {
		Config       *config.Config `json:"config"`
		ClearSecrets [][]string     `json:"clearSecrets"`
		Kind         string         `json:"kind,omitempty"`
		Format       string         `json:"format,omitempty"`
		CreatedAt    string         `json:"createdAt,omitempty"`
		Path         string         `json:"path,omitempty"`
	}
	if err := config.DecodeStrictJSON(data, &request); err != nil {
		return config.Config{}, nil, err
	}
	if request.Config == nil {
		return config.Config{}, nil, errors.New("missing configuration object")
	}
	return *request.Config, request.ClearSecrets, nil
}
