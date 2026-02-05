package contextcapture

import (
	"testing"
	"time"
)

func TestNewPlan(t *testing.T) {
	p := NewPlan("plan-test", "Test Plan", "party/daemon")

	if p.PlanID != "plan-test" {
		t.Errorf("PlanID = %q, want %q", p.PlanID, "plan-test")
	}
	if p.Title != "Test Plan" {
		t.Errorf("Title = %q, want %q", p.Title, "Test Plan")
	}
	if p.Repo != "party/daemon" {
		t.Errorf("Repo = %q, want %q", p.Repo, "party/daemon")
	}
	if p.Status != PlanStatusDraft {
		t.Errorf("Status = %q, want %q", p.Status, PlanStatusDraft)
	}
	if len(p.Milestones) != 0 {
		t.Errorf("Milestones = %d, want 0", len(p.Milestones))
	}
	if p.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
}

func TestNewMilestone(t *testing.T) {
	m := NewMilestone("ms-1", "plan-test", 1, "First Milestone")

	if m.MilestoneID != "ms-1" {
		t.Errorf("MilestoneID = %q, want %q", m.MilestoneID, "ms-1")
	}
	if m.PlanID != "plan-test" {
		t.Errorf("PlanID = %q, want %q", m.PlanID, "plan-test")
	}
	if m.MilestoneNum != 1 {
		t.Errorf("MilestoneNum = %d, want 1", m.MilestoneNum)
	}
	if m.Name != "First Milestone" {
		t.Errorf("Name = %q, want %q", m.Name, "First Milestone")
	}
	if m.Status != MilestoneStatusPending {
		t.Errorf("Status = %q, want %q", m.Status, MilestoneStatusPending)
	}
}

func TestNewTasklet(t *testing.T) {
	task := NewTasklet("task-1.1", "plan-test", "ms-1", "Implement feature")

	if task.TaskletID != "task-1.1" {
		t.Errorf("TaskletID = %q, want %q", task.TaskletID, "task-1.1")
	}
	if task.PlanID != "plan-test" {
		t.Errorf("PlanID = %q, want %q", task.PlanID, "plan-test")
	}
	if task.MilestoneID != "ms-1" {
		t.Errorf("MilestoneID = %q, want %q", task.MilestoneID, "ms-1")
	}
	if task.Status != TaskletStatusPending {
		t.Errorf("Status = %q, want %q", task.Status, TaskletStatusPending)
	}
	if task.Assignee != "" {
		t.Errorf("Assignee = %q, want empty", task.Assignee)
	}
}

func TestPlanSetStatus(t *testing.T) {
	p := NewPlan("plan-test", "Test", "repo")

	tests := []struct {
		status string
		valid  bool
	}{
		{PlanStatusDraft, true},
		{PlanStatusActive, true},
		{PlanStatusCompleted, true},
		{PlanStatusAbandoned, true},
		{"invalid", false},
		{"", false},
	}

	for _, tt := range tests {
		ok := p.SetStatus(tt.status)
		if ok != tt.valid {
			t.Errorf("SetStatus(%q) = %v, want %v", tt.status, ok, tt.valid)
		}
		if ok && p.Status != tt.status {
			t.Errorf("Status = %q, want %q", p.Status, tt.status)
		}
	}
}

func TestMilestoneSetStatus(t *testing.T) {
	m := NewMilestone("ms-1", "plan", 1, "Test")

	tests := []struct {
		status string
		valid  bool
	}{
		{MilestoneStatusPending, true},
		{MilestoneStatusInProgress, true},
		{MilestoneStatusDone, true},
		{MilestoneStatusSkipped, true},
		{"invalid", false},
	}

	for _, tt := range tests {
		ok := m.SetStatus(tt.status)
		if ok != tt.valid {
			t.Errorf("SetStatus(%q) = %v, want %v", tt.status, ok, tt.valid)
		}
	}
}

func TestTaskletSetStatus(t *testing.T) {
	task := NewTasklet("task-1", "plan", "ms-1", "Test")

	tests := []struct {
		status string
		valid  bool
	}{
		{TaskletStatusPending, true},
		{TaskletStatusInProgress, true},
		{TaskletStatusDone, true},
		{TaskletStatusBlocked, true},
		{"invalid", false},
	}

	for _, tt := range tests {
		ok := task.SetStatus(tt.status)
		if ok != tt.valid {
			t.Errorf("SetStatus(%q) = %v, want %v", tt.status, ok, tt.valid)
		}
	}
}

func TestTaskletDoneTimestamp(t *testing.T) {
	task := NewTasklet("task-1", "plan", "ms-1", "Test")

	if !task.CompletedAt.IsZero() {
		t.Error("CompletedAt should be zero initially")
	}

	task.SetStatus(TaskletStatusDone)

	if task.CompletedAt.IsZero() {
		t.Error("CompletedAt should be set after marking done")
	}
	if time.Since(task.CompletedAt) > time.Second {
		t.Error("CompletedAt should be recent")
	}
}

func TestTaskletAssign(t *testing.T) {
	task := NewTasklet("task-1", "plan", "ms-1", "Test")

	task.Assign("cc")
	if task.Assignee != "cc" {
		t.Errorf("Assignee = %q, want %q", task.Assignee, "cc")
	}

	task.Assign("cx")
	if task.Assignee != "cx" {
		t.Errorf("Assignee = %q, want %q", task.Assignee, "cx")
	}
}

func TestTaskletIsBlocked(t *testing.T) {
	task := NewTasklet("task-2", "plan", "ms-1", "Test")

	if task.IsBlocked() {
		t.Error("IsBlocked should be false with no blockers")
	}

	task.BlockedBy = []string{"task-1"}
	if !task.IsBlocked() {
		t.Error("IsBlocked should be true with blockers")
	}
}

func TestTaskletIsDone(t *testing.T) {
	task := NewTasklet("task-1", "plan", "ms-1", "Test")

	if task.IsDone() {
		t.Error("IsDone should be false initially")
	}

	task.SetStatus(TaskletStatusDone)
	if !task.IsDone() {
		t.Error("IsDone should be true after marking done")
	}
}

func TestPlanAddMilestone(t *testing.T) {
	p := NewPlan("plan-test", "Test", "repo")
	originalTime := p.UpdatedAt

	time.Sleep(time.Millisecond)
	p.AddMilestone("ms-1")
	p.AddMilestone("ms-2")

	if len(p.Milestones) != 2 {
		t.Errorf("Milestones count = %d, want 2", len(p.Milestones))
	}
	if p.Milestones[0] != "ms-1" || p.Milestones[1] != "ms-2" {
		t.Errorf("Milestones = %v, want [ms-1, ms-2]", p.Milestones)
	}
	if !p.UpdatedAt.After(originalTime) {
		t.Error("UpdatedAt should be updated")
	}
}

func TestMilestoneAddTasklet(t *testing.T) {
	m := NewMilestone("ms-1", "plan", 1, "Test")

	m.AddTasklet("task-1.1")
	m.AddTasklet("task-1.2")

	if len(m.Tasklets) != 2 {
		t.Errorf("Tasklets count = %d, want 2", len(m.Tasklets))
	}
	if m.Tasklets[0] != "task-1.1" || m.Tasklets[1] != "task-1.2" {
		t.Errorf("Tasklets = %v, want [task-1.1, task-1.2]", m.Tasklets)
	}
}

func TestValidStatuses(t *testing.T) {
	planStatuses := ValidPlanStatuses()
	if len(planStatuses) != 4 {
		t.Errorf("ValidPlanStatuses count = %d, want 4", len(planStatuses))
	}

	milestoneStatuses := ValidMilestoneStatuses()
	if len(milestoneStatuses) != 4 {
		t.Errorf("ValidMilestoneStatuses count = %d, want 4", len(milestoneStatuses))
	}

	taskletStatuses := ValidTaskletStatuses()
	if len(taskletStatuses) != 4 {
		t.Errorf("ValidTaskletStatuses count = %d, want 4", len(taskletStatuses))
	}
}

func TestIsValidStatusFunctions(t *testing.T) {
	// Valid statuses
	if !IsValidPlanStatus(PlanStatusActive) {
		t.Error("PlanStatusActive should be valid")
	}
	if !IsValidMilestoneStatus(MilestoneStatusDone) {
		t.Error("MilestoneStatusDone should be valid")
	}
	if !IsValidTaskletStatus(TaskletStatusBlocked) {
		t.Error("TaskletStatusBlocked should be valid")
	}

	// Invalid statuses
	if IsValidPlanStatus("bogus") {
		t.Error("bogus should not be valid plan status")
	}
	if IsValidMilestoneStatus("") {
		t.Error("empty should not be valid milestone status")
	}
	if IsValidTaskletStatus("finished") {
		t.Error("finished should not be valid tasklet status")
	}
}
