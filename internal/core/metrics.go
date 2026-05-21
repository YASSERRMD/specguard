package core

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// MetricsRegistry manages core and mock telemetry.
type MetricsRegistry struct {
	mu                sync.RWMutex
	HTTPRequestsTotal map[string]int64
	ActiveMocksCount  int64
	MockRequestsTotal map[string]int64
	StartTime         time.Time
}

// Metrics is the global metrics registry.
var Metrics = &MetricsRegistry{
	HTTPRequestsTotal: make(map[string]int64),
	MockRequestsTotal: make(map[string]int64),
	StartTime:         time.Now(),
}

// RecordHTTPRequest increments the request counter for the API server.
func (m *MetricsRegistry) RecordHTTPRequest(method, path, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s:%s", method, path, status)
	m.HTTPRequestsTotal[key]++
}

// SetActiveMocks updates the current count of running mock servers.
func (m *MetricsRegistry) SetActiveMocks(count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ActiveMocksCount = count
}

// RecordMockRequest increments the request counter for mock servers.
func (m *MetricsRegistry) RecordMockRequest(mockID, method, path, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s:%s:%s", mockID, method, path, status)
	m.MockRequestsTotal[key]++
}

// FormatPrometheus serializes registry metrics into Prometheus exposition format.
func (m *MetricsRegistry) FormatPrometheus() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var sb strings.Builder

	// specguard_http_requests_total
	sb.WriteString("# HELP specguard_http_requests_total Total number of HTTP requests processed by core API.\n")
	sb.WriteString("# TYPE specguard_http_requests_total counter\n")
	for key, val := range m.HTTPRequestsTotal {
		parts := strings.Split(key, ":")
		if len(parts) == 3 {
			sb.WriteString(fmt.Sprintf("specguard_http_requests_total{method=\"%s\",path=\"%s\",status=\"%s\"} %d\n", parts[0], parts[1], parts[2], val))
		}
	}

	// specguard_active_mocks_count
	sb.WriteString("# HELP specguard_active_mocks_count Current number of running mock servers.\n")
	sb.WriteString("# TYPE specguard_active_mocks_count gauge\n")
	sb.WriteString(fmt.Sprintf("specguard_active_mocks_count %d\n", m.ActiveMocksCount))

	// specguard_mock_requests_total
	sb.WriteString("# HELP specguard_mock_requests_total Total number of requests processed by mock servers.\n")
	sb.WriteString("# TYPE specguard_mock_requests_total counter\n")
	for key, val := range m.MockRequestsTotal {
		parts := strings.Split(key, ":")
		if len(parts) == 4 {
			sb.WriteString(fmt.Sprintf("specguard_mock_requests_total{mock_id=\"%s\",method=\"%s\",path=\"%s\",status=\"%s\"} %d\n", parts[0], parts[1], parts[2], parts[3], val))
		}
	}

	// specguard_uptime_seconds
	sb.WriteString("# HELP specguard_uptime_seconds Uptime of the Specguard server in seconds.\n")
	sb.WriteString("# TYPE specguard_uptime_seconds gauge\n")
	sb.WriteString(fmt.Sprintf("specguard_uptime_seconds %.0f\n", time.Since(m.StartTime).Seconds()))

	return sb.String()
}
