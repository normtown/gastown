// Package adapter provides source control adapters for Gas Town.
package adapter

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitAdapter implements SourceControlAdapter for Git repositories.
// This is the default adapter preserving existing Gas Town behavior.
type GitAdapter struct {
	// rigPath is the rig container directory
	rigPath string

	// bareRepoPath is the path to the shared bare repository
	bareRepoPath string

	// workerPath is the path to the current worker (for BuildRoot)
	workerPath string

	// config holds the rig configuration
	config RigConfig
}

func init() {
	Register("git", func() SourceControlAdapter {
		return &GitAdapter{}
	})
}

// RigInit initializes a git-based rig at the given path.
// It creates a shared bare repository for worktree-based worker management.
func (g *GitAdapter) RigInit(path string, config RigConfig) error {
	g.rigPath = path
	g.config = config

	// Get git URL from config extra
	gitURL, ok := config.Extra["git_url"].(string)
	if !ok || gitURL == "" {
		return fmt.Errorf("git adapter requires git_url in config")
	}

	// Create the rig directory
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("creating rig directory: %w", err)
	}

	// Create shared bare repo for worktrees
	g.bareRepoPath = filepath.Join(path, ".repo.git")

	// Check for optional local reference repo
	localRepo, _ := config.Extra["local_repo"].(string)

	// Clone bare repository
	var cloneArgs []string
	if localRepo != "" {
		cloneArgs = []string{"clone", "--bare", "--reference", localRepo, gitURL, g.bareRepoPath}
	} else {
		cloneArgs = []string{"clone", "--bare", gitURL, g.bareRepoPath}
	}

	cmd := exec.Command("git", cloneArgs...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cloning bare repo: %w: %s", err, output)
	}

	return nil
}

// WorkerCreate creates a new worker as a git worktree.
func (g *GitAdapter) WorkerCreate(workerPath string) error {
	if g.bareRepoPath == "" {
		// Infer bare repo path from worker path
		// Worker is typically at <rig>/polecats/<name> or <rig>/crew/<name>
		g.bareRepoPath = filepath.Join(filepath.Dir(filepath.Dir(workerPath)), ".repo.git")
	}

	// Determine branch name from worker path
	workerName := filepath.Base(workerPath)
	branchName := fmt.Sprintf("polecat/%s", workerName)

	// Get default branch from bare repo
	defaultBranch := g.getDefaultBranch()

	// Create the worktree
	// git worktree add -b <branch> <path> <start-point>
	cmd := exec.Command("git", "worktree", "add", "-b", branchName, workerPath, defaultBranch)
	cmd.Dir = g.bareRepoPath
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("creating worktree: %w: %s", err, output)
	}

	g.workerPath = workerPath
	return nil
}

// WorkerActivate makes a worker the active context.
// For git with worktrees in parallel mode, this is a no-op.
func (g *GitAdapter) WorkerActivate(worker string) error {
	// No-op for parallel worktree mode
	return nil
}

// WorkerDeactivate deactivates a worker.
// For git with worktrees in parallel mode, this is a no-op.
func (g *GitAdapter) WorkerDeactivate(worker string) error {
	// No-op for parallel worktree mode
	return nil
}

// BuildRoot returns the root directory for build operations.
// For git, this is simply the worker path.
func (g *GitAdapter) BuildRoot() string {
	return g.workerPath
}

// Submit pushes the worker's changes to the remote.
func (g *GitAdapter) Submit(worker string) error {
	workerPath := worker
	if !filepath.IsAbs(worker) {
		// Assume it's a worker name, construct path
		workerPath = filepath.Join(g.rigPath, "polecats", worker)
	}

	// Get current branch
	branchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	branchCmd.Dir = workerPath
	branchOutput, err := branchCmd.Output()
	if err != nil {
		return fmt.Errorf("getting current branch: %w", err)
	}
	branch := strings.TrimSpace(string(branchOutput))

	// Push to origin
	pushCmd := exec.Command("git", "push", "-u", "origin", branch)
	pushCmd.Dir = workerPath
	if output, err := pushCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pushing to remote: %w: %s", err, output)
	}

	return nil
}

// Sync pulls the latest changes from the remote.
func (g *GitAdapter) Sync() error {
	if g.workerPath == "" {
		return fmt.Errorf("no active worker")
	}

	// Fetch from origin
	fetchCmd := exec.Command("git", "fetch", "origin")
	fetchCmd.Dir = g.workerPath
	if output, err := fetchCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("fetching from origin: %w: %s", err, output)
	}

	// Pull with rebase
	pullCmd := exec.Command("git", "pull", "--rebase")
	pullCmd.Dir = g.workerPath
	if output, err := pullCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pulling with rebase: %w: %s", err, output)
	}

	return nil
}

// getDefaultBranch returns the default branch of the bare repository.
func (g *GitAdapter) getDefaultBranch() string {
	// Try to get from remote HEAD
	cmd := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD")
	cmd.Dir = g.bareRepoPath
	output, err := cmd.Output()
	if err == nil {
		ref := strings.TrimSpace(string(output))
		// refs/remotes/origin/main -> main
		if parts := strings.Split(ref, "/"); len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}

	// Fallback: check for common branch names
	for _, branch := range []string{"main", "master"} {
		checkCmd := exec.Command("git", "rev-parse", "--verify", branch)
		checkCmd.Dir = g.bareRepoPath
		if err := checkCmd.Run(); err == nil {
			return branch
		}
	}

	return "main"
}

// SetWorkerPath sets the worker path for operations that need it.
// This is useful when the adapter is retrieved from the registry
// and needs to be configured for a specific worker.
func (g *GitAdapter) SetWorkerPath(path string) {
	g.workerPath = path
}

// SetRigPath sets the rig path for operations that need it.
func (g *GitAdapter) SetRigPath(path string) {
	g.rigPath = path
	g.bareRepoPath = filepath.Join(path, ".repo.git")
}
