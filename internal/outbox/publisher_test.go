package outbox

import "testing"

func TestNewLoggingPublisherRequiresLogger(t *testing.T) {
	publisher, err := NewLoggingPublisher(nil)
	if err == nil {
		t.Fatal("NewLoggingPublisher returned nil error")
	}
	if publisher != nil {
		t.Fatalf("NewLoggingPublisher publisher = %#v, want nil", publisher)
	}
}
