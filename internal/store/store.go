package store

import (
	"time"

	"github.com/YASSERRMD/specguard/internal/core"
)

// ContractRun holds outcome details of a contract validation run.
type ContractRun struct {
	ID          string            `json:"id"`
	SpecID      string            `json:"spec_id"`
	TargetURL   string            `json:"target_url"`
	Passed      bool              `json:"passed"`
	DriftReport *core.DriftReport `json:"drift_report"`
	CreatedAt   time.Time         `json:"created_at"`
}

// Store specifies a database-agnostic persistence layer.
type Store interface {
	SaveSpec(specID string, spec *core.NormalizedSpec) error
	LoadSpec(specID string) (*core.NormalizedSpec, error)
	SaveMockConfig(mockID string, config *core.MockConfig) error
	LoadMockConfig(mockID string) (*core.MockConfig, error)
	SaveContractRun(run *ContractRun) error
	GetContractRun(runID string) (*ContractRun, error)
	ListContractRuns(specID string) ([]ContractRun, error)
	Close() error
}
