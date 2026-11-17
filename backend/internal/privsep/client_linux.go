package privsep

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/managed"
)

type Client struct {
	http      *http.Client
	transport *http.Transport
}

func NewClient(path string, serverUID uint32) *Client {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return DialContext(ctx, path, serverUID) }}
	return &Client{http: &http.Client{Transport: tr, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport: tr}
}
func (c *Client) Close() { c.transport.CloseIdleConnections() }
func (c *Client) Submit(ctx context.Context, id string, cfg config.Config) (Job, error) {
	var j Job
	err := c.call(ctx, "POST", "/v1/jobs", applyRequest{ID: id, Config: &cfg}, &j)
	return j, err
}
func (c *Client) Job(ctx context.Context, id string) (Job, error) {
	if !jobID.MatchString(id) {
		return Job{}, fmt.Errorf("invalid job ID")
	}
	var j Job
	err := c.call(ctx, "GET", "/v1/jobs/"+id, nil, &j)
	return j, err
}
func (c *Client) Applied(ctx context.Context) (config.Config, bool, error) {
	var res appliedResponse
	err := c.call(ctx, "GET", "/v1/applied", nil, &res)
	return res.Config, res.Available, err
}
func (c *Client) call(ctx context.Context, method, path string, payload, dst any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://executor"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 && res.StatusCode != 202 {
		var msg struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&msg)
		return &RPCError{Status: res.StatusCode, Code: msg.Error}
	}
	// Applied snapshots can contain the same full document accepted on submit.
	dec := json.NewDecoder(io.LimitReader(res.Body, maxRequestBytes))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func (c *Client) Leases(ctx context.Context) ([]managed.Lease, error) {
	var leases []managed.Lease
	err := c.call(ctx, "GET", "/v1/leases", nil, &leases)
	return leases, err
}
