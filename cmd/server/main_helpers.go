package main

import (
	"os"
	"strconv"
)

// getEnv gets an environment variable with a default value
func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// getEnvInt gets an integer environment variable with a default value
func getEnvInt(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}

	intValue, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	return intValue
}

// shouldEnableMySQL checks if MySQL persistence should be enabled
func shouldEnableMySQL() bool {
	// Check if MySQL host is configured
	mysqlHost := os.Getenv("MYSQL_HOST")
	if mysqlHost == "" || mysqlHost == "disabled" {
		return false
	}

	// Check for explicit disable flag
	if os.Getenv("DISABLE_MYSQL") == "true" {
		return false
	}

	return true
}
