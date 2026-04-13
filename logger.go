package fus

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

const defaultFlushTimeout = 2 * time.Second

// MaxDataFields is the per-event data-field cap enforced by the FUS analytics UI.
// Track drops events that exceed this limit.
const MaxDataFields = 10

// Logger buffers FUS events to disk and sends them on Flush.
type Logger struct {
	config    RecorderConfig
	fusConfig *FUSConfig
	buffer    *Buffer
	client    *Client
	deviceID  string
	session   string
	bucket    int
	mu        sync.Mutex
}

type LoggerOption func(*Logger)

// WithFUSConfig overrides automatic config fetching.
func WithFUSConfig(cfg *FUSConfig) LoggerOption {
	return func(l *Logger) { l.fusConfig = cfg }
}

// WithClient overrides the default HTTP client.
func WithClient(c *Client) LoggerOption {
	return func(l *Logger) { l.client = c }
}

func NewLogger(cfg RecorderConfig, opts ...LoggerOption) (*Logger, error) {
	deviceID := cfg.DeviceID
	if deviceID == "" {
		var err error
		deviceID, err = GetOrCreateDeviceID(cfg.DataDir)
		if err != nil {
			return nil, err
		}
	}

	sessionID, err := generateUUID()
	if err != nil {
		return nil, err
	}

	l := &Logger{
		config:   cfg,
		buffer:   NewBuffer(cfg.DataDir, defaultMaxEvents),
		deviceID: deviceID,
		bucket:   ComputeBucket(deviceID),
	}

	for _, opt := range opts {
		opt(l)
	}

	if l.fusConfig == nil {
		region := cfg.Region
		if region == "" {
			region = RegionAll
		}
		fc, err := LoadOrFetchConfig(cfg.RecorderID, cfg.ProductCode, cfg.BuildVersion, cfg.DataDir, region)
		if err != nil {
			return nil, err
		}
		l.fusConfig = fc
	}
	if l.fusConfig.Salt == "" {
		return nil, errors.New("fus: config salt is empty; refusing to emit events with predictable hashes")
	}
	if l.client == nil {
		l.client = NewClient(l.fusConfig.SendEndpoint, defaultTimeout)
		l.client.userAgent = cfg.ProductCode + "/" + cfg.BuildVersion
	}

	l.session = Anonymize([]byte(l.fusConfig.Salt), sessionID)

	_ = l.buffer.Trim()

	return l, nil
}

// Track buffers an event to disk. Failures are silent. Events with more than
// MaxDataFields entries are dropped. String fields are sanitized for LION v4
// wire safety: ' " dropped, CR/LF/TAB and separator chars replaced, non-ASCII
// runes replaced with ?.
func (l *Logger) Track(group EventGroup, eventID string, data map[string]any) {
	if len(data) > MaxDataFields {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	salt := []byte(l.fusConfig.Salt)

	event := LogEvent{
		Recorder: Recorder{
			ID:      l.config.RecorderID,
			Version: l.config.RecorderVersion,
		},
		Product:  l.config.ProductCode,
		IDs:      map[string]string{"device": Anonymize(salt, l.deviceID)},
		Internal: l.config.Internal,
		Time:     time.Now().UnixMilli(),
		Build:    l.config.BuildVersion,
		Session:  l.session,
		Group:    group,
		Bucket:   l.bucket,
		Event:    EventAction{ID: eventID, Data: data, Count: 1},
	}

	event = escapeEvent(event)
	_ = l.buffer.Append(event)
}

// escapeEvent applies StatisticsEventEscaper rules to every caller-sourced
// string field in a LION v4 event.
func escapeEvent(e LogEvent) LogEvent {
	e.Recorder.ID = Escape(e.Recorder.ID)
	e.Product = Escape(e.Product)
	e.IDs = EscapeIDs(e.IDs)
	e.Build = Escape(e.Build)
	e.Group.ID = Escape(e.Group.ID)
	e.Event.ID = EscapeEventIDOrFieldValue(e.Event.ID)
	e.Event.Data = EscapeEventData(e.Event.Data)
	return e
}

// Flush sends all buffered events, merging consecutive duplicates.
func (l *Logger) Flush(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	events, err := l.buffer.ReadAndClear()
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}

	events = mergeEvents(events)

	sent, err := l.client.SendBatched(ctx, events)
	if err != nil {
		for _, e := range events[sent:] {
			_ = l.buffer.Append(e)
		}
		return err
	}

	return nil
}

func (l *Logger) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultFlushTimeout)
	defer cancel()
	return l.Flush(ctx)
}

// mergeEvents combines consecutive events where all fields match except time and count.
func mergeEvents(events []LogEvent) []LogEvent {
	if len(events) <= 1 {
		return events
	}

	merged := make([]LogEvent, 0, len(events))
	merged = append(merged, events[0])

	for i := 1; i < len(events); i++ {
		last := &merged[len(merged)-1]
		next := events[i]

		if canMerge(last, &next) {
			last.Event.Count += next.Event.Count
		} else {
			merged = append(merged, next)
		}
	}

	return merged
}

func canMerge(a, b *LogEvent) bool {
	return a.Recorder == b.Recorder &&
		a.Product == b.Product &&
		a.Internal == b.Internal &&
		a.Build == b.Build &&
		a.Session == b.Session &&
		a.Group == b.Group &&
		a.Bucket == b.Bucket &&
		a.Event.ID == b.Event.ID &&
		stringMapsEqual(a.IDs, b.IDs) &&
		anyMapsEqual(a.Event.Data, b.Event.Data) &&
		anyMapsEqual(a.SystemData, b.SystemData) &&
		anyMapsEqual(a.ClientData, b.ClientData)
}

func stringMapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func anyMapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}
