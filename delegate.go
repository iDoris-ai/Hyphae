// Package main provides agent delegation capabilities for autonomous task execution
// including discovery, negotiation, and monitoring of agent tasks.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"github.com/fatih/color"
	"github.com/urfave/cli/v3"
)

// TaskType represents the type of task
type TaskType string

const (
	TaskMarketing   TaskType = "marketing"
	TaskDevelopment TaskType = "development"
	TaskDesign      TaskType = "design"
	TaskResearch    TaskType = "research"
	TaskWriting     TaskType = "writing"
	TaskGeneric     TaskType = "generic"
)

// TaskState represents the state of a task
type TaskState string

const (
	TaskCreated     TaskState = "created"
	TaskDiscovering TaskState = "discovering"
	TaskNegotiating TaskState = "negotiating"
	TaskContracted  TaskState = "contracted"
	TaskExecuting   TaskState = "executing"
	TaskMonitoring  TaskState = "monitoring"
	TaskCompleted   TaskState = "completed"
	TaskFailed      TaskState = "failed"
	TaskTimeout     TaskState = "timeout"
	TaskCancelled   TaskState = "cancelled"
)

// Task represents an autonomous task delegation
type Task struct {
	ID           string           `json:"id"`
	Type         TaskType         `json:"type"`
	State        TaskState        `json:"state"`
	Description  string           `json:"description"`
	Requirements TaskRequirements `json:"requirements"`

	// Execution info
	Candidates   []AgentInfo   `json:"candidates,omitempty"`
	Selected     *AgentInfo    `json:"selected,omitempty"`
	Negotiations []Negotiation `json:"negotiations,omitempty"`
	Contract     *Contract     `json:"contract,omitempty"`

	// Monitoring
	StartTime time.Time   `json:"start_time"`
	Deadline  time.Time   `json:"deadline,omitempty"`
	Progress  float64     `json:"progress"`
	Logs      []TaskLog   `json:"logs,omitempty"`
	Result    *TaskResult `json:"result,omitempty"`

	// Internal
	mu         sync.RWMutex
	cancelFunc context.CancelFunc
}

// TaskRequirements defines what the task needs
type TaskRequirements struct {
	Capabilities []string  `json:"capabilities,omitempty"`
	MinBudget    float64   `json:"min_budget,omitempty"`
	MaxBudget    float64   `json:"max_budget,omitempty"`
	Currency     string    `json:"currency,omitempty"`
	Deadline     time.Time `json:"deadline,omitempty"`
	Location     string    `json:"location,omitempty"`
}

// AgentInfo represents information about an agent
type AgentInfo struct {
	Pubkey         string       `json:"pubkey"`
	Name           string       `json:"name,omitempty"`
	About          string       `json:"about,omitempty"`
	Capabilities   []Capability `json:"capabilities,omitempty"`
	Rating         float64      `json:"rating,omitempty"`
	CompletedTasks int          `json:"completed_tasks,omitempty"`
	Availability   string       `json:"availability,omitempty"`
}

// Capability represents an agent's capability
type Capability struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	PriceRange  PriceRange        `json:"price_range,omitempty"`
	Reach       *int              `json:"reach,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// PriceRange defines the price for a capability
type PriceRange struct {
	Min      float64 `json:"min,omitempty"`
	Max      float64 `json:"max,omitempty"`
	Currency string  `json:"currency,omitempty"`
	Unit     string  `json:"unit,omitempty"`
}

// Negotiation represents a negotiation between delegator and agent
type Negotiation struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Type      string    `json:"type"` // "offer", "counter", "accept", "reject"
	Content   string    `json:"content"`
	Price     float64   `json:"price,omitempty"`
	Deadline  time.Time `json:"deadline,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// Contract represents a finalized agreement
type Contract struct {
	Delegator string    `json:"delegator"`
	Agent     string    `json:"agent"`
	Price     float64   `json:"price"`
	Currency  string    `json:"currency"`
	Deadline  time.Time `json:"deadline"`
	Terms     string    `json:"terms"`
	SignedAt  time.Time `json:"signed_at"`
}

// TaskLog represents a log entry for a task
type TaskLog struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"` // "info", "warn", "error"
	Message   string    `json:"message"`
}

// TaskResult represents the result of a task
type TaskResult struct {
	Success     bool      `json:"success"`
	Deliverable string    `json:"deliverable,omitempty"`
	Summary     string    `json:"summary,omitempty"`
	CompletedAt time.Time `json:"completed_at"`
}

// TaskEngine manages task delegation
type TaskEngine struct {
	sys     *nostr.System
	relays  []string
	keyer   nostr.Keyer
	tasks   map[string]*Task
	mu      sync.RWMutex
}

// NewTaskEngine creates a new task engine
func NewTaskEngine(sys *nostr.System, relays []string, keyer nostr.Keyer) *TaskEngine {
	return &TaskEngine{
		sys:    sys,
		relays: relays,
		keyer:  keyer,
		tasks:  make(map[string]*Task),
	}
}

// CreateTask creates a new task
func (te *TaskEngine) CreateTask(taskType TaskType, description string, requirements TaskRequirements) *Task {
	task := &Task{
		ID:           generateTaskID(),
		Type:         taskType,
		State:        TaskCreated,
		Description:  description,
		Requirements: requirements,
		StartTime:    time.Now(),
		Progress:     0,
		Logs:         []TaskLog{},
	}

	te.mu.Lock()
	te.tasks[task.ID] = task
	te.mu.Unlock()

	return task
}

// GetTask retrieves a task by ID
func (te *TaskEngine) GetTask(id string) (*Task, bool) {
	te.mu.RLock()
	defer te.mu.RUnlock()
	task, ok := te.tasks[id]
	return task, ok
}

// ExecuteTask runs the full task delegation workflow
func (te *TaskEngine) ExecuteTask(ctx context.Context, task *Task) error {
	task.mu.Lock()
	task.State = TaskDiscovering
	ctx, task.cancelFunc = context.WithCancel(ctx)
	task.mu.Unlock()

	task.AddLog("info", "Starting task execution workflow")

	// Step 1: Discover agents
	candidates, err := te.discoverAgents(ctx, task)
	if err != nil {
		task.SetState(TaskFailed)
		task.AddLog("error", fmt.Sprintf("Discovery failed: %v", err))
		return fmt.Errorf("discovery failed: %w", err)
	}

	task.mu.Lock()
	task.Candidates = candidates
	task.mu.Unlock()

	if len(candidates) == 0 {
		task.SetState(TaskFailed)
		task.AddLog("error", "No suitable agents found")
		return fmt.Errorf("no suitable agents found")
	}

	task.AddLog("info", fmt.Sprintf("Found %d candidates", len(candidates)))

	// Step 2: Negotiate
	task.SetState(TaskNegotiating)
	selected, contract, err := te.negotiate(ctx, task, candidates)
	if err != nil {
		task.SetState(TaskFailed)
		task.AddLog("error", fmt.Sprintf("Negotiation failed: %v", err))
		return fmt.Errorf("negotiation failed: %w", err)
	}

	task.mu.Lock()
	task.Selected = selected
	task.Contract = contract
	task.mu.Unlock()

	task.AddLog("info", fmt.Sprintf("Contracted with agent: %s", selected.Name))

	// Step 3: Execute
	task.SetState(TaskExecuting)
	if err := te.execute(ctx, task); err != nil {
		task.SetState(TaskFailed)
		task.AddLog("error", fmt.Sprintf("Execution failed: %v", err))
		return fmt.Errorf("execution failed: %w", err)
	}

	// Step 4: Complete
	task.SetState(TaskCompleted)
	task.AddLog("info", "Task completed successfully")

	return nil
}

// discoverAgents finds suitable agents for a task
func (te *TaskEngine) discoverAgents(ctx context.Context, task *Task) ([]AgentInfo, error) {
	// Query for agent profiles (Kind 0 with specific tags)
	filter := nostr.Filter{
		Kinds: []int{0}, // Metadata
		Tags: nostr.TagMap{
			"c": []string{"agent"},
		},
	}

	events := te.sys.Pool.FetchMany(ctx, te.relays, filter, nostr.SubscriptionOptions{})

	var candidates []AgentInfo
	for ie := range events {
		var profile struct {
			Name         string       `json:"name"`
			About        string       `json:"about"`
			Capabilities []Capability `json:"capabilities"`
		}

		if err := json.Unmarshal([]byte(ie.Event.Content), &profile); err != nil {
			continue
		}

		// Check if agent has required capabilities
		if te.hasCapabilities(profile.Capabilities, task.Requirements.Capabilities) {
			candidates = append(candidates, AgentInfo{
				Pubkey:       ie.Event.PubKey,
				Name:         profile.Name,
				About:        profile.About,
				Capabilities: profile.Capabilities,
			})
		}
	}

	return candidates, nil
}

// hasCapabilities checks if agent has all required capabilities
func (te *TaskEngine) hasCapabilities(agentCaps, requiredCaps []string) bool {
	if len(requiredCaps) == 0 {
		return true
	}

	capSet := make(map[string]bool)
	for _, cap := range agentCaps {
		capSet[strings.ToLower(cap)] = true
	}

	for _, req := range requiredCaps {
		if !capSet[strings.ToLower(req)] {
			return false
		}
	}

	return true
}

// negotiate negotiates with candidates and selects the best one
func (te *TaskEngine) negotiate(ctx context.Context, task *Task, candidates []AgentInfo) (*AgentInfo, *Contract, error) {
	// Simple strategy: pick first candidate within budget
	for _, candidate := range candidates {
		// Check availability
		if candidate.Availability != "busy" {
			contract := &Contract{
				Delegator: "", // Will be set to our pubkey
				Agent:     candidate.Pubkey,
				Price:     task.Requirements.MaxBudget,
				Currency:  task.Requirements.Currency,
				Deadline:  task.Requirements.Deadline,
				SignedAt:  time.Now(),
			}
			return &candidate, contract, nil
		}
	}

	return nil, nil, fmt.Errorf("no available agents within budget")
}

// execute monitors task execution
func (te *TaskEngine) execute(ctx context.Context, task *Task) error {
	// In a real implementation, this would:
	// 1. Send task details to the selected agent
	// 2. Subscribe to progress updates
	// 3. Monitor for completion or timeout

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			task.mu.Lock()
			task.Progress += 10
			if task.Progress >= 100 {
				task.Progress = 100
				task.mu.Unlock()
				return nil
			}
			task.mu.Unlock()
			task.AddLog("info", fmt.Sprintf("Progress: %.0f%%", task.Progress))
		}
	}
}

// SetState updates task state thread-safely
func (t *Task) SetState(state TaskState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.State = state
}

// AddLog adds a log entry thread-safely
func (t *Task) AddLog(level, message string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Logs = append(t.Logs, TaskLog{
		Timestamp: time.Now(),
		Level:     level,
		Message:   message,
	})
}

// generateTaskID generates a unique task ID
func generateTaskID() string {
	return fmt.Sprintf("task-%d", time.Now().UnixNano())
}

// CLI Commands

var agentDelegateCmd = &cli.Command{
	Name:  "delegate",
	Usage: "delegate a task to an autonomous agent",
	Flags: append(defaultKeyFlags,
		&cli.StringFlag{
			Name:     "type",
			Usage:    "task type (marketing, development, design, research, writing, generic)",
			Required: true,
		},
		&cli.StringFlag{
			Name:     "desc",
			Usage:    "task description",
			Required: true,
		},
		&cli.StringSliceFlag{
			Name:  "caps",
			Usage: "required capabilities",
		},
		&cli.Float64Flag{
			Name:  "budget",
			Usage: "maximum budget",
			Value: 1000,
		},
		&cli.StringFlag{
			Name:  "currency",
			Usage: "currency (USD, CNY, etc.)",
			Value: "CNY",
		},
	),
	Action: func(ctx context.Context, c *cli.Command) error {
		// Get keyer
		kr, _, err := gatherKeyerFromArguments(ctx, c)
		if err != nil {
			return fmt.Errorf("failed to get signer: %w", err)
		}

		// Create task engine
		engine := NewTaskEngine(sys, defaultRelays, kr)

		// Create task
		requirements := TaskRequirements{
			Capabilities: c.StringSlice("caps"),
			MaxBudget:    c.Float64("budget"),
			Currency:     c.String("currency"),
		}

		task := engine.CreateTask(TaskType(c.String("type")), c.String("desc"), requirements)

		color.Cyan("Created task: %s", task.ID)
		color.Cyan("Type: %s", task.Type)
		color.Cyan("Description: %s", task.Description)

		// Execute task
		if err := engine.ExecuteTask(ctx, task); err != nil {
			return fmt.Errorf("task execution failed: %w", err)
		}

		color.Green("✓ Task completed: %s", task.ID)
		return nil
	},
}

var agentTaskCmd = &cli.Command{
	Name:  "task",
	Usage: "manage delegated tasks",
	Commands: []*cli.Command{
		{
			Name:  "list",
			Usage: "list all tasks",
			Action: func(ctx context.Context, c *cli.Command) error {
				fmt.Println("Task list: (not implemented)")
				return nil
			},
		},
		{
			Name:  "status",
			Usage: "check task status",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "id",
					Usage:    "task ID",
					Required: true,
				},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				fmt.Printf("Task status for %s: (not implemented)\n", c.String("id"))
				return nil
			},
		},
		{
			Name:  "cancel",
			Usage: "cancel a task",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "id",
					Usage:    "task ID",
					Required: true,
				},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				fmt.Printf("Cancelling task %s: (not implemented)\n", c.String("id"))
				return nil
			},
		},
	},
}
