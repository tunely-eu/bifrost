package header

import (
	"fmt"
	"strings"
)

type Values map[string]string

func ParsePair(raw string) (string, string, error) {
	key, value, ok := strings.Cut(raw, "=")
	if !ok {
		return "", "", fmt.Errorf("header must be in key=value form")
	}
	if err := ValidateName(key); err != nil {
		return "", "", err
	}
	if value == "" {
		return "", "", fmt.Errorf("header %q must have a non-empty value", key)
	}
	return key, value, nil
}

func Validate(headers map[string]string) error {
	_, err := Normalize(headers, 0, 0)
	return err
}

func Normalize(headers map[string]string, maxHeaders int, maxBytes int) (map[string]string, error) {
	if maxHeaders > 0 && len(headers) > maxHeaders {
		return nil, fmt.Errorf("too many headers: %d > %d", len(headers), maxHeaders)
	}

	normalized := make(map[string]string, len(headers))
	totalBytes := 0
	for key, value := range headers {
		if err := ValidateName(key); err != nil {
			return nil, err
		}
		if value == "" {
			return nil, fmt.Errorf("header %q must have a non-empty value", key)
		}
		lower := strings.ToLower(key)
		if _, exists := normalized[lower]; exists {
			return nil, fmt.Errorf("duplicate header %q", key)
		}
		totalBytes += len(key) + len(value)
		if maxBytes > 0 && totalBytes > maxBytes {
			return nil, fmt.Errorf("headers exceed %d bytes", maxBytes)
		}
		normalized[lower] = value
	}
	return normalized, nil
}

func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("header name must not be empty")
	}
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return fmt.Errorf("invalid header name %q", name)
		}
	}
	return nil
}

func Clone(headers map[string]string) map[string]string {
	cloned := make(map[string]string, len(headers))
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
}

type Flag struct {
	values Values
}

func (f *Flag) Set(raw string) error {
	key, value, err := ParsePair(raw)
	if err != nil {
		return err
	}
	key = strings.ToLower(key)
	if f.values == nil {
		f.values = make(Values)
	}
	if _, exists := f.values[key]; exists {
		return fmt.Errorf("duplicate header %q", key)
	}
	f.values[key] = value
	return nil
}

func (f *Flag) String() string {
	if f == nil || len(f.values) == 0 {
		return ""
	}
	return fmt.Sprintf("%v", map[string]string(f.values))
}

func (f *Flag) Values() map[string]string {
	if f == nil {
		return nil
	}
	return Clone(f.values)
}
