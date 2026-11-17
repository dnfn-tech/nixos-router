package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/db"
	"github.com/dnfn-tech/nixos-router/backend/internal/managed"
	"github.com/dnfn-tech/nixos-router/backend/internal/privsep"
)

type remoteRuntime interface {
	ApplyJob(context.Context, string, config.Config) (managed.Result, error)
	Job(context.Context, string) (privsep.Job, error)
}

// Reconcile durable jobs on reads as well as in the live worker. An API restart
// cannot manufacture an executor failure, and a lost response cannot mean success.
func (s *Server) reconcileRemoteJob(ctx context.Context, j db.Job) (db.Job, error) {
	if j.Kind != "apply" || j.Status != "running" {
		return j, nil
	}
	var marker struct {
		Executor       bool   `json:"executor"`
		ConfigRevision string `json:"configRevision"`
	}
	if json.Unmarshal([]byte(j.Payload), &marker) != nil || !marker.Executor {
		return j, nil
	}
	runtime, ok := s.managedRuntime.(remoteRuntime)
	if !ok {
		return j, fmt.Errorf("executor is unavailable")
	}
	remote, err := runtime.Job(ctx, j.ID)
	if err != nil {
		var rpc *privsep.RPCError
		if errors.As(err, &rpc) && rpc.Status == 404 && rpc.Code == "job_not_found" {
			return unknownRemoteJob(j, "not-found"), nil
		}
		return j, err
	}
	if remote.ID != j.ID {
		return j, fmt.Errorf("executor returned different job")
	}
	switch remote.Status {
	case "running":
		return j, nil
	case "success", "failed", "interrupted":
	default:
		return j, fmt.Errorf("executor returned unknown status")
	}
	status := remote.Status
	if status == "interrupted" {
		status = "failed"
	}
	payload := map[string]interface{}{"jobId": j.ID, "mode": "managed-runtime", "executor": true, "configRevision": marker.ConfigRevision, "status": status, "executorStatus": remote.Status, "stage": remote.Result.Stage, "appliedRuntime": remote.Status == "success", "rollbackStatus": remote.Result.RollbackStatus, "warnings": remote.Result.Warnings}
	if remote.Error != "" {
		payload["error"] = remote.Error
	}
	body, _ := json.Marshal(payload)
	if err = s.db.FinishJob(j.ID, status, string(body), remote.Error); err != nil {
		return j, err
	}
	updated, ok, err := s.db.GetJob(j.ID)
	if err != nil {
		return j, err
	}
	if !ok {
		return j, fmt.Errorf("API job disappeared")
	}
	return updated, nil
}

// This is an observation, not a durable transition: a concurrent submission or
// later query may still reveal the real result. Never replay or overwrite it.
func unknownRemoteJob(j db.Job, reason string) db.Job {
	j.Status = "unknown"
	var original struct {
		ConfigRevision string `json:"configRevision"`
	}
	_ = json.Unmarshal([]byte(j.Payload), &original)
	body, _ := json.Marshal(map[string]interface{}{
		"jobId": j.ID, "mode": "managed-runtime", "executor": true,
		"configRevision": original.ConfigRevision, "status": "unknown", "stage": "outcome-unknown", "executorStatus": reason,
		"warnings": []string{"执行结果尚未确认，请刷新作业记录并核对实际运行配置；系统不会自动重试应用。"},
	})
	j.Payload = string(body)
	return j
}

// Both the client list and overview must use the same observer after privilege
// separation. An executor read failure is never an empty measured client list.
func (s *Server) leaseSnapshot(ctx context.Context) ([]managed.Lease, string, error) {
	if observer, ok := s.managedRuntime.(interface {
		Leases(context.Context) ([]managed.Lease, error)
	}); ok {
		leases, err := observer.Leases(ctx)
		if err != nil {
			return nil, "unavailable", err
		}
		return leases, "leases", nil
	}
	leases := s.readDnsmasqLeases()
	if len(leases) > 0 {
		return leases, "leases", nil
	}
	return leases, "stub", nil
}
