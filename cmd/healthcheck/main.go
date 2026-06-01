// Healthcheck binary for Docker HEALTHCHECK in distroless images.
package main

import (
	"net/http"
	"os"
)

// healthcheck performs an HTTP GET against the service /health endpoint and
// returns a process exit code: 0 if healthy, 1 otherwise. It is separated from
// main so it can be unit tested without terminating the test process.
func healthcheck(port string) int {
	if port == "" {
		port = "8080"
	}
	resp, err := http.Get("http://localhost:" + port + "/health")
	if err != nil || resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func main() {
	os.Exit(healthcheck(os.Getenv("HEALTH_PORT")))
}
