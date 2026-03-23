package fus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	defaultTimeout  = 10 * time.Second
	maxBatchSize    = 500
	contentTypeJSON = "application/json"

	retryInterval = 500 * time.Millisecond
	maxRetries    = 10
)

var retryableStatusCodes = map[int]bool{
	408: true, // Request Timeout
	429: true, // Too Many Requests
	500: true, // Internal Server Error
	502: true, // Bad Gateway
	503: true, // Service Unavailable
	504: true, // Gateway Timeout
	598: true, // Network Read Timeout
}

type Client struct {
	endpoint   string
	userAgent  string
	httpClient *http.Client
}

func NewClient(endpoint string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Client{
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Send posts a report with retry on transient HTTP errors.
func (c *Client) Send(ctx context.Context, report Report) error {
	if len(report.Events) == 0 {
		return nil
	}

	data, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}

	var lastErr error
	for attempt := range maxRetries + 1 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryInterval):
			}
		}

		err := c.doPost(ctx, data)
		if err == nil {
			return nil
		}
		lastErr = err

		if statusErr, ok := err.(*statusError); ok && retryableStatusCodes[statusErr.code] {
			continue
		}
		return err
	}

	return lastErr
}

func (c *Client) doPost(ctx context.Context, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", contentTypeJSON)
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send report: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &statusError{code: resp.StatusCode}
	}

	return nil
}

type statusError struct {
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("fus endpoint returned %d", e.code)
}

// SendBatched sends events in batches of up to 500.
// Returns the number of events successfully sent before the first error.
func (c *Client) SendBatched(ctx context.Context, events []LogEvent) (int, error) {
	sent := 0
	for i := 0; i < len(events); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(events) {
			end = len(events)
		}
		if err := c.Send(ctx, Report{Events: events[i:end]}); err != nil {
			return sent, err
		}
		sent += end - i
	}
	return sent, nil
}
