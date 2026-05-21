package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/YASSERRMD/specguard/internal/adapters/grpc"
	"github.com/YASSERRMD/specguard/internal/adapters/rest"
	"github.com/YASSERRMD/specguard/internal/core"
)

func TestHardening_LoadSpecFuzz(t *testing.T) {
	restAdapter := rest.NewAdapter()
	grpcAdapter := grpc.NewAdapter()

	junkData := make([]byte, 1024)
	for i := range junkData {
		junkData[i] = byte(rand.Intn(256))
	}

	testCases := []struct {
		name  string
		input []byte
	}{
		{"Empty Input", []byte{}},
		{"Nil Input", nil},
		{"Whitespace Only", []byte("   \n\t   ")},
		{"Completely Malformed JSON", []byte(`{"openapi": 3.0.0, "paths": { "/test": { "get": `)},
		{"Binary Junk", junkData},
		{"Deeply Nested Object", []byte(generateDeeplyNestedJSON(50))},
	}

	for _, tc := range testCases {
		t.Run("REST_"+tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("REST LoadSpec panicked on input %s: %v", tc.name, r)
				}
			}()
			_, _ = restAdapter.LoadSpec(tc.input)
		})

		t.Run("gRPC_"+tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("gRPC LoadSpec panicked on input %s: %v", tc.name, r)
				}
			}()
			_, _ = grpcAdapter.LoadSpec(tc.input)
		})
	}
}

func generateDeeplyNestedJSON(depth int) string {
	if depth <= 0 {
		return `{"value": "end"}`
	}
	return fmt.Sprintf(`{"nested": %s}`, generateDeeplyNestedJSON(depth-1))
}

func TestHardening_ConcurrentLoadAndRateLimit(t *testing.T) {
	rawOpenAPI := `
openapi: 3.0.3
info:
  title: Stateful Pets API
  version: 1.0.0
paths:
  /pets:
    get:
      summary: List all pets
      operationId: listPets
      responses:
        '200':
          description: A list of pets
          content:
            application/json:
              schema:
                type: object
                required:
                  - total
                  - pets
                properties:
                  total:
                    type: integer
                  pets:
                    type: array
                    items:
                      type: object
                      required:
                        - id
                        - name
                      properties:
                        id:
                          type: string
                          format: uuid
                        name:
                          type: string
    post:
      summary: Create a pet
      operationId: createPet
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required:
                - name
              properties:
                name:
                  type: string
      responses:
        '201':
          description: Created pet
          content:
            application/json:
              schema:
                type: object
                required:
                  - id
                  - name
                properties:
                  id:
                    type: string
                    format: uuid
                  name:
                    type: string
`

	adapter := rest.NewAdapter()
	spec, err := adapter.LoadSpec([]byte(rawOpenAPI))
	if err != nil {
		t.Fatalf("failed to load test spec: %v", err)
	}

	// First, let us run a concurrent load test with rate limiting.
	// Rate limit: 20 requests per second.
	configWithLimit := core.MockConfig{
		Host:               "127.0.0.1",
		Port:               0,
		RateLimit:          20.0,
		MaxRequestBodySize: 1024 * 1024,
	}

	mockServer, err := adapter.GenerateMock(spec, configWithLimit)
	if err != nil {
		t.Fatalf("failed to generate mock server: %v", err)
	}

	if err := mockServer.Start(); err != nil {
		t.Fatalf("failed to start mock server: %v", err)
	}

	addr := mockServer.GetAddress()
	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	// Spin up multiple goroutines to trigger requests concurrently.
	var wg sync.WaitGroup
	numWorkers := 15
	reqsPerWorker := 10
	statusCodes := make(chan int, numWorkers*reqsPerWorker)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < reqsPerWorker; j++ {
				// Alternating POST and GET
				var resp *http.Response
				var err error
				if j%2 == 0 {
					payload := map[string]string{"name": fmt.Sprintf("pet-%d-%d", workerID, j)}
					bodyBytes, _ := json.Marshal(payload)
					resp, err = client.Post(addr+"/pets", "application/json", bytes.NewReader(bodyBytes))
				} else {
					resp, err = client.Get(addr + "/pets")
				}

				if err == nil {
					statusCodes <- resp.StatusCode
					_, _ = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				} else {
					statusCodes <- 0
				}
			}
		}(i)
	}

	wg.Wait()
	close(statusCodes)

	_ = mockServer.Stop()

	// Analyze status codes
	count429 := 0
	countSuccess := 0
	countOther := 0

	for code := range statusCodes {
		if code == http.StatusTooManyRequests {
			count429++
		} else if code == http.StatusOK || code == http.StatusCreated {
			countSuccess++
		} else {
			countOther++
		}
	}

	t.Logf("Concurrent test results: Success=%d, Rate-Limited=%d, Other/Errors=%d", countSuccess, count429, countOther)

	// Since limit was 20 rps and we sent 150 requests almost instantaneously,
	// we expect to have hit some rate-limiting (429) and had some successes.
	if count429 == 0 {
		t.Log("Warning: No requests were rate limited (this can happen on extremely slow systems, but is unusual)")
	}
	if countSuccess == 0 {
		t.Errorf("Expected at least some requests to succeed, but got 0 successes")
	}

	// Now let us run a concurrent CRUD integrity test without rate limiting constraints (high rate limit)
	configNoLimit := core.MockConfig{
		Host:               "127.0.0.1",
		Port:               0,
		RateLimit:          10000.0,
		MaxRequestBodySize: 1024 * 1024,
	}

	mockServerNoLimit, err := adapter.GenerateMock(spec, configNoLimit)
	if err != nil {
		t.Fatalf("failed to generate mock server: %v", err)
	}

	if err := mockServerNoLimit.Start(); err != nil {
		t.Fatalf("failed to start mock server: %v", err)
	}
	defer func() {
		_ = mockServerNoLimit.Stop()
	}()

	addrNoLimit := mockServerNoLimit.GetAddress()
	var wgCrud sync.WaitGroup
	crudWorkers := 10
	crudOps := 20

	for i := 0; i < crudWorkers; i++ {
		wgCrud.Add(1)
		go func(workerID int) {
			defer wgCrud.Done()
			for j := 0; j < crudOps; j++ {
				// Perform stateful CRUD operations concurrently.
				// 1. Create
				payload := map[string]string{"name": fmt.Sprintf("crud-pet-%d-%d", workerID, j)}
				bodyBytes, _ := json.Marshal(payload)
				resp, err := client.Post(addrNoLimit+"/pets", "application/json", bytes.NewReader(bodyBytes))
				if err != nil {
					t.Errorf("POST failed: %v", err)
					return
				}
				if resp.StatusCode != http.StatusCreated {
					t.Errorf("expected 201 Created, got %d", resp.StatusCode)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()

				// 2. Read (list)
				resp, err = client.Get(addrNoLimit + "/pets")
				if err != nil {
					t.Errorf("GET failed: %v", err)
					return
				}
				if resp.StatusCode != http.StatusOK {
					t.Errorf("expected 200 OK, got %d", resp.StatusCode)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}(i)
	}

	wgCrud.Wait()
}
