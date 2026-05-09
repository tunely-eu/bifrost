package logging

import "testing"

func TestRedactHeaders(t *testing.T) {
	headers := RedactHeaders(map[string]string{"x-bifrost-token": "secret"})
	if headers["x-bifrost-token"] == "secret" {
		t.Fatal("header value was not redacted")
	}
}
