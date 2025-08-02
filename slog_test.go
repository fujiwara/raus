package raus_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/fujiwara/raus"
)

func TestSlogIntegration(t *testing.T) {
	// Create a buffer to capture log output
	var buf bytes.Buffer
	
	// Create JSON handler for structured logging
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	logger := slog.New(handler)
	
	// Set default logger
	raus.SetDefaultSlogLogger(logger)
	
	// Create raus instance
	r, err := raus.New(redisURL, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	
	// Test instance-specific logger
	var instanceBuf bytes.Buffer
	instanceHandler := slog.NewJSONHandler(&instanceBuf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	instanceLogger := slog.New(instanceHandler)
	r.SetSlogLogger(instanceLogger)
	
	// Get machine ID
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	machineID, errCh, err := r.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	
	// Verify machine ID is valid
	if machineID > 3 {
		t.Errorf("Invalid machine ID: %d", machineID)
	}
	
	// Let it run for a moment to generate logs
	time.Sleep(100 * time.Millisecond)
	cancel()
	
	// Wait for error channel to close
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
	}
	
	// Verify structured logs were generated
	output := instanceBuf.String()
	if output == "" {
		t.Error("No log output generated")
	}
	
	// Parse JSON logs to verify structure
	lines := bytes.Split(instanceBuf.Bytes(), []byte("\n"))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		
		var logEntry map[string]interface{}
		if err := json.Unmarshal(line, &logEntry); err != nil {
			t.Errorf("Failed to parse JSON log: %v", err)
			continue
		}
		
		// Verify required fields exist
		if _, ok := logEntry["time"]; !ok {
			t.Error("Missing 'time' field in log entry")
		}
		if _, ok := logEntry["level"]; !ok {
			t.Error("Missing 'level' field in log entry")
		}
		if _, ok := logEntry["msg"]; !ok {
			t.Error("Missing 'msg' field in log entry")
		}
	}
	
	t.Logf("Generated %d log entries with structured logging", len(lines)-1)
}

func TestDefaultSlogLogger(t *testing.T) {
	// Test that default slog logger works
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	customLogger := slog.New(handler)
	
	// Set global default
	raus.SetDefaultSlogLogger(customLogger)
	
	r, err := raus.New(redisURL, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	machineID, errCh, err := r.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	
	if machineID > 3 {
		t.Errorf("Invalid machine ID: %d", machineID)
	}
	
	cancel()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
	}
	
	// Verify logs were written to our custom logger
	output := buf.String()
	if output == "" {
		t.Error("No log output generated with custom default logger")
	}
}