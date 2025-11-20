package config

import (
	"os"
	"testing"
)

func TestIsAdmin(t *testing.T) {
	cfg := &Config{
		AdminIDs: []int64{123456789, 987654321},
	}

	tests := []struct {
		name     string
		userID   int64
		expected bool
	}{
		{"Admin user 1", 123456789, true},
		{"Admin user 2", 987654321, true},
		{"Non-admin user", 111111111, false},
		{"Zero ID", 0, false},
		{"Negative ID", -1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cfg.IsAdmin(tt.userID)
			if result != tt.expected {
				t.Errorf("IsAdmin(%d) = %v, expected %v", tt.userID, result, tt.expected)
			}
		})
	}
}

func TestGetDatabaseDSN(t *testing.T) {
	cfg := &Config{
		DBHost:     "localhost",
		DBPort:     5432,
		DBUser:     "testuser",
		DBPassword: "testpass",
		DBName:     "testdb",
		DBSSLMode:  "disable",
	}

	expected := "host=localhost port=5432 user=testuser password=testpass dbname=testdb sslmode=disable"
	result := cfg.GetDatabaseDSN()

	if result != expected {
		t.Errorf("GetDatabaseDSN() = %v, expected %v", result, expected)
	}
}

func TestParseAdminIDs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []int64
	}{
		{"Single ID", "123456789", []int64{123456789}},
		{"Multiple IDs", "123456789,987654321", []int64{123456789, 987654321}},
		{"IDs with spaces", "123456789, 987654321, 555555555", []int64{123456789, 987654321, 555555555}},
		{"Empty string", "", []int64{}},
		{"Invalid ID", "abc,123", []int64{123}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseAdminIDs(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("parseAdminIDs(%q) returned %d IDs, expected %d", tt.input, len(result), len(tt.expected))
				return
			}
			for i, id := range result {
				if id != tt.expected[i] {
					t.Errorf("parseAdminIDs(%q)[%d] = %d, expected %d", tt.input, i, id, tt.expected[i])
				}
			}
		})
	}
}

func TestGetEnvInt(t *testing.T) {
	tests := []struct {
		name         string
		envKey       string
		envValue     string
		defaultValue int
		expected     int
	}{
		{"Valid int", "TEST_INT", "42", 10, 42},
		{"Empty env", "TEST_INT_EMPTY", "", 10, 10},
		{"Invalid int", "TEST_INT_INVALID", "abc", 10, 10},
		{"Negative int", "TEST_INT_NEG", "-5", 10, -5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.envKey, tt.envValue)
				defer os.Unsetenv(tt.envKey)
			}

			result := getEnvInt(tt.envKey, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("getEnvInt(%q, %d) = %d, expected %d", tt.envKey, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestGetEnvBool(t *testing.T) {
	tests := []struct {
		name         string
		envKey       string
		envValue     string
		defaultValue bool
		expected     bool
	}{
		{"True value", "TEST_BOOL_TRUE", "true", false, true},
		{"False value", "TEST_BOOL_FALSE", "false", true, false},
		{"1 value", "TEST_BOOL_1", "1", false, true},
		{"0 value", "TEST_BOOL_0", "0", true, false},
		{"Empty env", "TEST_BOOL_EMPTY", "", true, true},
		{"Invalid bool", "TEST_BOOL_INVALID", "abc", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.envKey, tt.envValue)
				defer os.Unsetenv(tt.envKey)
			}

			result := getEnvBool(tt.envKey, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("getEnvBool(%q, %v) = %v, expected %v", tt.envKey, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestConfigValidation(t *testing.T) {
	// Test that config validation enforces architectural constraints
	tests := []struct {
		name              string
		maxExtractWorkers int
		maxConvertWorkers int
		expectError       bool
		errorContains     string
	}{
		{"Valid config", 1, 1, false, ""},
		{"Invalid extract workers", 2, 1, true, "MAX_EXTRACT_WORKERS must be 1"},
		{"Invalid convert workers", 1, 2, true, "MAX_CONVERT_WORKERS must be 1"},
		{"Both invalid", 2, 2, true, "MAX_EXTRACT_WORKERS must be 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables for minimal valid config
			os.Setenv("TELEGRAM_BOT_TOKEN", "test_token")
			os.Setenv("ADMIN_IDS", "123456789")
			os.Setenv("DB_PASSWORD", "test_password")
			os.Setenv("MAX_EXTRACT_WORKERS", string(rune(tt.maxExtractWorkers+'0')))
			os.Setenv("MAX_CONVERT_WORKERS", string(rune(tt.maxConvertWorkers+'0')))

			defer func() {
				os.Unsetenv("TELEGRAM_BOT_TOKEN")
				os.Unsetenv("ADMIN_IDS")
				os.Unsetenv("DB_PASSWORD")
				os.Unsetenv("MAX_EXTRACT_WORKERS")
				os.Unsetenv("MAX_CONVERT_WORKERS")
			}()

			cfg, err := LoadConfig()

			if tt.expectError {
				if err == nil {
					t.Errorf("LoadConfig() expected error containing %q, got nil", tt.errorContains)
				} else if tt.errorContains != "" && !contains(err.Error(), tt.errorContains) {
					t.Errorf("LoadConfig() error = %q, expected to contain %q", err.Error(), tt.errorContains)
				}
			} else {
				if err != nil {
					t.Errorf("LoadConfig() unexpected error: %v", err)
				}
				if cfg == nil {
					t.Error("LoadConfig() returned nil config")
				}
			}
		})
	}
}

// Helper function for string contains check
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
