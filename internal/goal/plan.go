package goal

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type StepStatus string

const (
	StepStatusPending StepStatus = "pending"
	StepStatusActive  StepStatus = "active"
	StepStatusDone    StepStatus = "done"
	StepStatusBlocked StepStatus = "blocked"
	StepStatusSkipped StepStatus = "skipped"
)

type CriterionStatus string

const (
	CriterionStatusPending CriterionStatus = "pending"
	CriterionStatusPassed  CriterionStatus = "passed"
	CriterionStatusFailed  CriterionStatus = "failed"
	CriterionStatusWaived  CriterionStatus = "waived"
)

type DependencyStatus string

const (
	DependencyStatusUnknown   DependencyStatus = "unknown"
	DependencyStatusAvailable DependencyStatus = "available"
	DependencyStatusMissing   DependencyStatus = "missing"
	DependencyStatusBlocked   DependencyStatus = "blocked"
)

type RiskStatus string

const (
	RiskStatusOpen      RiskStatus = "open"
	RiskStatusMitigated RiskStatus = "mitigated"
	RiskStatusAccepted  RiskStatus = "accepted"
	RiskStatusEscalated RiskStatus = "escalated"
)

type RiskSeverity string

const (
	RiskSeverityLow      RiskSeverity = "low"
	RiskSeverityMedium   RiskSeverity = "medium"
	RiskSeverityHigh     RiskSeverity = "high"
	RiskSeverityCritical RiskSeverity = "critical"
)

type EvidenceType string

const (
	EvidenceTypeCommand  EvidenceType = "command"
	EvidenceTypeTest     EvidenceType = "test"
	EvidenceTypeGit      EvidenceType = "git"
	EvidenceTypeAPI      EvidenceType = "api"
	EvidenceTypeDB       EvidenceType = "db"
	EvidenceTypeDoc      EvidenceType = "doc"
	EvidenceTypeArtifact EvidenceType = "artifact"
	EvidenceTypeManual   EvidenceType = "manual"
)

type GoalPlan struct {
	GoalID             string           `json:"goal_id"`
	Version            int              `json:"version"`
	Summary            string           `json:"summary,omitempty"`
	AcceptanceCriteria []GoalCriterion  `json:"acceptance_criteria,omitempty"`
	Steps              []GoalStep       `json:"steps,omitempty"`
	Dependencies       []GoalDependency `json:"dependencies,omitempty"`
	Risks              []GoalRisk       `json:"risks,omitempty"`
	CurrentStepID      string           `json:"current_step_id,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
}

type GoalStep struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Status      StepStatus `json:"status"`
	Rationale   string     `json:"rationale,omitempty"`
	DependsOn   []string   `json:"depends_on,omitempty"`
	EvidenceIDs []string   `json:"evidence_ids,omitempty"`
}

type GoalCriterion struct {
	ID          string          `json:"id"`
	Description string          `json:"description"`
	Required    bool            `json:"required"`
	Status      CriterionStatus `json:"status"`
	EvidenceIDs []string        `json:"evidence_ids,omitempty"`
}

type GoalDependency struct {
	ID          string           `json:"id"`
	Type        string           `json:"type,omitempty"`
	Description string           `json:"description"`
	Required    bool             `json:"required"`
	Status      DependencyStatus `json:"status"`
	EvidenceIDs []string         `json:"evidence_ids,omitempty"`
}

type GoalRisk struct {
	ID          string       `json:"id"`
	Type        string       `json:"type,omitempty"`
	Description string       `json:"description"`
	Severity    RiskSeverity `json:"severity"`
	Mitigation  string       `json:"mitigation,omitempty"`
	Status      RiskStatus   `json:"status"`
}

type GoalEvidence struct {
	ID        string          `json:"id"`
	GoalID    string          `json:"goal_id"`
	Type      EvidenceType    `json:"type"`
	Summary   string          `json:"summary"`
	Command   string          `json:"command,omitempty"`
	ExitCode  *int            `json:"exit_code,omitempty"`
	Passed    bool            `json:"passed"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

func (s StepStatus) Valid() bool {
	switch s {
	case StepStatusPending, StepStatusActive, StepStatusDone, StepStatusBlocked, StepStatusSkipped:
		return true
	default:
		return false
	}
}

func (s CriterionStatus) Valid() bool {
	switch s {
	case CriterionStatusPending, CriterionStatusPassed, CriterionStatusFailed, CriterionStatusWaived:
		return true
	default:
		return false
	}
}

func (s DependencyStatus) Valid() bool {
	switch s {
	case DependencyStatusUnknown, DependencyStatusAvailable, DependencyStatusMissing, DependencyStatusBlocked:
		return true
	default:
		return false
	}
}

func (s RiskStatus) Valid() bool {
	switch s {
	case RiskStatusOpen, RiskStatusMitigated, RiskStatusAccepted, RiskStatusEscalated:
		return true
	default:
		return false
	}
}

func (s RiskSeverity) Valid() bool {
	switch s {
	case RiskSeverityLow, RiskSeverityMedium, RiskSeverityHigh, RiskSeverityCritical:
		return true
	default:
		return false
	}
}

func (t EvidenceType) Valid() bool {
	switch t {
	case EvidenceTypeCommand, EvidenceTypeTest, EvidenceTypeGit, EvidenceTypeAPI, EvidenceTypeDB, EvidenceTypeDoc, EvidenceTypeArtifact, EvidenceTypeManual:
		return true
	default:
		return false
	}
}

func (p GoalPlan) Validate() error {
	if strings.TrimSpace(p.GoalID) == "" {
		return errors.New("goal plan goal id is required")
	}
	if p.Version <= 0 {
		return errors.New("goal plan version must be positive")
	}
	stepIDs := map[string]bool{}
	for _, step := range p.Steps {
		if err := step.Validate(); err != nil {
			return err
		}
		if stepIDs[step.ID] {
			return fmt.Errorf("duplicate goal step id: %s", step.ID)
		}
		stepIDs[step.ID] = true
	}
	if p.CurrentStepID != "" && !stepIDs[p.CurrentStepID] {
		return fmt.Errorf("current step id not found: %s", p.CurrentStepID)
	}
	for _, criterion := range p.AcceptanceCriteria {
		if err := criterion.Validate(); err != nil {
			return err
		}
	}
	for _, dependency := range p.Dependencies {
		if err := dependency.Validate(); err != nil {
			return err
		}
	}
	for _, risk := range p.Risks {
		if err := risk.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (s GoalStep) Validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return errors.New("goal step id is required")
	}
	if strings.TrimSpace(s.Title) == "" {
		return errors.New("goal step title is required")
	}
	if !s.Status.Valid() {
		return fmt.Errorf("invalid goal step status: %s", s.Status)
	}
	return nil
}

func (c GoalCriterion) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return errors.New("goal criterion id is required")
	}
	if strings.TrimSpace(c.Description) == "" {
		return errors.New("goal criterion description is required")
	}
	if !c.Status.Valid() {
		return fmt.Errorf("invalid goal criterion status: %s", c.Status)
	}
	return nil
}

func (d GoalDependency) Validate() error {
	if strings.TrimSpace(d.ID) == "" {
		return errors.New("goal dependency id is required")
	}
	if strings.TrimSpace(d.Description) == "" {
		return errors.New("goal dependency description is required")
	}
	if !d.Status.Valid() {
		return fmt.Errorf("invalid goal dependency status: %s", d.Status)
	}
	return nil
}

func (r GoalRisk) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return errors.New("goal risk id is required")
	}
	if strings.TrimSpace(r.Description) == "" {
		return errors.New("goal risk description is required")
	}
	if !r.Severity.Valid() {
		return fmt.Errorf("invalid goal risk severity: %s", r.Severity)
	}
	if !r.Status.Valid() {
		return fmt.Errorf("invalid goal risk status: %s", r.Status)
	}
	return nil
}

func (e GoalEvidence) Validate() error {
	if strings.TrimSpace(e.ID) == "" {
		return errors.New("goal evidence id is required")
	}
	if strings.TrimSpace(e.GoalID) == "" {
		return errors.New("goal evidence goal id is required")
	}
	if !e.Type.Valid() {
		return fmt.Errorf("invalid goal evidence type: %s", e.Type)
	}
	if strings.TrimSpace(e.Summary) == "" {
		return errors.New("goal evidence summary is required")
	}
	if len(e.Payload) > 0 && !json.Valid(e.Payload) {
		return errors.New("goal evidence payload must be valid JSON")
	}
	return nil
}
