package mcp

// Port of consumeSseStream from packages/mcp/src/transports/streamable-http.ts:
// a dependency-free Server-Sent Events reader with per-event size bounds.

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"strconv"
)

// SseEvent is one dispatched SSE event.
type SseEvent struct {
	Event    string
	HasEvent bool
	Data     string
	ID       string
	HasID    bool
}

// ConsumeSseOptions configures ConsumeSseStream.
type ConsumeSseOptions struct {
	MaxEventBytes int
	OnEvent       func(event SseEvent)
	// OnID runs for every id field, including events without data (for
	// example resumption priming events).
	OnID func(id string)
	// OnRetry runs for every valid retry field, in milliseconds.
	OnRetry func(delayMs int)
}

// ConsumeSseStream reads a byte stream as SSE events until the stream ends.
// Event data is bounded by maxEventBytes so events streamed as many short
// data lines without a terminating blank line cannot grow without bound.
func ConsumeSseStream(stream io.Reader, options ConsumeSseOptions) error {
	maxEventBytes := options.MaxEventBytes
	if maxEventBytes == 0 {
		maxEventBytes = DefaultMaxMessageBytes
	}
	state := &sseState{options: options, maxEventBytes: maxEventBytes}
	chunk := make([]byte, 16*1024)
	for {
		read, err := stream.Read(chunk)
		if read > 0 {
			if processErr := state.push(chunk[:read]); processErr != nil {
				return processErr
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}
	return state.finish()
}

type sseState struct {
	options       ConsumeSseOptions
	maxEventBytes int
	buffered      []byte
	eventName     string
	hasEvent      bool
	eventID       string
	hasEventID    bool
	dataLines     []string
	dataBytes     int
}

func (s *sseState) push(chunk []byte) error {
	s.buffered = append(s.buffered, chunk...)
	for {
		newline := bytes.IndexByte(s.buffered, '\n')
		if newline < 0 {
			break
		}
		line := s.buffered[:newline]
		owned := append([]byte(nil), line...)
		s.buffered = s.buffered[newline+1:]
		if err := s.processLine(owned); err != nil {
			return err
		}
	}
	if len(s.buffered) > s.maxEventBytes {
		return errors.New("MCP SSE event exceeds " + itoa(s.maxEventBytes) + " bytes")
	}
	return nil
}

func (s *sseState) finish() error {
	if len(s.buffered) > 0 {
		if err := s.processLine(s.buffered); err != nil {
			return err
		}
	}
	s.dispatch()
	return nil
}

func (s *sseState) dispatch() {
	if len(s.dataLines) == 0 {
		s.hasEvent = false
		s.hasEventID = false
		return
	}
	data := ""
	for index, line := range s.dataLines {
		if index > 0 {
			data += "\n"
		}
		data += line
	}
	event := SseEvent{Data: data}
	if s.hasEvent {
		event.Event, event.HasEvent = s.eventName, true
	}
	if s.hasEventID {
		event.ID, event.HasID = s.eventID, true
	}
	if s.options.OnEvent != nil {
		s.options.OnEvent(event)
	}
	s.hasEvent = false
	s.hasEventID = false
	s.dataLines = nil
	s.dataBytes = 0
}

func (s *sseState) processLine(rawLine []byte) error {
	line := rawLine
	if bytes.HasSuffix(line, []byte("\r")) {
		line = line[:len(line)-1]
	}
	if len(line) == 0 {
		s.dispatch()
		return nil
	}
	if line[0] == ':' {
		return nil
	}
	field := line
	var value []byte
	if colon := bytes.IndexByte(line, ':'); colon >= 0 {
		field, value = line[:colon], line[colon+1:]
	}
	if len(value) > 0 && value[0] == ' ' {
		value = value[1:]
	}
	switch string(field) {
	case "data":
		if len(s.dataLines) > 0 {
			s.dataBytes++
		}
		s.dataBytes += len(value)
		if s.dataBytes > s.maxEventBytes {
			return errors.New("MCP SSE event exceeds " + itoa(s.maxEventBytes) + " bytes")
		}
		s.dataLines = append(s.dataLines, string(value))
	case "event":
		s.eventName, s.hasEvent = string(value), true
	case "id":
		if bytes.IndexByte(value, 0) >= 0 {
			return nil
		}
		s.eventID, s.hasEventID = string(value), true
		if s.options.OnID != nil {
			s.options.OnID(string(value))
		}
	case "retry":
		if retryPattern.Match(value) {
			if delay, err := strconv.Atoi(string(value)); err == nil && s.options.OnRetry != nil {
				s.options.OnRetry(delay)
			}
		}
	}
	return nil
}

var retryPattern = regexp.MustCompile(`^\d+$`)
