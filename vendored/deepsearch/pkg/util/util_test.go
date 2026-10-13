package util

import "testing"

func TestBuildVersionPrefersInjectedVersion(t *testing.T) {
	previous := version
	version = "v9.9.9"
	t.Cleanup(func() { version = previous })

	if got := BuildVersion(); got != "v9.9.9" {
		t.Fatalf("BuildVersion() = %q, want %q", got, "v9.9.9")
	}
}

func TestBuildVersionFallsBack(t *testing.T) {
	previous := version
	version = ""
	t.Cleanup(func() { version = previous })

	if got := BuildVersion(); got == "" {
		t.Fatal("BuildVersion() must never be empty")
	}
}
