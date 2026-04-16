package fus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func isRetryable(code int) bool {
	switch code {
	case 408, 429, 500, 502, 503, 504, 598:
		return true
	}
	return false
}

type Client struct {
	endpoint   string
	userAgent  string
	httpClient *http.Client
}

func NewClient(endpoint string, timeout time.Duration, userAgent string) *Client {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Client{
		endpoint:  endpoint,
		userAgent: userAgent,
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
			timer := time.NewTimer(retryInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}

		err := c.doPost(ctx, data)
		if err == nil {
			return nil
		}
		lastErr = err

		if statusErr, ok := errors.AsType[*statusError](err); ok && isRetryable(statusErr.code) {
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
		end := min(i+maxBatchSize, len(events))
		if err := c.Send(ctx, Report{Events: events[i:end]}); err != nil {
			return sent, err
		}
		sent += end - i
	}
	return sent, nil
}
