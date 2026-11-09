package privsep

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/managed"
)

// OutcomeUnknownError distinguishes observation failure from an executor result.
// Callers must keep the job queryable, not mark the network transaction failed.
type OutcomeUnknownError struct {
	ID    string
	Cause error
}

func (e *OutcomeUnknownError) Error() string {
	return fmt.Sprintf("executor outcome for %s is unknown: %v", e.ID, e.Cause)
}
func (e *OutcomeUnknownError) Unwrap() error { return e.Cause }

type RPCError struct {
	Status int
	Code   string
}

func (e *RPCError) Error() string { return fmt.Sprintf("executor HTTP %d: %s", e.Status, e.Code) }

func (c *Client) Apply(ctx context.Context, cfg config.Config) (managed.Result, error) {
	var id [24]byte
	if _, err := rand.Read(id[:]); err != nil {
		return managed.Result{}, err
	}
	return c.ApplyJob(ctx, hex.EncodeToString(id[:]), cfg)
}

// ApplyJob submits once with the API's durable identifier. Lost responses are
// resolved only by querying that identifier, never by creating a new job.
func (c *Client) ApplyJob(ctx context.Context, id string, cfg config.Config) (managed.Result, error) {
	j, err := c.Submit(ctx, id, cfg)
	var remote *RPCError
	if errors.As(err, &remote) && remote.Status >= 400 && remote.Status < 500 {
		return managed.Result{Stage: "submit", RollbackStatus: "not-needed"}, err
	}
	for {
		if err == nil {
			if j.ID != id {
				return managed.Result{}, &OutcomeUnknownError{id, errors.New("executor returned wrong job ID")}
			}
			switch j.Status {
			case "success":
				return j.Result, nil
			case "failed", "interrupted":
				return j.Result, fmt.Errorf("executor %s: %s", j.Status, j.Error)
			case "running":
			default:
				return managed.Result{}, &OutcomeUnknownError{id, errors.New("executor returned invalid job state")}
			}
		}
		select {
		case <-ctx.Done():
			return managed.Result{Stage: "outcome-unknown", RollbackStatus: "unknown"}, &OutcomeUnknownError{id, ctx.Err()}
		case <-time.After(100 * time.Millisecond):
		}
		j, err = c.Job(ctx, id)
	}
}
func (c *Client) AppliedConfig() (config.Config, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cfg, ok, err := c.Applied(ctx)
	return cfg, ok && err == nil
}
