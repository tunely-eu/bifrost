package header

import "testing"

func TestFlagParsesRepeatedHeaders(t *testing.T) {
	var headers Flag
	if err := headers.Set("tunnel=dev"); err != nil {
		t.Fatalf("Set tunnel: %v", err)
	}
	if err := headers.Set("env=dev"); err != nil {
		t.Fatalf("Set env: %v", err)
	}

	values := headers.Values()
	if values["tunnel"] != "dev" {
		t.Fatalf("tunnel = %q", values["tunnel"])
	}
	if values["env"] != "dev" {
		t.Fatalf("env = %q", values["env"])
	}
}

func TestFlagRejectsDuplicateHeaders(t *testing.T) {
	var headers Flag
	if err := headers.Set("tunnel=dev"); err != nil {
		t.Fatalf("Set tunnel: %v", err)
	}
	if err := headers.Set("tunnel=other"); err == nil {
		t.Fatal("expected duplicate header error")
	}
}

func TestParsePairRejectsInvalidHeaders(t *testing.T) {
	tests := []string{
		"missing-equals",
		"=value",
		"tunnel name=value",
		"tunnel=",
		"tünnel=value",
	}

	for _, test := range tests {
		t.Run(test, func(t *testing.T) {
			if _, _, err := ParsePair(test); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
