package fus

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"reflect"
	"sync"
	"time"
)

const defaultFlushTimeout = 2 * time.Second

// MaxDataFields is the per-event data-field cap enforced by the FUS analytics UI.
// Track drops events that exceed this limit.
const MaxDataFields = 10

// DropReason describes why an event was silently dropped by Track.
type DropReason string

const (
	DropTooManyFields DropReason = "too_many_data_fields"
	DropValidation    DropReason = "validation_rejected"
	DropBufferError   DropReason = "buffer_write_failed"
)

// OnDropFunc is called when Track silently drops an event.
type OnDropFunc func(group, event string, reason DropReason)

// Logger buffers FUS events to disk and sends them on Flush.
type Logger struct {
	config     RecorderConfig
	fusConfig  *FUSConfig
	buf        *buffer
	client     *Client
	validator  *Validator
	anonymizer *Anonymizer
	onDrop     OnDropFunc
	deviceID   string
	session    string
	bucket     int
	mu         sync.Mutex
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

// WithValidator installs the client-side scheme validator. Required by
// NewLogger — every event is rewritten so that only scheme-approved keys
// and values reach the wire, and events whose group is registered but out
// of build/version range are dropped.
func WithValidator(v *Validator) LoggerOption {
	return func(l *Logger) { l.validator = v }
}

// WithAnonymizer installs the field anonymizer. When set, event_data fields
// declared in the scheme's anonymized_fields are hashed before buffering.
func WithAnonymizer(a *Anonymizer) LoggerOption {
	return func(l *Logger) { l.anonymizer = a }
}

// WithOnDrop registers a callback invoked when Track silently drops an event.
func WithOnDrop(fn OnDropFunc) LoggerOption {
	return func(l *Logger) { l.onDrop = fn }
}

func NewLogger(ctx context.Context, cfg RecorderConfig, opts ...LoggerOption) (*Logger, error) {
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
		buf:      newBuffer(cfg.DataDir, defaultMaxEvents),
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
		fc, err := loadOrFetchConfigCtx(ctx, cfg.RecorderID, cfg.ProductCode, cfg.BuildVersion, cfg.DataDir, region)
		if err != nil {
			return nil, err
		}
		l.fusConfig = fc
	}
	l.fusConfig.Salt = cmp.Or(cfg.AnonymizationSalt, l.fusConfig.Salt)
	if l.fusConfig.Salt == "" {
		return nil, errors.New("fus: anonymization salt is empty; set RecorderConfig.AnonymizationSalt")
	}
	if l.validator == nil {
		return nil, errors.New("fus: validator is required; pass WithValidator(fus.NewValidator(scheme))")
	}
	if l.client == nil {
		ua := cfg.ProductCode + "/" + cfg.BuildVersion
		l.client = NewClient(l.fusConfig.SendEndpoint, defaultTimeout, ua)
	}

	l.session = Anonymize([]byte(l.fusConfig.Salt), sessionID)

	_ = l.buf.Trim()

	return l, nil
}

// loadOrFetchConfigCtx wraps LoadOrFetchConfig, checking ctx before the
// potentially slow network fetch.
func loadOrFetchConfigCtx(ctx context.Context, recorderID, productCode, productVersion, dataDir string, region RegionCode) (*FUSConfig, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return LoadOrFetchConfig(recorderID, productCode, productVersion, dataDir, region)
}

// Track buffers an event to disk. Failures are silent unless an OnDrop
// callback is registered. Events with more than MaxDataFields entries are
// dropped.
//
// Pipeline: raw event -> validator -> anonymizer -> escaper -> disk buffer.
func (l *Logger) Track(group EventGroup, eventID string, data map[string]any) {
	if len(data) > MaxDataFields {
		if l.onDrop != nil {
			l.onDrop(group.ID, eventID, DropTooManyFields)
		}
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

	if l.validator != nil {
		validated, drop := l.validator.Validate(event)
		if drop {
			if l.onDrop != nil {
				l.onDrop(group.ID, eventID, DropValidation)
			}
			return
		}
		event = validated
	}

	if l.anonymizer != nil {
		l.anonymizer.AnonymizeEvent(&event)
	}

	event = escapeLogEvent(event)
	if err := l.buf.Append(event); err != nil && l.onDrop != nil {
		l.onDrop(group.ID, eventID, DropBufferError)
	}
}

// escapeLogEvent applies StatisticsEventEscaper rules to every caller-sourced
// string field in a LION v4 event.
func escapeLogEvent(e LogEvent) LogEvent {
	e.Recorder.ID = escape(e.Recorder.ID)
	e.Product = escape(e.Product)
	e.IDs = escapeIDs(e.IDs)
	e.Build = escape(e.Build)
	e.Group.ID = escape(e.Group.ID)
	e.Event.ID = escapeEventIDOrFieldValue(e.Event.ID)
	e.Event.Data = escapeEventData(e.Event.Data)
	return e
}

// Flush sends all buffered events, merging consecutive duplicates.
// The mutex is held only during buffer I/O, not during the HTTP send,
// so Track calls are not blocked by network latency.
func (l *Logger) Flush(ctx context.Context) error {
	l.mu.Lock()
	events, err := l.buf.ReadAndClear()
	l.mu.Unlock()

	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}

	events = mergeEvents(events)

	sent, err := l.client.SendBatched(ctx, events)
	if err != nil {
		l.mu.Lock()
		for _, e := range events[sent:] {
			_ = l.buf.Append(e)
		}
		l.mu.Unlock()
		return err
	}

	return nil
}

// Close flushes all buffered events. Pass a context with a deadline to bound
// the flush duration; use context.Background() for an unbounded flush.
func (l *Logger) Close(ctx context.Context) error {
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
		maps.Equal(a.IDs, b.IDs) &&
		reflect.DeepEqual(a.Event.Data, b.Event.Data) &&
		reflect.DeepEqual(a.SystemData, b.SystemData) &&
		reflect.DeepEqual(a.ClientData, b.ClientData)
}
