package pagination

import (
	"testing"
	"time"
)

func TestEncodeDecodeCursorRoundTrip(t *testing.T) {
	encoded := EncodeCursor(42, "created_at", time.Date(2026, 4, 16, 12, 30, 0, 123456789, time.UTC))
	if encoded == "" {
		t.Fatal("expected encoded cursor")
	}

	decoded, err := DecodeCursor(encoded)
	if err != nil {
		t.Fatalf("decode cursor failed: %v", err)
	}
	if decoded == nil {
		t.Fatal("expected decoded cursor")
	}
	if decoded.ID != 42 {
		t.Fatalf("unexpected id: %d", decoded.ID)
	}
	if decoded.SortField != "created_at" {
		t.Fatalf("unexpected sort field: %q", decoded.SortField)
	}
	if decoded.SortValue != "2026-04-16T12:30:00.123456789Z" {
		t.Fatalf("unexpected sort value: %q", decoded.SortValue)
	}
}

func TestDecodeCursorRejectsInvalidInput(t *testing.T) {
	if _, err := DecodeCursor("not-a-cursor"); err != ErrInvalidCursor {
		t.Fatalf("expected ErrInvalidCursor, got %v", err)
	}
}