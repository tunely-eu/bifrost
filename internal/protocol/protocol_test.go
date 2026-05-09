package protocol

import (
	"bytes"
	"strings"
	"testing"
)

func TestJSONLineRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	err := WriteJSONLine(&buf, Hello{
		ProtocolVersion: Version,
		Headers: map[string]string{
			"tunnel": "dev",
			"env":    "dev",
		},
	}, DefaultMaxLineBytes)
	if err != nil {
		t.Fatalf("WriteJSONLine: %v", err)
	}

	var hello Hello
	if err := ReadJSONLine(&buf, &hello, DefaultMaxLineBytes); err != nil {
		t.Fatalf("ReadJSONLine: %v", err)
	}
	if hello.ProtocolVersion != Version {
		t.Fatalf("ProtocolVersion = %q", hello.ProtocolVersion)
	}
	if hello.Headers["tunnel"] != "dev" {
		t.Fatalf("tunnel = %q", hello.Headers["tunnel"])
	}
}

func TestReadJSONLineDoesNotOverread(t *testing.T) {
	buf := bytes.NewBufferString(`{"accepted":true}` + "\nNEXT")

	var response Response
	if err := ReadJSONLine(buf, &response, DefaultMaxLineBytes); err != nil {
		t.Fatalf("ReadJSONLine: %v", err)
	}
	if !response.Accepted {
		t.Fatal("expected accepted response")
	}
	if rest := buf.String(); rest != "NEXT" {
		t.Fatalf("buffer rest = %q", rest)
	}
}

func TestReadJSONLineRejectsOversizedPayload(t *testing.T) {
	buf := strings.NewReader(`{"x":"` + strings.Repeat("a", 64) + `"}` + "\n")
	var value map[string]string
	if err := ReadJSONLine(buf, &value, 16); err == nil {
		t.Fatal("expected oversized payload error")
	}
}
