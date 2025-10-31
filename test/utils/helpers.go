package utils

import (
	"fmt"
	"os"
)

// CreateTempFile creates a temporary file with the given content
func CreateTempFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}

// CleanupTempFile removes a temporary file
func CleanupTempFile(path string) error {
	return os.Remove(path)
}

// MustGetEnv gets an environment variable or panics
func MustGetEnv(key string) string {
	value := os.Getenv(key)
	if value == "" {
		panic(fmt.Sprintf("Environment variable %s is required", key))
	}
	return value
}

// GetEnvOrDefault gets an environment variable or returns a default value
func GetEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}
