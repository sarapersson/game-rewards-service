package rewards

import (
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

	if id[14] != '4' {
		t.Fatalf("id[14] = %q, want UUID version '4'", id[14])
	}

	if !strings.ContainsRune("89ab", rune(id[19])) {
		t.Fatalf("id[19] = %q, want RFC 4122 variant 8, 9, a, or b", id[19])
	}
}
