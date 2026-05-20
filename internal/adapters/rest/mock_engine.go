package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YASSERRMD/specguard/internal/core"
)

// MockServer implements core.RunnableMock for HTTP mock servers.
type MockServer struct {
	spec     *core.NormalizedSpec
	config   core.MockConfig
	listener net.Listener
	server   *http.Server
	address  string
	mu       sync.Mutex
	running  bool
}

// NewMockServer creates a new instance of MockServer.
func NewMockServer(spec *core.NormalizedSpec, config core.MockConfig) *MockServer {
	return &MockServer{
		spec:   spec,
		config: config,
	}
}

// Start starts the mock HTTP server in the background.
func (m *MockServer) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("mock server already running")
	}

	host := m.config.Host
	if host == "" {
		host = "127.0.0.1"
	}
	addr := fmt.Sprintf("%s:%d", host, m.config.Port)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	m.listener = l
	m.address = fmt.Sprintf("http://%s", l.Addr().String())

	mux := http.NewServeMux()
	mux.HandleFunc("/", m.handleRequest)

	m.server = &http.Server{
		Handler: mux,
	}

	m.running = true
	go func() {
		_ = m.server.Serve(l)
	}()

	return nil
}

// Stop gracefully shuts down the running mock server.
func (m *MockServer) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := m.server.Shutdown(ctx)
	m.running = false
	return err
}

// GetAddress returns the listening URL/address of the mock server.
func (m *MockServer) GetAddress() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.address
}

func (m *MockServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	var matchedOp *core.Operation
	var pathParams map[string]string

	for _, op := range m.spec.Operations {
		opMethod := op.Metadata["method"]
		opPath := op.Metadata["path"]

		if strings.ToUpper(opMethod) != strings.ToUpper(r.Method) {
			continue
		}

		params, ok := matchPath(opPath, r.URL.Path)
		if ok {
			o := op
			matchedOp = &o
			pathParams = params
			break
		}
	}

	if matchedOp == nil {
		m.writeError(w, http.StatusNotFound, "no matching operation found in specification")
		return
	}

	reqVal := make(map[string]interface{})

	// 1. Path parameters
	if pathSchema, exists := matchedOp.Input.Properties["path"]; exists {
		pathMap := make(map[string]interface{})
		for name, schema := range pathSchema.Properties {
			val, ok := pathParams[name]
			if !ok {
				continue
			}
			parsedVal, err := convertValue(val, schema)
			if err == nil {
				pathMap[name] = parsedVal
			} else {
				pathMap[name] = val
			}
		}
		reqVal["path"] = pathMap
	}

	// 2. Query parameters
	if querySchema, exists := matchedOp.Input.Properties["query"]; exists {
		queryMap := make(map[string]interface{})
		queryParams := r.URL.Query()
		for name, schema := range querySchema.Properties {
			vals, ok := queryParams[name]
			if !ok || len(vals) == 0 {
				continue
			}
			var rawVal interface{}
			if schema.Type == core.TypeArray {
				rawVal = vals
			} else {
				rawVal = vals[0]
			}
			parsedVal, err := convertValue(rawVal, schema)
			if err == nil {
				queryMap[name] = parsedVal
			} else {
				queryMap[name] = rawVal
			}
		}
		reqVal["query"] = queryMap
	}

	// 3. Header parameters
	if headerSchema, exists := matchedOp.Input.Properties["header"]; exists {
		headerMap := make(map[string]interface{})
		for name, schema := range headerSchema.Properties {
			val := r.Header.Get(name)
			if val == "" {
				continue
			}
			parsedVal, err := convertValue(val, schema)
			if err == nil {
				headerMap[name] = parsedVal
			} else {
				headerMap[name] = val
			}
		}
		reqVal["header"] = headerMap
	}

	// 4. Request Body
	if _, exists := matchedOp.Input.Properties["body"]; exists {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			m.writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to read body: %v", err))
			return
		}
		if len(bodyBytes) > 0 {
			var bodyVal interface{}
			if err := json.Unmarshal(bodyBytes, &bodyVal); err != nil {
				reqVal["body"] = string(bodyBytes)
			} else {
				reqVal["body"] = bodyVal
			}
		}
	}

	// Validate request fields
	if err := matchedOp.Input.Match(reqVal); err != nil {
		validationErr, ok := err.(*core.ValidationError)
		var msg string
		if ok {
			msg = validationErr.Error()
		} else {
			msg = err.Error()
		}
		m.writeError(w, http.StatusBadRequest, fmt.Sprintf("Request validation failed: %s", msg))
		return
	}

	// Select low 2xx response status
	selectedStatus := 200
	var responseSchema *core.Schema

	lowestSuccess := 999
	for statusStr, schema := range matchedOp.Output.Properties {
		status, err := strconv.Atoi(statusStr)
		if err == nil && status >= 200 && status < 300 {
			if status < lowestSuccess {
				lowestSuccess = status
				s := schema
				responseSchema = &s
			}
		}
	}

	if lowestSuccess != 999 {
		selectedStatus = lowestSuccess
	}

	if responseSchema == nil {
		w.WriteHeader(selectedStatus)
		return
	}

	mockResp := generateMockValue(*responseSchema)
	m.writeJSON(w, selectedStatus, mockResp)
}

func (m *MockServer) writeJSON(w http.ResponseWriter, status int, val interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(val)
}

func (m *MockServer) writeError(w http.ResponseWriter, status int, msg string) {
	m.writeJSON(w, status, map[string]string{"error": msg})
}

func matchPath(pattern, path string) (map[string]string, bool) {
	pattern = strings.Trim(pattern, "/")
	path = strings.Trim(path, "/")
	if pattern == "" && path == "" {
		return nil, true
	}
	patternSegs := strings.Split(pattern, "/")
	pathSegs := strings.Split(path, "/")
	if len(patternSegs) != len(pathSegs) {
		return nil, false
	}
	params := make(map[string]string)
	for i, patternSeg := range patternSegs {
		if strings.HasPrefix(patternSeg, "{") && strings.HasSuffix(patternSeg, "}") {
			paramName := patternSeg[1 : len(patternSeg)-1]
			params[paramName] = pathSegs[i]
		} else if patternSeg != pathSegs[i] {
			return nil, false
		}
	}
	return params, true
}

func convertValue(raw interface{}, s core.Schema) (interface{}, error) {
	if s.Type == core.TypeScalar {
		str, ok := raw.(string)
		if !ok {
			return raw, nil
		}
		switch s.ScalarType {
		case core.ScalarNumber:
			return strconv.ParseFloat(str, 64)
		case core.ScalarInteger:
			val, err := strconv.ParseInt(str, 10, 64)
			if err != nil {
				return raw, err
			}
			return float64(val), nil
		case core.ScalarBoolean:
			return strconv.ParseBool(str)
		}
	} else if s.Type == core.TypeArray && s.Item != nil {
		if strSlice, ok := raw.([]string); ok {
			var converted []interface{}
			for _, str := range strSlice {
				conv, err := convertValue(str, *s.Item)
				if err != nil {
					converted = append(converted, str)
				} else {
					converted = append(converted, conv)
				}
			}
			return converted, nil
		}
	}
	return raw, nil
}

func generateMockValue(s core.Schema) interface{} {
	switch s.Type {
	case core.TypeScalar:
		switch s.ScalarType {
		case core.ScalarString:
			for _, c := range s.Constraints {
				if c.Kind == "format" {
					switch c.Value {
					case "uuid":
						return "123e4567-e89b-12d3-a456-426614174000"
					case "date-time":
						return "2026-05-19T23:00:00Z"
					case "email":
						return "mock@example.com"
					}
				}
			}
			return "mock_string"

		case core.ScalarNumber:
			minVal := 42.0
			for _, c := range s.Constraints {
				if c.Kind == "min" {
					if val, err := strconv.ParseFloat(c.Value, 64); err == nil {
						minVal = val
					}
				}
			}
			return minVal

		case core.ScalarInteger:
			minVal := int64(42)
			for _, c := range s.Constraints {
				if c.Kind == "min" {
					if val, err := strconv.ParseInt(c.Value, 10, 64); err == nil {
						minVal = val
					}
				}
			}
			return minVal

		case core.ScalarBoolean:
			return true
		}

	case core.TypeEnum:
		if len(s.EnumValues) > 0 {
			return s.EnumValues[0]
		}
		return "mock_enum_value"

	case core.TypeArray:
		if s.Item != nil {
			return []interface{}{generateMockValue(*s.Item)}
		}
		return []interface{}{}

	case core.TypeObject:
		obj := make(map[string]interface{})
		for name, propSchema := range s.Properties {
			obj[name] = generateMockValue(propSchema)
		}
		return obj
	}

	return nil
}
