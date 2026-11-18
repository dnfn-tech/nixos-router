package managed

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

// Confirmation describes the current candidate, not a claim of connectivity.
type Confirmation struct {
	ID       string    `json:"id"`
	Revision string    `json:"revision"`
	State    string    `json:"state"`
	Deadline time.Time `json:"deadline"`
}
type candidateRecord struct {
	Confirmation
	Candidate        config.Config `json:"candidate"`
	Previous         config.Config `json:"previous"`
	PreviousDocument []byte        `json:"previousDocument"`
}

func (r *candidateRecord) unsettled() bool { return r.State != "confirmed" && r.State != "rolled-back" }
func (m *Manager) candidatePath() string   { return filepath.Join(m.base(), "candidate.json") }
func (m *Manager) readCandidate() (*candidateRecord, error) {
	b, err := os.ReadFile(m.candidatePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r candidateRecord
	if err = json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("invalid candidate journal: %w", err)
	}
	if len(r.ID) != 48 || r.Deadline.IsZero() {
		return nil, errors.New("invalid candidate identity")
	}
	switch r.State {
	case "activating", "pending", "confirming", "confirmed", "rolling-back", "rolled-back", "rollback-failed":
	default:
		return nil, errors.New("invalid candidate state")
	}
	if !r.unsettled() {
		return &r, nil
	}
	var original config.Config
	if len(r.PreviousDocument) == 0 || json.Unmarshal(r.PreviousDocument, &original) != nil {
		return nil, errors.New("invalid recovery document")
	}
	oldJSON, _ := json.Marshal(original)
	expectedJSON, _ := json.Marshal(r.Previous)
	if string(oldJSON) != string(expectedJSON) {
		return nil, errors.New("recovery document mismatch")
	}
	if err = m.validate(r.Previous); err != nil {
		return nil, fmt.Errorf("invalid recovery baseline: %w", err)
	}
	if err = m.validate(r.Candidate); err != nil {
		return nil, fmt.Errorf("invalid recovery candidate: %w", err)
	}
	return &r, nil
}
func (m *Manager) writeCandidate(r *candidateRecord) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return atomicFile(m.candidatePath(), append(b, '\n'))
}
func (m *Manager) beginCandidate(c, previous config.Config) (*candidateRecord, error) {
	var id [24]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	document, err := os.ReadFile(m.lastGood())
	if err != nil {
		return nil, err
	}
	r := &candidateRecord{Confirmation: Confirmation{ID: hex.EncodeToString(id[:]), Revision: c.Revision, State: "activating", Deadline: m.opts.Clock().Add(m.opts.ConfirmationTimeout)}, Candidate: clone(c), Previous: clone(previous), PreviousDocument: document}
	return r, m.writeCandidate(r)
}
func (m *Manager) armCandidate(r *candidateRecord) {
	if m.timer != nil {
		m.timer.Stop()
	}
	delay := r.Deadline.Sub(m.opts.Clock())
	if delay < 0 {
		delay = 0
	}
	m.timer = time.AfterFunc(delay, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if err := m.ExpireCandidate(ctx); err != nil {
			log.Printf("managed candidate timeout recovery failed: %v", err)
		}
	})
}

// Candidate is available through authenticated API even after an API restart.
func (m *Manager) Candidate(ctx context.Context) (*Confirmation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.readCandidate()
	if err != nil || r == nil {
		return nil, err
	}
	result := r.Confirmation
	return &result, nil
}
func (m *Manager) Confirm(ctx context.Context, id string) (*Confirmation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock, err := m.transactionLock()
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	r, err := m.readCandidate()
	if err != nil {
		return nil, err
	}
	if r == nil || r.ID != id {
		return nil, errors.New("candidate not found or superseded")
	}
	if r.State == "confirmed" {
		return &r.Confirmation, nil
	}
	if r.State != "pending" {
		return &r.Confirmation, errors.New("candidate cannot be confirmed")
	}
	if !m.opts.Clock().Before(r.Deadline) {
		recovery, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		return &r.Confirmation, errors.Join(errors.New("candidate expired"), m.rollbackCandidate(recovery, r))
	}
	r.State = "confirming"
	if err = m.writeCandidate(r); err != nil {
		return nil, err
	}
	b, _ := json.MarshalIndent(r.Candidate, "", "  ")
	err = atomicFile(m.lastGood(), append(b, '\n'))
	if err == nil {
		r.State = "confirmed"
		err = m.writeCandidate(r)
	}
	if err != nil {
		recovery, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		return nil, errors.Join(err, m.rollbackCandidate(recovery, r))
	}
	c := clone(r.Candidate)
	m.good = &c
	m.publish(&c)
	if m.timer != nil {
		m.timer.Stop()
	}
	return &r.Confirmation, nil
}

// ExpireCandidate is executor-owned and independent of request cancellation.
func (m *Manager) ExpireCandidate(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock, err := m.transactionLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	r, err := m.readCandidate()
	if err != nil || r == nil {
		return err
	}
	if !r.unsettled() {
		return nil
	}
	if r.State == "pending" && m.opts.Clock().Before(r.Deadline) {
		m.armCandidate(r)
		return nil
	}
	return m.rollbackCandidate(ctx, r)
}
func (m *Manager) rollbackCandidate(ctx context.Context, r *candidateRecord) error {
	if m.timer != nil {
		m.timer.Stop()
	}
	r.State = "rolling-back"
	if err := m.writeCandidate(r); err != nil {
		m.publish(nil)
		return err
	}
	// Restore the durable baseline first, including a crash during confirmation.
	err := atomicFile(m.lastGood(), r.PreviousDocument)
	if err == nil {
		c := clone(r.Previous)
		m.good = &c
		var dir string
		dir, err = m.prepare(ctx, c)
		if err == nil {
			err = m.activate(ctx, c, &r.Candidate, dir)
		}
	}
	if err != nil {
		r.State = "rollback-failed"
		m.publish(nil)
		return errors.Join(fmt.Errorf("candidate recovery failed: %w", err), m.writeCandidate(r))
	}
	r.State = "rolled-back"
	if err = m.writeCandidate(r); err != nil {
		m.publish(nil)
		return err
	}
	m.publish(&r.Previous)
	return nil
}

// Close stops this process's watchdog; the durable journal survives for Restore.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.timer != nil {
		m.timer.Stop()
	}
}
