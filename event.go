package fus

type Recorder struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

type EventGroup struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	State   bool   `json:"state"`
}

type EventAction struct {
	ID    string         `json:"id"`
	Data  map[string]any `json:"data,omitempty"`
	Count int            `json:"count"`
}

// LogEvent is a single LION v4 wire-format event.
type LogEvent struct {
	Recorder   Recorder          `json:"recorder"`
	Product    string            `json:"product"`
	IDs        map[string]string `json:"ids"`
	Internal   bool              `json:"internal"`
	Time       int64             `json:"time"`
	Build      string            `json:"build"`
	Session    string            `json:"session"`
	Group      EventGroup        `json:"group"`
	Bucket     int               `json:"bucket"`
	Event      EventAction       `json:"event"`
	SystemData map[string]any    `json:"system_data,omitempty"`
	ClientData map[string]any    `json:"client_data,omitempty"`
}

type Report struct {
	Events []LogEvent `json:"events"`
}

// Group returns a counter EventGroup (State=false).
func Group(id string, version int) EventGroup {
	return EventGroup{ID: id, Version: version}
}

// StateGroup returns a state EventGroup (State=true).
func StateGroup(id string, version int) EventGroup {
	return EventGroup{ID: id, Version: version, State: true}
}
