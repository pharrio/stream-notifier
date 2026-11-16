package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Track struct {
	RID     string `json:"rid"`
	Packets uint64 `json:"packetsReceived"`
	Bitrate uint64 `json:"bitrate"`
}

func (t *Track) UnmarshalJSON(data []byte) error {
	var wire struct {
		RID     *string         `json:"rid"`
		Packets *uint64         `json:"packetsReceived"`
		Bitrate json.RawMessage `json:"bitrate"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.RID == nil || wire.Packets == nil {
		return fmt.Errorf("track requires rid and packetsReceived")
	}
	*t = Track{RID: *wire.RID, Packets: *wire.Packets}
	if wire.Bitrate != nil {
		if bytes.Equal(bytes.TrimSpace(wire.Bitrate), []byte("null")) {
			return fmt.Errorf("bitrate must be a number")
		}
		if err := json.Unmarshal(wire.Bitrate, &t.Bitrate); err != nil {
			return err
		}
	}
	return nil
}

type Stream struct {
	Key      string            `json:"streamKey"`
	Start    time.Time         `json:"streamStart"`
	Audio    []Track           `json:"audioTracks"`
	Video    []Track           `json:"videoTracks"`
	Sessions []json.RawMessage `json:"sessions"`
}
type Snapshot struct {
	Streams  map[string]Stream
	Complete bool
}

type StatusClient struct {
	Client  *http.Client
	BaseURL string
}

func (s *StatusClient) Fetch(ctx context.Context) (Snapshot, error) {
	result := Snapshot{Streams: map[string]Stream{}, Complete: false}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL+"/api/status", nil)
	if err != nil {
		return result, fmt.Errorf("could not create status request")
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return result, fmt.Errorf("status request: %w", networkError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("status endpoint returned HTTP %d", resp.StatusCode)
	}
	const maxBody = 4 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return result, fmt.Errorf("could not read status response")
	}
	if len(data) > maxBody {
		return result, fmt.Errorf("status response exceeds 4 MiB")
	}
	return parseSnapshot(data)
}
func parseSnapshot(data []byte) (Snapshot, error) {
	result := Snapshot{Streams: map[string]Stream{}, Complete: false}
	var entries []json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		return result, fmt.Errorf("status response must be a JSON array or null")
	}
	invalid := 0
	for _, raw := range entries {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			invalid++
			continue
		}
		missing := false
		for _, key := range []string{"streamKey", "streamStart", "audioTracks", "videoTracks", "sessions"} {
			if _, ok := fields[key]; !ok {
				missing = true
			}
		}
		var stream Stream
		if err := json.Unmarshal(raw, &stream); err != nil || missing || strings.TrimSpace(stream.Key) == "" || stream.Start.IsZero() {
			invalid++
			continue
		}
		valid := true
		for _, session := range stream.Sessions {
			var obj map[string]json.RawMessage
			if json.Unmarshal(session, &obj) != nil || obj == nil {
				valid = false
			}
		}
		if !valid {
			invalid++
			continue
		}
		if _, exists := result.Streams[stream.Key]; exists {
			invalid++
			continue
		}
		result.Streams[stream.Key] = stream
	}
	result.Complete = invalid == 0
	if invalid > 0 {
		return result, fmt.Errorf("ignored %d invalid or duplicate stream entries", invalid)
	}
	return result, nil
}

type LiveState struct {
	Stream   Stream
	Previous map[string]uint64
	Stalled  int
	Ghost    bool
	Pending  bool
}

func (s *LiveState) Update(stream Stream) {
	packets := map[string]uint64{}
	for _, group := range []struct {
		name   string
		tracks []Track
	}{{"audio", stream.Audio}, {"video", stream.Video}} {
		for _, track := range group.tracks {
			packets[group.name+":"+track.RID] = track.Packets
		}
	}
	if s.Previous == nil {
		s.Ghost = len(packets) == 0
		s.Stalled = 0
	} else {
		progressed, reset := false, false
		for key, count := range packets {
			prev := s.Previous[key]
			if count > prev {
				progressed = true
			}
			if count < prev {
				reset = true
			}
		}
		switch {
		case progressed:
			s.Stalled = 0
			s.Ghost = false
			s.Pending = false
		case reset:
			s.Stalled = 0
		default:
			s.Stalled++
			if s.Stalled >= 2 {
				s.Ghost = true
				s.Pending = false
			}
		}
	}
	s.Stream = stream
	if len(packets) == 0 {
		s.Pending = false
		s.Ghost = true
	}
	s.Previous = packets
}
func (s Stream) Mbps() float64 {
	var highest uint64
	for _, track := range s.Video {
		if track.Bitrate > highest {
			highest = track.Bitrate
		}
	}
	return float64(highest) * 8 / 1_000_000
}
