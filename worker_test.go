package main

import (
	"encoding/json"
	"testing"
)

func TestWorkerJobRoundTrip(t *testing.T) {
	in := workerJob{
		Mode:       "receive",
		Code:       "1234-alpha-beta-gamma",
		StagingDir: `C:\Users\someone\Downloads\.krokodyl-partial-x`,
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out workerJob
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Mode != in.Mode || out.Code != in.Code || out.StagingDir != in.StagingDir {
		t.Errorf("round trip mismatch: %+v != %+v", out, in)
	}
}

func TestWorkerEventRoundTrip(t *testing.T) {
	in := workerEvent{
		Type:     "progress",
		Sent:     512,
		Size:     1024,
		Progress: 50,
		Speed:    256,
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out workerEvent
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Type != in.Type || out.Sent != in.Sent || out.Size != in.Size ||
		out.Progress != in.Progress || out.Speed != in.Speed {
		t.Errorf("round trip mismatch: %+v != %+v", out, in)
	}
}

func TestProgressPercent(t *testing.T) {
	tests := []struct {
		name  string
		sent  int64
		total int64
		want  int
	}{
		{"zero total returns 0", 100, 0, 0},
		{"zero sent returns 0", 0, 1000, 0},
		{"halfway", 500, 1000, 50},
		{"complete caps at 99 until done event", 1000, 1000, 99},
		{"overshoot caps at 99", 2000, 1000, 99},
		{"negative sent returns 0", -1, 1000, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := progressPercent(tt.sent, tt.total); got != tt.want {
				t.Errorf("progressPercent(%d, %d) = %d, want %d", tt.sent, tt.total, got, tt.want)
			}
		})
	}
}

func TestHasWorkerFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"no args", nil, false},
		{"unrelated args", []string{"--verbose"}, false},
		{"flag present", []string{workerFlag}, true},
		{"flag among others", []string{"--verbose", workerFlag}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasWorkerFlag(tt.args); got != tt.want {
				t.Errorf("hasWorkerFlag(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}
