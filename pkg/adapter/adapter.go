// Package adapter defines the interface for source control adapters in Gas Town.
// Different version control systems (git, brazil, etc.) implement this interface
// to provide unified worker management regardless of underlying VCS.
package adapter

import (
	"fmt"
	"sync"
)

// WorkerMode specifies how workers are created and managed.
type WorkerMode string

const (
	// WorkerModeWorktree creates workers as git worktrees (default for git adapter).
	WorkerModeWorktree WorkerMode = "worktree"

	// WorkerModeBranch creates workers as branches in a single clone.
	WorkerModeBranch WorkerMode = "branch"

	// WorkerModeWorkspace creates workers as Brazil workspaces.
	WorkerModeWorkspace WorkerMode = "workspace"
)

// RigConfig holds configuration for a rig's source control setup.
type RigConfig struct {
	// Adapter specifies which source control adapter to use (e.g., "git", "brazil").
	Adapter string `json:"adapter" toml:"adapter"`

	// WorkerMode specifies how workers should be created.
	// For git: "worktree" (default) or "branch"
	// For brazil: "workspace" (default)
	WorkerMode WorkerMode `json:"worker_mode,omitempty" toml:"worker_mode,omitempty"`

	// BuildRoot is the root directory for builds (used by brazil adapter).
	// If empty, defaults are used based on the adapter.
	BuildRoot string `json:"build_root,omitempty" toml:"build_root,omitempty"`

	// Extra holds adapter-specific configuration.
	Extra map[string]any `json:"extra,omitempty" toml:"extra,omitempty"`
}

// SourceControlAdapter defines the interface for version control system integration.
// Each adapter (git, brazil, etc.) implements this interface to provide
// consistent worker management across different VCS backends.
type SourceControlAdapter interface {
	// RigInit initializes a rig at the given path with the provided configuration.
	// This sets up any necessary VCS infrastructure (e.g., git init, brazil workspace setup).
	RigInit(path string, config RigConfig) error

	// WorkerCreate creates a new worker at the given path.
	// For git: creates a worktree or branch depending on WorkerMode.
	// For brazil: creates a new workspace.
	WorkerCreate(workerPath string) error

	// WorkerActivate makes a worker the active working context.
	// This may involve checking out a branch, switching workspaces, etc.
	WorkerActivate(worker string) error

	// WorkerDeactivate deactivates a worker, cleaning up any temporary state.
	// The worker remains available for future activation.
	WorkerDeactivate(worker string) error

	// BuildRoot returns the root directory for build operations.
	// This is where compiled artifacts, caches, etc. are stored.
	BuildRoot() string

	// Submit submits the worker's changes for review/integration.
	// For git: creates a PR or pushes to remote.
	// For brazil: submits a code review.
	Submit(worker string) error

	// Sync synchronizes the worker with upstream changes.
	// For git: fetch + rebase/merge.
	// For brazil: brazil ws sync.
	Sync() error
}

// AdapterFactory is a function that creates a new adapter instance.
type AdapterFactory func() SourceControlAdapter

var (
	adaptersMu sync.RWMutex
	adapters   = make(map[string]AdapterFactory)
)

// Register registers an adapter factory under the given name.
// This is typically called from an adapter package's init() function.
func Register(name string, factory AdapterFactory) {
	adaptersMu.Lock()
	defer adaptersMu.Unlock()
	adapters[name] = factory
}

// Get returns an adapter instance for the given name.
// Returns an error if no adapter is registered with that name.
func Get(name string) (SourceControlAdapter, error) {
	adaptersMu.RLock()
	factory, ok := adapters[name]
	adaptersMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown adapter: %q", name)
	}
	return factory(), nil
}

// List returns the names of all registered adapters.
func List() []string {
	adaptersMu.RLock()
	defer adaptersMu.RUnlock()

	names := make([]string, 0, len(adapters))
	for name := range adapters {
		names = append(names, name)
	}
	return names
}

// IsRegistered returns true if an adapter with the given name is registered.
func IsRegistered(name string) bool {
	adaptersMu.RLock()
	defer adaptersMu.RUnlock()
	_, ok := adapters[name]
	return ok
}
