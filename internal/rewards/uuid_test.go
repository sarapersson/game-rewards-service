package rewards

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestNewUUIDV4Shape(t *testing.T) {
	id, err := NewUUIDV4()
	if err != nil {
		t.Fatalf("NewUUIDV4 returned error: %v", err)
	}

	if len(id) != 36 {
		t.Fatalf("len(id) = %d, want 36", len(id))
	}

	for _, index := range []int{8, 13, 18, 23} {
		if id[index] != '-' {
			t.Fatalf("id[%d] = %q, want '-'", index, id[index])
		}
	}

	encoded := strings.ReplaceAll(id, "-", "")
	if len(encoded) != 32 {
		t.Fatalf("UUID without separators has length %d, want 32", len(encoded))
	}

	decoded, err := hex.DecodeString(encoded)
	if err != nil {
		t.Fatalf("UUID contains invalid hexadecimal data: %v", err)
	}

	if len(decoded) != 16 {
		t.Fatalf("decoded UUID length = %d, want 16", len(decoded))
	}

	if version := decoded[6] >> 4; version != 4 {
		t.Fatalf("UUID version = %d, want 4", version)
	}

	if variant := decoded[8] >> 6; variant != 2 {
		t.Fatalf("UUID variant bits = %02b, want 10", variant)
	}
}
