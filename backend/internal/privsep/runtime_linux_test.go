package privsep

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestApplyJobUsesCallerIDAndPreservesUnknownOutcome(t *testing.T) {
	r := &protocolRunner{entered: make(chan struct{}), release: make(chan struct{})}
	m, cfg, state := protocolFixture(t, r)
	_, c, stop := protocolService(t, m, state)
	defer func() {
		select {
		case <-r.release:
		default:
			close(r.release)
		}
		stop()
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := c.ApplyJob(ctx, "api-job", cfg); result <- err }()
	<-r.entered
	cancel()
	var unknown *OutcomeUnknownError
	if err := <-result; !errors.As(err, &unknown) {
		t.Fatalf("uncertain transaction reported terminal: %v", err)
	}
	j, err := c.Job(context.Background(), "api-job")
	if err != nil || j.Status != "running" {
		t.Fatalf("ID lost: %+v %v", j, err)
	}
	close(r.release)
	if j = awaitJob(t, c, "api-job"); j.Status != "success" {
		t.Fatal(j)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	res, err := c.ApplyJob(ctx2, "api-job", cfg)
	if err != nil || res.Stage != "awaiting-wan-lease" {
		t.Fatalf("terminal result: %+v %v", res, err)
	}
}
