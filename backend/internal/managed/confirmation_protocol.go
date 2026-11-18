package managed

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
)

type ConfirmationControl interface {
	Candidate(context.Context) (*Confirmation, error)
	Confirm(context.Context, string) (*Confirmation, error)
}

// DecodeConfirmationID accepts exactly one ID, with no duplicates or extra data.
func DecodeConfirmationID(r io.Reader) (string, error) {
	dec := json.NewDecoder(io.LimitReader(r, 4097))
	bad := errors.New("invalid confirmation request")
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return "", bad
	}
	key, err := dec.Token()
	if err != nil || key != "id" {
		return "", bad
	}
	var id string
	if dec.Decode(&id) != nil {
		return "", bad
	}
	tok, err = dec.Token()
	if err != nil || tok != json.Delim('}') {
		return "", bad
	}
	if _, err = dec.Token(); err != io.EOF {
		return "", bad
	}
	if len(id) != 48 {
		return "", bad
	}
	if _, err = hex.DecodeString(id); err != nil {
		return "", bad
	}
	return id, nil
}
