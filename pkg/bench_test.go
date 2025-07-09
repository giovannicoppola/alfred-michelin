package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/giovanni/alfred-michelin/db"
)

func BenchmarkUpdateCheckNoFile(b *testing.B) {
	// Use a non-existent path to simulate the common case
	tempDir := "/tmp/non_existent_directory_12345"
	dbPath := filepath.Join(tempDir, "test.db")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := db.UpdateDatabase(dbPath)
		if err == nil || !db.IsNoUpdateAvailable(err) {
			b.Fatal("Expected NoUpdateAvailableError")
		}
	}
}

func TestUpdateCheckTiming(t *testing.T) {
	dbPath := "/tmp/test.db"

	// Measure 100 consecutive calls
	start := time.Now()
	for i := 0; i < 100; i++ {
		err := db.UpdateDatabase(dbPath)
		if err == nil || !db.IsNoUpdateAvailable(err) {
			t.Fatal("Expected NoUpdateAvailableError")
		}
	}
	duration := time.Since(start)

	avgTime := duration / 100
	t.Logf("Average time per update check: %v", avgTime)
	t.Logf("Total time for 100 checks: %v", duration)

	// Should be very fast - if it takes more than 10ms per check, something's wrong
	if avgTime > 10*time.Millisecond {
		t.Errorf("Update check is too slow: %v per check", avgTime)
	}
}
