// Package contextcapture provides types and utilities for context capture.
// This file defines plan, milestone, and tasklet bead schemas for RFC-002 Phase 4.
package contextcapture

import "time"

// Plan status constants
const (
	PlanStatusDraft      = "draft"
	PlanStatusActive     = "active"
	PlanStatusCompleted  = "completed"
	PlanStatusAbandoned  = "abandoned"
)

// Milestone status constants
const (
	MilestoneStatusPending    = "pending"
	MilestoneStatusInProgress = "in_progress"
	MilestoneStatusDone       = "done"
	MilestoneStatusSkipped    = "skipped"
)

// Tasklet status constants
const (
	TaskletStatusPending    = "pending"
	TaskletStatusInProgress = "in_progress"
	TaskletStatusDone       = "done"
	TaskletStatusBlocked    = "blocked"
)

// Plan represents a high-level implementation plan bead.
// Plans organize work into milestones and track overall progress.
type Plan struct {
	// PlanID is the unique identifier for this plan (e.g., "plan-rfc002-phase4")
	PlanID string `json:"plan_id" yaml:"plan_id"`

	// Title is a short descriptive name for the plan
	Title string `json:"title" yaml:"title"`

	// Description provides additional context about the plan's goals
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Repo is the repository this plan applies to (e.g., "party/daemon")
	Repo string `json:"repo" yaml:"repo"`

	// Milestones lists milestone IDs in execution order
	Milestones []string `json:"milestones" yaml:"milestones"`

	// Status is the current plan status (draft, active, completed, abandoned)
	Status string `json:"status" yaml:"status"`

	// CreatedAt is when the plan was created
	CreatedAt time.Time `json:"created_at" yaml:"created_at"`

	// UpdatedAt is when the plan was last modified
	UpdatedAt time.Time `json:"updated_at" yaml:"updated_at"`

	// Labels are optional key-value pairs for categorization
	Labels map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// Milestone represents a phase or section within a plan.
// Milestones group related tasklets and track section progress.
type Milestone struct {
	// MilestoneID is the unique identifier (e.g., "ms-4.1")
	MilestoneID string `json:"milestone_id" yaml:"milestone_id"`

	// PlanID references the parent plan
	PlanID string `json:"plan_id" yaml:"plan_id"`

	// MilestoneNum is the sequence number within the plan (1-based)
	MilestoneNum int `json:"milestone_num" yaml:"milestone_num"`

	// Name is a short descriptive name (e.g., "Plan Bead Format")
	Name string `json:"name" yaml:"name"`

	// Description provides details about the milestone's scope
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Status is the current milestone status (pending, in_progress, done, skipped)
	Status string `json:"status" yaml:"status"`

	// Tasklets lists tasklet IDs belonging to this milestone
	Tasklets []string `json:"tasklets,omitempty" yaml:"tasklets,omitempty"`

	// CreatedAt is when the milestone was created
	CreatedAt time.Time `json:"created_at" yaml:"created_at"`

	// UpdatedAt is when the milestone was last modified
	UpdatedAt time.Time `json:"updated_at" yaml:"updated_at"`

	// Labels are optional key-value pairs for categorization
	Labels map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// Tasklet represents an individual implementation task within a milestone.
// Tasklets are the atomic units of work assigned to agents.
type Tasklet struct {
	// TaskletID is the unique identifier (e.g., "task-4.1.1")
	TaskletID string `json:"tasklet_id" yaml:"tasklet_id"`

	// PlanID references the parent plan
	PlanID string `json:"plan_id" yaml:"plan_id"`

	// MilestoneID references the parent milestone
	MilestoneID string `json:"milestone_id" yaml:"milestone_id"`

	// Thread groups related tasklets for parallel work (e.g., "schemas", "tests")
	Thread string `json:"thread,omitempty" yaml:"thread,omitempty"`

	// Name is a short descriptive name for the task
	Name string `json:"name" yaml:"name"`

	// Description provides implementation details
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Assignee is the agent role assigned (oc, cc, cx, or empty for unassigned)
	Assignee string `json:"assignee,omitempty" yaml:"assignee,omitempty"`

	// Status is the current tasklet status (pending, in_progress, done, blocked)
	Status string `json:"status" yaml:"status"`

	// BlockedBy lists tasklet IDs that must complete before this one
	BlockedBy []string `json:"blocked_by,omitempty" yaml:"blocked_by,omitempty"`

	// CreatedAt is when the tasklet was created
	CreatedAt time.Time `json:"created_at" yaml:"created_at"`

	// UpdatedAt is when the tasklet was last modified
	UpdatedAt time.Time `json:"updated_at" yaml:"updated_at"`

	// CompletedAt is when the tasklet was marked done (zero if not done)
	CompletedAt time.Time `json:"completed_at,omitempty" yaml:"completed_at,omitempty"`

	// Labels are optional key-value pairs for categorization
	Labels map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// ValidPlanStatuses returns all valid plan status values.
func ValidPlanStatuses() []string {
	return []string{PlanStatusDraft, PlanStatusActive, PlanStatusCompleted, PlanStatusAbandoned}
}

// ValidMilestoneStatuses returns all valid milestone status values.
func ValidMilestoneStatuses() []string {
	return []string{MilestoneStatusPending, MilestoneStatusInProgress, MilestoneStatusDone, MilestoneStatusSkipped}
}

// ValidTaskletStatuses returns all valid tasklet status values.
func ValidTaskletStatuses() []string {
	return []string{TaskletStatusPending, TaskletStatusInProgress, TaskletStatusDone, TaskletStatusBlocked}
}

// IsValidPlanStatus checks if a status is valid for a plan.
func IsValidPlanStatus(status string) bool {
	for _, s := range ValidPlanStatuses() {
		if s == status {
			return true
		}
	}
	return false
}

// IsValidMilestoneStatus checks if a status is valid for a milestone.
func IsValidMilestoneStatus(status string) bool {
	for _, s := range ValidMilestoneStatuses() {
		if s == status {
			return true
		}
	}
	return false
}

// IsValidTaskletStatus checks if a status is valid for a tasklet.
func IsValidTaskletStatus(status string) bool {
	for _, s := range ValidTaskletStatuses() {
		if s == status {
			return true
		}
	}
	return false
}

// NewPlan creates a new Plan with default values.
func NewPlan(planID, title, repo string) *Plan {
	now := time.Now()
	return &Plan{
		PlanID:     planID,
		Title:      title,
		Repo:       repo,
		Milestones: []string{},
		Status:     PlanStatusDraft,
		CreatedAt:  now,
		UpdatedAt:  now,
		Labels:     make(map[string]string),
	}
}

// NewMilestone creates a new Milestone with default values.
func NewMilestone(milestoneID, planID string, num int, name string) *Milestone {
	now := time.Now()
	return &Milestone{
		MilestoneID:  milestoneID,
		PlanID:       planID,
		MilestoneNum: num,
		Name:         name,
		Status:       MilestoneStatusPending,
		Tasklets:     []string{},
		CreatedAt:    now,
		UpdatedAt:    now,
		Labels:       make(map[string]string),
	}
}

// NewTasklet creates a new Tasklet with default values.
func NewTasklet(taskletID, planID, milestoneID, name string) *Tasklet {
	now := time.Now()
	return &Tasklet{
		TaskletID:   taskletID,
		PlanID:      planID,
		MilestoneID: milestoneID,
		Name:        name,
		Status:      TaskletStatusPending,
		BlockedBy:   []string{},
		CreatedAt:   now,
		UpdatedAt:   now,
		Labels:      make(map[string]string),
	}
}

// AddMilestone adds a milestone ID to the plan's list.
func (p *Plan) AddMilestone(milestoneID string) {
	p.Milestones = append(p.Milestones, milestoneID)
	p.UpdatedAt = time.Now()
}

// AddTasklet adds a tasklet ID to the milestone's list.
func (m *Milestone) AddTasklet(taskletID string) {
	m.Tasklets = append(m.Tasklets, taskletID)
	m.UpdatedAt = time.Now()
}

// SetStatus updates the plan status with validation.
func (p *Plan) SetStatus(status string) bool {
	if !IsValidPlanStatus(status) {
		return false
	}
	p.Status = status
	p.UpdatedAt = time.Now()
	return true
}

// SetStatus updates the milestone status with validation.
func (m *Milestone) SetStatus(status string) bool {
	if !IsValidMilestoneStatus(status) {
		return false
	}
	m.Status = status
	m.UpdatedAt = time.Now()
	return true
}

// SetStatus updates the tasklet status with validation.
func (t *Tasklet) SetStatus(status string) bool {
	if !IsValidTaskletStatus(status) {
		return false
	}
	t.Status = status
	t.UpdatedAt = time.Now()
	if status == TaskletStatusDone {
		t.CompletedAt = time.Now()
	}
	return true
}

// Assign sets the assignee for a tasklet.
func (t *Tasklet) Assign(role string) {
	t.Assignee = role
	t.UpdatedAt = time.Now()
}

// IsBlocked returns true if the tasklet has unresolved blockers.
func (t *Tasklet) IsBlocked() bool {
	return len(t.BlockedBy) > 0
}

// IsDone returns true if the tasklet is completed.
func (t *Tasklet) IsDone() bool {
	return t.Status == TaskletStatusDone
}
