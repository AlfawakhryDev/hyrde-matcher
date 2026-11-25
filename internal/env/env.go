// Package env reads configuration from environment variables, the
// twelve-factor way that Kubernetes ConfigMaps and Secrets feed a process.
package env

import (
	"os"
	"strings"
	"time"
)

func String(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func Duration(key string, fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return d
	}
	return fallback
}

func List(key, fallback string) []string {
	return strings.Split(String(key, fallback), ",")
}
