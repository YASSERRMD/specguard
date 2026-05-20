package core

// ProtocolAdapter specifies the methods a protocol-specific plugin must implement.
type ProtocolAdapter interface {
	// LoadSpec parses a raw specification document into the normalized spec model.
	LoadSpec(source []byte) (*NormalizedSpec, error)

	// GenerateMock spins up a runnable mock server driven by the normalized spec.
	GenerateMock(spec *NormalizedSpec, config MockConfig) (RunnableMock, error)

	// RunContractChecks executes contract tests against a real service target.
	RunContractChecks(spec *NormalizedSpec, targetURL string) (CheckResult, error)

	// NormalizeResult parses raw integration/testing output into a standard DriftReport.
	NormalizeResult(rawResult interface{}) (*DriftReport, error)
}

// MockConfig holds configurations for setting up mock servers.
type MockConfig struct {
	Port           int                    `json:"port"`
	Host           string                 `json:"host"`
	ProtocolConfig map[string]interface{} `json:"protocol_config,omitempty"`
	Chaos          *ChaosConfig           `json:"chaos,omitempty"`
}

// ChaosConfig defines parameters for fault and chaos injection.
type ChaosConfig struct {
	LatencyMs          int     `json:"latency_ms,omitempty"`
	LatencyJitterMs    int     `json:"latency_jitter_ms,omitempty"`
	ErrorRate          float64 `json:"error_rate,omitempty"`
	ErrorStatus        int     `json:"error_status,omitempty"`
	DropConnectionRate float64 `json:"drop_connection_rate,omitempty"`
}

// RunnableMock represents a running mock server instance.
type RunnableMock interface {
	// Start starts the mock server in the background.
	Start() error

	// Stop gracefully terminates the mock server.
	Stop() error

	// GetAddress returns the listening URL or address of the mock server.
	GetAddress() string
}

// CheckResult represents the outcome of a contract testing run.
type CheckResult struct {
	Passed      bool         `json:"passed"`
	DriftReport *DriftReport `json:"drift_report"`
}

// FindingKind classifies the type of specification divergence.
type FindingKind string

const (
	KindMissing            FindingKind = "missing"
	KindAdded              FindingKind = "added"
	KindTypeChanged        FindingKind = "type-changed"
	KindConstraintViolated FindingKind = "constraint-violated"
)

// Severity indicates the critical level of a finding.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Finding represents a single structural drift or validation failure.
type Finding struct {
	Location string      `json:"location"` // JSON-path like notation indicating where drift was found
	Kind     FindingKind `json:"kind"`
	Expected string      `json:"expected"`
	Actual   string      `json:"actual"`
	Severity Severity    `json:"severity"`
}

// DriftReport holds the full findings of a contract run.
type DriftReport struct {
	Findings []Finding `json:"findings"`
}
