package extract

import "testing"

func TestResolveExtractor_NoProviderConfigured(t *testing.T) {
	if _, err := ResolveExtractor(Config{}); err == nil {
		t.Fatal("expected error when no provider is configured, got nil")
	}
}

func TestResolveExtractor_GeminiConfigured(t *testing.T) {
	extractor, err := ResolveExtractor(Config{GeminiAPIKey: "test-key"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if extractor == nil {
		t.Fatal("expected a non-nil extractor")
	}
	if _, ok := extractor.(*GeminiExtractor); !ok {
		t.Fatalf("expected *GeminiExtractor, got %T", extractor)
	}
}
