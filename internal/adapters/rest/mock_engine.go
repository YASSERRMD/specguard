package rest

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YASSERRMD/specguard/internal/core"
)

type statusResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (srw *statusResponseWriter) WriteHeader(code int) {
	srw.statusCode = code
	srw.ResponseWriter.WriteHeader(code)
}

func (srw *statusResponseWriter) Write(b []byte) (int, error) {
	if srw.statusCode == 0 {
		srw.statusCode = http.StatusOK
	}
	return srw.ResponseWriter.Write(b)
}

func (srw *statusResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := srw.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}


// MockServer implements core.RunnableMock for HTTP mock servers.
type MockServer struct {
	spec        *core.NormalizedSpec
	config      core.MockConfig
	listener    net.Listener
	server      *http.Server
	address     string
	mu          sync.Mutex
	running     bool
	rateLimiter *core.RateLimiter

	stateMu     sync.RWMutex
	state       map[string][]map[string]interface{}
	counters    map[string]int64
	initialized map[string]bool
}

// NewMockServer creates a new instance of MockServer.
func NewMockServer(spec *core.NormalizedSpec, config core.MockConfig) *MockServer {
	limit := config.RateLimit
	if limit == 0 {
		limit = 100 // default 100 rps
	} else if limit < 0 {
		limit = 0 // unlimited
	}
	ms := &MockServer{
		spec:        spec,
		config:      config,
		state:       make(map[string][]map[string]interface{}),
		counters:    make(map[string]int64),
		initialized: make(map[string]bool),
		rateLimiter: core.NewRateLimiter(limit, int(limit)+1),
	}
	ms.seedState()
	return ms
}

func (m *MockServer) seedState() {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if m.config.ProtocolConfig == nil {
		return
	}
	rawState, ok := m.config.ProtocolConfig["initial_state"]
	if !ok {
		return
	}
	stateMap, ok := rawState.(map[string]interface{})
	if !ok {
		return
	}
	for path, itemsVal := range stateMap {
		itemsSlice, ok := itemsVal.([]interface{})
		if !ok {
			continue
		}
		var records []map[string]interface{}
		for _, itemVal := range itemsSlice {
			if itemMap, ok := itemVal.(map[string]interface{}); ok {
				records = append(records, itemMap)
			}
		}
		m.state[path] = records
		m.initialized[path] = true
	}
}

func (m *MockServer) ResetState() {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	m.state = make(map[string][]map[string]interface{})
	m.counters = make(map[string]int64)
	m.initialized = make(map[string]bool)
}

func parseResourcePath(pattern string, pathParams map[string]string) (string, string, bool) {
	pattern = "/" + strings.Trim(pattern, "/")
	if pattern == "/" {
		return "/", "", false
	}
	segs := strings.Split(pattern, "/")
	lastSeg := segs[len(segs)-1]

	isMember := false
	var itemId string
	var collectionPattern string

	if strings.HasPrefix(lastSeg, "{") && strings.HasSuffix(lastSeg, "}") {
		isMember = true
		paramName := lastSeg[1 : len(lastSeg)-1]
		itemId = pathParams[paramName]
		collectionPattern = strings.Join(segs[:len(segs)-1], "/")
	} else {
		collectionPattern = pattern
	}

	collectionKey := collectionPattern
	for name, val := range pathParams {
		placeholder := "{" + name + "}"
		collectionKey = strings.ReplaceAll(collectionKey, placeholder, val)
	}

	return collectionKey, itemId, isMember
}

func mergeValues(dest, src interface{}) interface{} {
	if src == nil {
		return dest
	}
	if dest == nil {
		return src
	}

	destMap, destOk := dest.(map[string]interface{})
	srcMap, srcOk := src.(map[string]interface{})

	if destOk && srcOk {
		merged := make(map[string]interface{})
		for k, v := range destMap {
			merged[k] = v
		}
		for k, v := range srcMap {
			if existing, exists := merged[k]; exists {
				merged[k] = mergeValues(existing, v)
			} else {
				merged[k] = v
			}
		}
		return merged
	}

	return src
}

func (m *MockServer) generateIdForCollection(collectionKey string, schema *core.Schema) interface{} {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	var idSchema *core.Schema
	if schema != nil && schema.Type == core.TypeObject {
		if idProp, ok := schema.Properties["id"]; ok {
			idSchema = &idProp
		}
	}

	isInteger := false
	isUUID := false
	if idSchema != nil && idSchema.Type == core.TypeScalar {
		if idSchema.ScalarType == core.ScalarInteger || idSchema.ScalarType == core.ScalarNumber {
			isInteger = true
		} else if idSchema.ScalarType == core.ScalarString {
			for _, c := range idSchema.Constraints {
				if c.Kind == "format" && c.Value == "uuid" {
					isUUID = true
					break
				}
			}
		}
	}

	if isInteger {
		m.counters[collectionKey]++
		return float64(m.counters[collectionKey])
	}

	if isUUID {
		m.counters[collectionKey]++
		return fmt.Sprintf("123e4567-e89b-12d3-a456-%012d", m.counters[collectionKey])
	}

	m.counters[collectionKey]++
	return fmt.Sprintf("id-%d", m.counters[collectionKey])
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
	startTime := time.Now()
	srw := &statusResponseWriter{ResponseWriter: w}
	w = srw

	defer func() {
		duration := time.Since(startTime)
		statusStr := strconv.Itoa(srw.statusCode)
		if srw.statusCode == 0 {
			statusStr = "200"
			srw.statusCode = 200
		}
		
		// Record metrics
		core.Metrics.RecordMockRequest(m.config.ID, r.Method, r.URL.Path, statusStr)

		// Structured logging
		slog.Info("mock request processed",
			"mock_id", m.config.ID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", srw.statusCode,
			"duration_ms", duration.Milliseconds(),
		)
	}()

	// 1. Rate Limiting
	if m.rateLimiter != nil && !m.rateLimiter.Allow() {
		m.writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	// 2. Max Request Body Size Limit
	if m.config.MaxRequestBodySize > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, m.config.MaxRequestBodySize)
	}

	if r.URL.Path == "/__reset" && r.Method == http.MethodPost {
		m.ResetState()
		w.WriteHeader(http.StatusOK)
		return
	}

	if m.evaluateChaos(w, r) {
		return
	}

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
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				m.writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
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

	// Scenario selection
	scenarioName := r.URL.Query().Get("_scenario")
	if scenarioName == "" {
		scenarioName = r.URL.Query().Get("scenario")
	}
	if scenarioName == "" {
		scenarioName = r.Header.Get("X-Mock-Scenario")
	}
	if scenarioName == "" {
		scenarioName = r.Header.Get("X-Scenario")
	}
	scenarioName = strings.TrimSpace(scenarioName)

	if scenarioName != "" && scenarioName != "success" {
		if m.handleScenario(w, r, matchedOp, scenarioName) {
			return
		}
	}

	// Try stateful CRUD
	if m.handleStatefulCRUD(w, r, matchedOp, pathParams, reqVal) {
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

func (m *MockServer) handleScenario(w http.ResponseWriter, r *http.Request, matchedOp *core.Operation, scenarioName string) bool {
	var status int
	var body interface{}
	var headers map[string]interface{}
	found := false

	parseScenarioDef := func(def interface{}) bool {
		mDef, ok := def.(map[string]interface{})
		if !ok {
			return false
		}
		if sVal, ok := mDef["status"]; ok {
			switch v := sVal.(type) {
			case float64:
				status = int(v)
			case int:
				status = v
			case int64:
				status = int(v)
			case string:
				if st, err := strconv.Atoi(v); err == nil {
					status = st
				}
			}
		}
		if bVal, ok := mDef["body"]; ok {
			body = bVal
		}
		if hVal, ok := mDef["headers"]; ok {
			if hm, ok := hVal.(map[string]interface{}); ok {
				headers = hm
			}
		}
		return true
	}

	if m.config.ProtocolConfig != nil {
		if ops, ok := m.config.ProtocolConfig["operations"].(map[string]interface{}); ok {
			if opDef, ok := ops[matchedOp.ID].(map[string]interface{}); ok {
				if scs, ok := opDef["scenarios"].(map[string]interface{}); ok {
					if scDef, ok := scs[scenarioName]; ok {
						if parseScenarioDef(scDef) {
							found = true
						}
					}
				}
			}
		}
		if !found {
			if scs, ok := m.config.ProtocolConfig["scenarios"].(map[string]interface{}); ok {
				if scDef, ok := scs[scenarioName]; ok {
					if parseScenarioDef(scDef) {
						found = true
					}
				}
			}
		}
	}

	if !found && matchedOp.Metadata != nil {
		statusKey := fmt.Sprintf("scenario:%s:status", scenarioName)
		bodyKey := fmt.Sprintf("scenario:%s:body", scenarioName)
		headersKey := fmt.Sprintf("scenario:%s:headers", scenarioName)

		if stStr, ok := matchedOp.Metadata[statusKey]; ok {
			if st, err := strconv.Atoi(stStr); err == nil {
				status = st
				found = true
			}
		}
		if bodyStr, ok := matchedOp.Metadata[bodyKey]; ok {
			var jsonVal interface{}
			if err := json.Unmarshal([]byte(bodyStr), &jsonVal); err == nil {
				body = jsonVal
			} else {
				body = bodyStr
			}
			found = true
		}
		if hStr, ok := matchedOp.Metadata[headersKey]; ok {
			var hMap map[string]interface{}
			if err := json.Unmarshal([]byte(hStr), &hMap); err == nil {
				headers = hMap
			}
		}
	}

	if !found {
		switch scenarioName {
		case "not-found":
			status = http.StatusNotFound
			if shape, exists := matchedOp.ErrorShapes["404"]; exists {
				body = generateMockValue(shape)
			} else {
				body = map[string]string{"error": "Not Found"}
			}
			found = true

		case "server-error":
			status = http.StatusInternalServerError
			if shape, exists := matchedOp.ErrorShapes["500"]; exists {
				body = generateMockValue(shape)
			} else {
				body = map[string]string{"error": "Internal Server Error"}
			}
			found = true

		case "empty-result":
			successStatus := 200
			var successSchema *core.Schema
			lowestSuccess := 999
			for statusStr, schema := range matchedOp.Output.Properties {
				st, err := strconv.Atoi(statusStr)
				if err == nil && st >= 200 && st < 300 {
					if st < lowestSuccess {
						lowestSuccess = st
						s := schema
						successSchema = &s
					}
				}
			}
			if lowestSuccess != 999 {
				successStatus = lowestSuccess
			}

			status = successStatus
			if successSchema != nil {
				body = m.formatListResponse(*successSchema, nil, 10, 0)
			} else {
				body = nil
			}
			found = true
		}
	}

	if found {
		for k, v := range headers {
			w.Header().Set(k, fmt.Sprintf("%v", v))
		}
		if status == 0 {
			status = 200
		}
		if body == nil {
			w.WriteHeader(status)
		} else {
			m.writeJSON(w, status, body)
		}
		return true
	}

	m.writeError(w, http.StatusBadRequest, fmt.Sprintf("scenario %q is not defined", scenarioName))
	return true
}

func (m *MockServer) handleStatefulCRUD(w http.ResponseWriter, r *http.Request, matchedOp *core.Operation, pathParams map[string]string, reqVal map[string]interface{}) bool {
	opPath := matchedOp.Metadata["path"]
	collectionKey, itemId, isMember := parseResourcePath(opPath, pathParams)

	successStatus := 200
	var successSchema *core.Schema
	lowestSuccess := 999
	for statusStr, schema := range matchedOp.Output.Properties {
		status, err := strconv.Atoi(statusStr)
		if err == nil && status >= 200 && status < 300 {
			if status < lowestSuccess {
				lowestSuccess = status
				s := schema
				successSchema = &s
			}
		}
	}
	if lowestSuccess != 999 {
		successStatus = lowestSuccess
	}

	switch r.Method {
	case http.MethodPost:
		if isMember {
			return false
		}
		bodyVal, exists := reqVal["body"]
		if !exists {
			bodyVal = make(map[string]interface{})
		}
		bodyMap, ok := bodyVal.(map[string]interface{})
		if !ok {
			return false
		}

		idKey := ""
		var currentId interface{}
		for k, v := range bodyMap {
			if strings.ToLower(k) == "id" {
				idKey = k
				currentId = v
				break
			}
		}

		if currentId == nil || fmt.Sprintf("%v", currentId) == "" {
			var respSchema *core.Schema
			if successSchema != nil {
				respSchema = successSchema
			}
			currentId = m.generateIdForCollection(collectionKey, respSchema)
			if idKey == "" {
				idKey = "id"
			}
			bodyMap[idKey] = currentId
		}

		m.stateMu.Lock()
		m.state[collectionKey] = append(m.state[collectionKey], bodyMap)
		m.initialized[collectionKey] = true
		m.stateMu.Unlock()

		var respVal interface{} = bodyMap
		if successSchema != nil {
			baseline := generateMockValue(*successSchema)
			respVal = mergeValues(baseline, bodyMap)
		}
		m.writeJSON(w, successStatus, respVal)
		return true

	case http.MethodGet:
		m.stateMu.RLock()
		isInit := m.initialized[collectionKey]
		m.stateMu.RUnlock()
		if !isInit {
			return false
		}

		if isMember {
			m.stateMu.RLock()
			records := m.state[collectionKey]
			var foundRecord map[string]interface{}
			for _, rec := range records {
				for k, v := range rec {
					if strings.ToLower(k) == "id" && fmt.Sprintf("%v", v) == itemId {
						foundRecord = rec
						break
					}
				}
				if foundRecord != nil {
					break
				}
			}
			m.stateMu.RUnlock()

			if foundRecord != nil {
				var respVal interface{} = foundRecord
				if successSchema != nil {
					baseline := generateMockValue(*successSchema)
					respVal = mergeValues(baseline, foundRecord)
				}
				m.writeJSON(w, successStatus, respVal)
				return true
			}

			m.writeResourceNotFound(w, matchedOp)
			return true
		} else {
			m.stateMu.RLock()
			records := make([]interface{}, len(m.state[collectionKey]))
			for i, rec := range m.state[collectionKey] {
				records[i] = rec
			}
			m.stateMu.RUnlock()

			limit := 10
			offset := 0

			queryMap, _ := reqVal["query"].(map[string]interface{})
			if queryMap != nil {
				for _, k := range []string{"limit", "per_page", "size", "pageSize"} {
					if val, ok := queryMap[k]; ok {
						if num, err := toFloat64(val); err == nil && num > 0 {
							limit = int(num)
						}
					}
				}
				offsetFound := false
				for _, k := range []string{"offset", "skip"} {
					if val, ok := queryMap[k]; ok {
						if num, err := toFloat64(val); err == nil && num >= 0 {
							offset = int(num)
							offsetFound = true
						}
					}
				}
				if !offsetFound {
					for _, k := range []string{"page", "pageNum"} {
						if val, ok := queryMap[k]; ok {
							if num, err := toFloat64(val); err == nil && num >= 1 {
								page := int(num)
								offset = (page - 1) * limit
							}
						}
					}
				}
			}

			var respVal interface{}
			if successSchema != nil {
				respVal = m.formatListResponse(*successSchema, records, limit, offset)
			} else {
				respVal = records
			}
			m.writeJSON(w, successStatus, respVal)
			return true
		}

	case http.MethodPut:
		if !isMember {
			return false
		}
		m.stateMu.RLock()
		isInit := m.initialized[collectionKey]
		m.stateMu.RUnlock()
		if !isInit {
			return false
		}

		bodyVal, exists := reqVal["body"]
		if !exists {
			bodyVal = make(map[string]interface{})
		}
		bodyMap, ok := bodyVal.(map[string]interface{})
		if !ok {
			return false
		}

		m.stateMu.Lock()
		records := m.state[collectionKey]
		foundIdx := -1
		var existingRecord map[string]interface{}
		for i, rec := range records {
			for k, v := range rec {
				if strings.ToLower(k) == "id" && fmt.Sprintf("%v", v) == itemId {
					foundIdx = i
					existingRecord = rec
					break
				}
			}
			if foundIdx != -1 {
				break
			}
		}

		if foundIdx != -1 {
			updatedRecord := make(map[string]interface{})
			for k, v := range existingRecord {
				updatedRecord[k] = v
			}
			for k, v := range bodyMap {
				if strings.ToLower(k) == "id" {
					continue
				}
				updatedRecord[k] = v
			}
			m.state[collectionKey][foundIdx] = updatedRecord
			m.stateMu.Unlock()

			var respVal interface{} = updatedRecord
			if successSchema != nil {
				baseline := generateMockValue(*successSchema)
				respVal = mergeValues(baseline, updatedRecord)
			}
			m.writeJSON(w, successStatus, respVal)
			return true
		}
		m.stateMu.Unlock()

		m.writeResourceNotFound(w, matchedOp)
		return true

	case http.MethodDelete:
		if !isMember {
			return false
		}
		m.stateMu.RLock()
		isInit := m.initialized[collectionKey]
		m.stateMu.RUnlock()
		if !isInit {
			return false
		}

		m.stateMu.Lock()
		records := m.state[collectionKey]
		foundIdx := -1
		for i, rec := range records {
			for k, v := range rec {
				if strings.ToLower(k) == "id" && fmt.Sprintf("%v", v) == itemId {
					foundIdx = i
					break
				}
			}
			if foundIdx != -1 {
				break
			}
		}

		if foundIdx != -1 {
			m.state[collectionKey] = append(records[:foundIdx], records[foundIdx+1:]...)
			m.stateMu.Unlock()

			if successStatus == http.StatusNoContent {
				w.WriteHeader(http.StatusNoContent)
			} else {
				if successSchema != nil {
					m.writeJSON(w, successStatus, generateMockValue(*successSchema))
				} else {
					w.WriteHeader(successStatus)
				}
			}
			return true
		}
		m.stateMu.Unlock()

		m.writeResourceNotFound(w, matchedOp)
		return true
	}

	return false
}

func (m *MockServer) writeResourceNotFound(w http.ResponseWriter, matchedOp *core.Operation) {
	if shape, exists := matchedOp.ErrorShapes["404"]; exists {
		m.writeJSON(w, http.StatusNotFound, generateMockValue(shape))
	} else {
		m.writeError(w, http.StatusNotFound, "resource not found")
	}
}

func (m *MockServer) formatListResponse(schema core.Schema, records []interface{}, limit, offset int) interface{} {
	total := len(records)
	start := offset
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	var sliced []interface{}
	if start < end {
		sliced = records[start:end]
	} else {
		sliced = []interface{}{}
	}

	if schema.Type == core.TypeArray {
		items := []interface{}{}
		for _, rec := range sliced {
			if schema.Item != nil {
				baseline := generateMockValue(*schema.Item)
				items = append(items, mergeValues(baseline, rec))
			} else {
				items = append(items, rec)
			}
		}
		return items
	}

	if schema.Type == core.TypeObject {
		obj := generateMockValue(schema).(map[string]interface{})
		arrayKey := ""
		var arrayItemSchema *core.Schema
		for k, prop := range schema.Properties {
			if prop.Type == core.TypeArray {
				arrayKey = k
				arrayItemSchema = prop.Item
				break
			}
		}

		if arrayKey != "" {
			items := []interface{}{}
			for _, rec := range sliced {
				if arrayItemSchema != nil {
					baseline := generateMockValue(*arrayItemSchema)
					items = append(items, mergeValues(baseline, rec))
				} else {
					items = append(items, rec)
				}
			}
			obj[arrayKey] = items
		}

		for k := range schema.Properties {
			kl := strings.ToLower(k)
			if kl == "total" || kl == "count" || kl == "total_results" || kl == "total_elements" {
				obj[k] = float64(total)
			} else if kl == "limit" || kl == "size" || kl == "per_page" {
				obj[k] = float64(limit)
			} else if kl == "offset" {
				obj[k] = float64(offset)
			} else if kl == "page" {
				pageVal := 1
				if limit > 0 {
					pageVal = (offset / limit) + 1
				}
				obj[k] = float64(pageVal)
			}
		}
		return obj
	}

	return generateMockValue(schema)
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

func toFloat64(val interface{}) (float64, error) {
	switch v := val.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int16:
		return float64(v), nil
	case int8:
		return float64(v), nil
	case uint:
		return float64(v), nil
	case uint64:
		return float64(v), nil
	case uint32:
		return float64(v), nil
	case uint16:
		return float64(v), nil
	case uint8:
		return float64(v), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", val)
	}
}

func (m *MockServer) evaluateChaos(w http.ResponseWriter, r *http.Request) bool {
	latencyMs := 0
	latencyJitterMs := 0
	errorRate := 0.0
	errorStatus := 500
	dropConnectionRate := 0.0

	if m.config.Chaos != nil {
		latencyMs = m.config.Chaos.LatencyMs
		latencyJitterMs = m.config.Chaos.LatencyJitterMs
		errorRate = m.config.Chaos.ErrorRate
		errorStatus = m.config.Chaos.ErrorStatus
		dropConnectionRate = m.config.Chaos.DropConnectionRate
	}

	if hDelay := r.Header.Get("X-Chaos-Delay"); hDelay != "" {
		if val, err := strconv.Atoi(hDelay); err == nil {
			latencyMs = val
		} else if dur, err := time.ParseDuration(hDelay); err == nil {
			latencyMs = int(dur.Milliseconds())
		}
	} else if hLatency := r.Header.Get("X-Chaos-Latency"); hLatency != "" {
		if val, err := strconv.Atoi(hLatency); err == nil {
			latencyMs = val
		} else if dur, err := time.ParseDuration(hLatency); err == nil {
			latencyMs = int(dur.Milliseconds())
		}
	}

	if hJitter := r.Header.Get("X-Chaos-Delay-Jitter"); hJitter != "" {
		if val, err := strconv.Atoi(hJitter); err == nil {
			latencyJitterMs = val
		} else if dur, err := time.ParseDuration(hJitter); err == nil {
			latencyJitterMs = int(dur.Milliseconds())
		}
	} else if hLatencyJitter := r.Header.Get("X-Chaos-Latency-Jitter"); hLatencyJitter != "" {
		if val, err := strconv.Atoi(hLatencyJitter); err == nil {
			latencyJitterMs = val
		} else if dur, err := time.ParseDuration(hLatencyJitter); err == nil {
			latencyJitterMs = int(dur.Milliseconds())
		}
	}

	if hErrRate := r.Header.Get("X-Chaos-Error-Rate"); hErrRate != "" {
		if val, err := strconv.ParseFloat(hErrRate, 64); err == nil {
			errorRate = val
		}
	}

	if hErrStatus := r.Header.Get("X-Chaos-Error-Status"); hErrStatus != "" {
		if val, err := strconv.Atoi(hErrStatus); err == nil {
			errorStatus = val
		}
	}

	if hDropRate := r.Header.Get("X-Chaos-Drop-Rate"); hDropRate != "" {
		if val, err := strconv.ParseFloat(hDropRate, 64); err == nil {
			dropConnectionRate = val
		}
	} else if hDropConnRate := r.Header.Get("X-Chaos-Drop-Connection-Rate"); hDropConnRate != "" {
		if val, err := strconv.ParseFloat(hDropConnRate, 64); err == nil {
			dropConnectionRate = val
		}
	}

	if errorStatus <= 0 {
		errorStatus = 500
	}

	if dropConnectionRate > 0 {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		if rng.Float64() < dropConnectionRate {
			if hijacker, ok := w.(http.Hijacker); ok {
				conn, _, err := hijacker.Hijack()
				if err == nil && conn != nil {
					conn.Close()
					return true
				}
			}
			w.Header().Set("Connection", "close")
			w.WriteHeader(http.StatusInternalServerError)
			return true
		}
	}

	if latencyMs > 0 {
		delay := time.Duration(latencyMs) * time.Millisecond
		if latencyJitterMs > 0 {
			rng := rand.New(rand.NewSource(time.Now().UnixNano()))
			jitter := rng.Intn(latencyJitterMs*2) - latencyJitterMs
			delay += time.Duration(jitter) * time.Millisecond
		}
		if delay > 0 {
			time.Sleep(delay)
		}
	}

	if errorRate > 0 {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		if rng.Float64() < errorRate {
			m.writeError(w, errorStatus, fmt.Sprintf("Chaos injection triggered: simulated error with status code %d", errorStatus))
			return true
		}
	}

	return false
}
