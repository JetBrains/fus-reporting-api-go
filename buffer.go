package fus

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const (
	bufferFile       = "fus_buffer.jsonl"
	defaultMaxEvents = 1000
)

// Buffer is a file-based JSONL event buffer with flock concurrency safety.
type Buffer struct {
	path    string
	maxSize int
}

func NewBuffer(dataDir string, maxSize int) *Buffer {
	if maxSize <= 0 {
		maxSize = defaultMaxEvents
	}
	return &Buffer{
		path:    filepath.Join(dataDir, bufferFile),
		maxSize: maxSize,
	}
}

func (b *Buffer) Append(event LogEvent) error {
	if err := os.MkdirAll(filepath.Dir(b.path), 0o700); err != nil {
		return fmt.Errorf("create buffer dir: %w", err)
	}

	f, err := os.OpenFile(b.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open buffer: %w", err)
	}
	defer f.Close()

	if err := lockFile(f); err != nil {
		return fmt.Errorf("lock buffer: %w", err)
	}
	defer unlockFile(f)

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	data = append(data, '\n')

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	return nil
}

// ReadAndClear atomically reads all events and truncates the buffer.
func (b *Buffer) ReadAndClear() ([]LogEvent, error) {
	f, err := os.OpenFile(b.path, os.O_RDWR, 0o600)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open buffer: %w", err)
	}
	defer f.Close()

	if err := lockFile(f); err != nil {
		return nil, fmt.Errorf("lock buffer: %w", err)
	}
	defer unlockFile(f)

	events := scanEvents(f)

	if err := f.Truncate(0); err != nil {
		return events, fmt.Errorf("truncate buffer: %w", err)
	}

	return events, nil
}

// Trim keeps only the last maxSize events, discarding the oldest.
func (b *Buffer) Trim() error {
	f, err := os.OpenFile(b.path, os.O_RDWR, 0o600)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open buffer for trim: %w", err)
	}
	defer f.Close()

	if err := lockFile(f); err != nil {
		return fmt.Errorf("lock buffer: %w", err)
	}
	defer unlockFile(f)

	events := scanEvents(f)
	if len(events) <= b.maxSize {
		return nil
	}

	events = events[len(events)-b.maxSize:]

	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("truncate buffer: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("seek buffer: %w", err)
	}

	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			continue
		}
		data = append(data, '\n')
		if _, err := f.Write(data); err != nil {
			return fmt.Errorf("write trimmed event: %w", err)
		}
	}
	return nil
}

func scanEvents(f *os.File) []LogEvent {
	var events []LogEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var event LogEvent
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		events = append(events, event)
	}
	return events
}

func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
