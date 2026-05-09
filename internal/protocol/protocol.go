package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	Version             = "1"
	ALPN                = "bifrost/1"
	DefaultMaxLineBytes = 8192
)

type Hello struct {
	ProtocolVersion string            `json:"protocol_version"`
	Headers         map[string]string `json:"headers,omitempty"`
}

type Response struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason,omitempty"`
}

func WriteJSONLine(w io.Writer, value any, maxBytes int) error {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxLineBytes
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(payload)+1 > maxBytes {
		return fmt.Errorf("json line exceeds %d bytes", maxBytes)
	}
	payload = append(payload, '\n')
	_, err = w.Write(payload)
	return err
}

func ReadJSONLine(r io.Reader, value any, maxBytes int) error {
	line, err := ReadRawJSONLine(r, maxBytes)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(line, value); err != nil {
		return fmt.Errorf("decode json line: %w", err)
	}
	return nil
}

func ReadRawJSONLine(r io.Reader, maxBytes int) (json.RawMessage, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxLineBytes
	}
	line, err := readLine(r, maxBytes)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, errors.New("empty json line")
	}
	if !json.Valid(line) {
		return nil, errors.New("invalid json line")
	}
	return json.RawMessage(line), nil
}

func readLine(r io.Reader, maxBytes int) ([]byte, error) {
	line := make([]byte, 0, 256)
	buf := make([]byte, 1)
	for len(line) < maxBytes {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				if len(line) > 0 && line[len(line)-1] == '\r' {
					line = line[:len(line)-1]
				}
				return line, nil
			}
			line = append(line, buf[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				return line, nil
			}
			return nil, err
		}
	}
	return nil, fmt.Errorf("json line exceeds %d bytes", maxBytes)
}
