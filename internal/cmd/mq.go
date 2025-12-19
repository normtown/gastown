package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/refinery"
	"github.com/steveyegge/gastown/internal/style"
)

// MQ command flags
var (
	// Retry flags
	mqRetryNow bool

	// Reject flags
	mqRejectReason string
	mqRejectNotify bool

	// List command flags
	mqListReady  bool
	mqListStatus string
	mqListWorker string
	mqListEpic   string
	mqListJSON   bool
)

var mqCmd = &cobra.Command{
	Use:   "mq",
	Short: "Merge queue operations",
	Long: `Manage the merge queue for a rig.

The merge queue tracks work branches from polecats waiting to be merged.
Use these commands to view, submit, retry, and manage merge requests.`,
}

var mqRetryCmd = &cobra.Command{
	Use:   "retry <rig> <mr-id>",
	Short: "Retry a failed merge request",
	Long: `Retry a failed merge request.

Resets a failed MR so it can be processed again by the refinery.
The MR must be in a failed state (open with an error).

Examples:
  gt mq retry gastown gt-mr-abc123
  gt mq retry gastown gt-mr-abc123 --now`,
	Args: cobra.ExactArgs(2),
	RunE: runMQRetry,
}

var mqListCmd = &cobra.Command{
	Use:   "list <rig>",
	Short: "Show the merge queue",
	Long: `Show the merge queue for a rig.

Lists all pending merge requests waiting to be processed.

Output format:
  ID          STATUS       PRIORITY  BRANCH                    WORKER  AGE
  gt-mr-001   ready        P0        polecat/Nux/gt-xyz        Nux     5m
  gt-mr-002   in_progress  P1        polecat/Toast/gt-abc      Toast   12m
  gt-mr-003   blocked      P1        polecat/Capable/gt-def    Capable 8m
              (waiting on gt-mr-001)

Examples:
  gt mq list gastown
  gt mq list gastown --ready
  gt mq list gastown --status=open
  gt mq list gastown --worker=Nux`,
	Args: cobra.ExactArgs(1),
	RunE: runMQList,
}

var mqRejectCmd = &cobra.Command{
	Use:   "reject <rig> <mr-id-or-branch>",
	Short: "Reject a merge request",
	Long: `Manually reject a merge request.

This closes the MR with a 'rejected' status without merging.
The source issue is NOT closed (work is not done).

Examples:
  gt mq reject gastown polecat/Nux/gt-xyz --reason "Does not meet requirements"
  gt mq reject gastown mr-Nux-12345 --reason "Superseded by other work" --notify`,
	Args: cobra.ExactArgs(2),
	RunE: runMQReject,
}

func init() {
	// Retry flags
	mqRetryCmd.Flags().BoolVar(&mqRetryNow, "now", false, "Immediately process instead of waiting for refinery loop")

	// List flags
	mqListCmd.Flags().BoolVar(&mqListReady, "ready", false, "Show only ready-to-merge (no blockers)")
	mqListCmd.Flags().StringVar(&mqListStatus, "status", "", "Filter by status (open, in_progress, closed)")
	mqListCmd.Flags().StringVar(&mqListWorker, "worker", "", "Filter by worker name")
	mqListCmd.Flags().StringVar(&mqListEpic, "epic", "", "Show MRs targeting integration/<epic>")
	mqListCmd.Flags().BoolVar(&mqListJSON, "json", false, "Output as JSON")

	// Reject flags
	mqRejectCmd.Flags().StringVarP(&mqRejectReason, "reason", "r", "", "Reason for rejection (required)")
	mqRejectCmd.Flags().BoolVar(&mqRejectNotify, "notify", false, "Send mail notification to worker")
	_ = mqRejectCmd.MarkFlagRequired("reason")

	// Add subcommands
	mqCmd.AddCommand(mqRetryCmd)
	mqCmd.AddCommand(mqListCmd)
	mqCmd.AddCommand(mqRejectCmd)

	rootCmd.AddCommand(mqCmd)
}

func runMQRetry(cmd *cobra.Command, args []string) error {
	rigName := args[0]
	mrID := args[1]

	mgr, _, err := getRefineryManager(rigName)
	if err != nil {
		return err
	}

	// Get the MR first to show info
	mr, err := mgr.GetMR(mrID)
	if err != nil {
		if err == refinery.ErrMRNotFound {
			return fmt.Errorf("merge request '%s' not found in rig '%s'", mrID, rigName)
		}
		return fmt.Errorf("getting merge request: %w", err)
	}

	// Show what we're retrying
	fmt.Printf("Retrying merge request: %s\n", mrID)
	fmt.Printf("  Branch: %s\n", mr.Branch)
	fmt.Printf("  Worker: %s\n", mr.Worker)
	if mr.Error != "" {
		fmt.Printf("  Previous error: %s\n", style.Dim.Render(mr.Error))
	}

	// Perform the retry
	if err := mgr.Retry(mrID, mqRetryNow); err != nil {
		if err == refinery.ErrMRNotFailed {
			return fmt.Errorf("merge request '%s' has not failed (status: %s)", mrID, mr.Status)
		}
		return fmt.Errorf("retrying merge request: %w", err)
	}

	if mqRetryNow {
		fmt.Printf("%s Merge request processed\n", style.Bold.Render("✓"))
	} else {
		fmt.Printf("%s Merge request queued for retry\n", style.Bold.Render("✓"))
		fmt.Printf("  %s\n", style.Dim.Render("Will be processed on next refinery cycle"))
	}

	return nil
}

func runMQList(cmd *cobra.Command, args []string) error {
	rigName := args[0]

	_, r, err := getRefineryManager(rigName)
	if err != nil {
		return err
	}

	// Create beads wrapper for the rig
	b := beads.New(r.Path)

	// Build list options - query for merge-request type
	opts := beads.ListOptions{
		Type: "merge-request",
	}

	// Apply status filter if specified
	if mqListStatus != "" {
		opts.Status = mqListStatus
	} else if !mqListReady {
		// Default to open if not showing ready
		opts.Status = "open"
	}

	var issues []*beads.Issue

	if mqListReady {
		// Use ready query which filters by no blockers
		allReady, err := b.Ready()
		if err != nil {
			return fmt.Errorf("querying ready MRs: %w", err)
		}
		// Filter to only merge-request type
		for _, issue := range allReady {
			if issue.Type == "merge-request" {
				issues = append(issues, issue)
			}
		}
	} else {
		issues, err = b.List(opts)
		if err != nil {
			return fmt.Errorf("querying merge queue: %w", err)
		}
	}

	// Apply additional filters
	var filtered []*beads.Issue
	for _, issue := range issues {
		// Parse MR fields
		fields := beads.ParseMRFields(issue)

		// Filter by worker
		if mqListWorker != "" {
			worker := ""
			if fields != nil {
				worker = fields.Worker
			}
			if !strings.EqualFold(worker, mqListWorker) {
				continue
			}
		}

		// Filter by epic (target branch)
		if mqListEpic != "" {
			target := ""
			if fields != nil {
				target = fields.Target
			}
			expectedTarget := "integration/" + mqListEpic
			if target != expectedTarget {
				continue
			}
		}

		filtered = append(filtered, issue)
	}

	// JSON output
	if mqListJSON {
		return outputJSON(filtered)
	}

	// Human-readable output
	fmt.Printf("%s Merge queue for '%s':\n\n", style.Bold.Render("📋"), rigName)

	if len(filtered) == 0 {
		fmt.Printf("  %s\n", style.Dim.Render("(empty)"))
		return nil
	}

	// Print header
	fmt.Printf("  %-12s %-12s %-8s %-30s %-10s %s\n",
		"ID", "STATUS", "PRIORITY", "BRANCH", "WORKER", "AGE")
	fmt.Printf("  %s\n", strings.Repeat("-", 90))

	// Print each MR
	for _, issue := range filtered {
		fields := beads.ParseMRFields(issue)

		// Determine display status
		displayStatus := issue.Status
		if issue.Status == "open" {
			if len(issue.BlockedBy) > 0 || issue.BlockedByCount > 0 {
				displayStatus = "blocked"
			} else {
				displayStatus = "ready"
			}
		}

		// Format status with styling
		styledStatus := displayStatus
		switch displayStatus {
		case "ready":
			styledStatus = style.Bold.Render("ready")
		case "in_progress":
			styledStatus = style.Bold.Render("in_progress")
		case "blocked":
			styledStatus = style.Dim.Render("blocked")
		case "closed":
			styledStatus = style.Dim.Render("closed")
		}

		// Get MR fields
		branch := ""
		worker := ""
		if fields != nil {
			branch = fields.Branch
			worker = fields.Worker
		}

		// Truncate branch if too long
		if len(branch) > 30 {
			branch = branch[:27] + "..."
		}

		// Format priority
		priority := fmt.Sprintf("P%d", issue.Priority)

		// Calculate age
		age := formatMRAge(issue.CreatedAt)

		// Truncate ID if needed
		displayID := issue.ID
		if len(displayID) > 12 {
			displayID = displayID[:12]
		}

		fmt.Printf("  %-12s %-12s %-8s %-30s %-10s %s\n",
			displayID, styledStatus, priority, branch, worker, style.Dim.Render(age))

		// Show blocking info if blocked
		if displayStatus == "blocked" && len(issue.BlockedBy) > 0 {
			fmt.Printf("  %s\n", style.Dim.Render(fmt.Sprintf("             (waiting on %s)", issue.BlockedBy[0])))
		}
	}

	return nil
}

// formatMRAge formats the age of an MR from its created_at timestamp.
func formatMRAge(createdAt string) string {
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		// Try other formats
		t, err = time.Parse("2006-01-02T15:04:05Z", createdAt)
		if err != nil {
			return "?"
		}
	}

	d := time.Since(t)

	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// outputJSON outputs data as JSON.
func outputJSON(data interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

func runMQReject(cmd *cobra.Command, args []string) error {
	rigName := args[0]
	mrIDOrBranch := args[1]

	mgr, _, err := getRefineryManager(rigName)
	if err != nil {
		return err
	}

	result, err := mgr.RejectMR(mrIDOrBranch, mqRejectReason, mqRejectNotify)
	if err != nil {
		return fmt.Errorf("rejecting MR: %w", err)
	}

	fmt.Printf("%s Rejected: %s\n", style.Bold.Render("✗"), result.Branch)
	fmt.Printf("  Worker: %s\n", result.Worker)
	fmt.Printf("  Reason: %s\n", mqRejectReason)

	if result.IssueID != "" {
		fmt.Printf("  Issue:  %s %s\n", result.IssueID, style.Dim.Render("(not closed - work not done)"))
	}

	if mqRejectNotify {
		fmt.Printf("  %s\n", style.Dim.Render("Worker notified via mail"))
	}

	return nil
}
