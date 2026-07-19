package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Persistent workspace flags (bound in root.go). Identity lane only.
var (
	flagWorkspace        string // --workspace <slug|id>: per-command target
	flagConfirmWorkspace string // --confirm-workspace <slug|id>: non-TTY write confirm
)

// wsEntry mirrors one row of GET /workspaces.
type wsEntry struct {
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	CanWrite bool   `json:"can_write"`
	IsActive bool   `json:"is_active"`
}

// fetchWorkspaces calls the exempt discovery endpoint (never carries the
// X-Blenau-Workspace header — SPEC 1 §1.2). Works in both lanes.
func fetchWorkspaces() ([]wsEntry, error) {
	raw, status, err := apiCall("GET", "/workspaces", nil)
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, fmt.Errorf("GET /workspaces: %s", strings.TrimSpace(string(raw)))
	}
	var resp struct {
		Workspaces []wsEntry `json:"workspaces"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse /workspaces: %w", err)
	}
	return resp.Workspaces, nil
}

// resolveWorkspaceRef maps a slug-or-id to a full ref, validating membership
// client-side (a 403 would otherwise only surface on the write). Empty → nil.
func resolveWorkspaceRef(raw string) (*WorkspaceRef, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	entries, err := fetchWorkspaces()
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.ID == raw || e.Slug == raw {
			return &WorkspaceRef{ID: e.ID, Slug: e.Slug, Name: e.Name}, nil
		}
	}
	return nil, fmt.Errorf("not a member of workspace %q (run `blenau workspaces` to list)", raw)
}

// currentAnchor is the "expected" workspace: BLENAU_WORKSPACE (operator pin,
// resolved) if set, else the config's active workspace. May be nil.
func currentAnchor() (*WorkspaceRef, error) {
	if pin := strings.TrimSpace(os.Getenv("BLENAU_WORKSPACE")); pin != "" {
		return resolveWorkspaceRef(pin)
	}
	cfg, _ := LoadConfig()
	if cfg != nil && cfg.ActiveWorkspace != nil && cfg.ActiveWorkspace.ID != "" {
		return cfg.ActiveWorkspace, nil
	}
	return nil, nil
}

// resolveTargetAndAnchor returns the write/read target and the anchor.
//   - target: --workspace override if present, else the anchor.
//   - Common path (no --workspace, no BLENAU_WORKSPACE, active set): NO API call.
func resolveTargetAndAnchor() (target, anchor *WorkspaceRef, err error) {
	anchor, err = currentAnchor()
	if err != nil {
		return nil, nil, err
	}
	if flagWorkspace != "" {
		target, err = resolveWorkspaceRef(flagWorkspace)
		if err != nil {
			return nil, nil, err
		}
		return target, anchor, nil
	}
	return anchor, anchor, nil
}

func anchorName(a *WorkspaceRef) string {
	if a == nil {
		return "none"
	}
	return a.Name
}

// confirmWrite enforces the target≠anchor write guard (SPEC 3 §7). Agent-first:
//   - always announces the target on stderr;
//   - non-interactive (stdin not a TTY, the primary case): FAIL-CLOSED unless
//     --confirm-workspace names this target (never auto-confirm from piped stdin);
//   - interactive: a [y/N] prompt read from the terminal.
func confirmWrite(target, anchor *WorkspaceRef) error {
	fmt.Fprintf(os.Stderr, "→ writing to %s (active: %s)\n", target.Name, anchorName(anchor))
	if flagConfirmWorkspace != "" {
		if flagConfirmWorkspace == target.Slug || flagConfirmWorkspace == target.ID {
			return nil
		}
		return fmt.Errorf("--confirm-workspace %q does not match the write target %q", flagConfirmWorkspace, target.Slug)
	}
	if !isTerminal(os.Stdin) {
		return fmt.Errorf(
			"refusing to write to %q — it is not the active workspace (%s). "+
				"Re-run with --confirm-workspace %s, or switch with `blenau use %s`",
			target.Name, anchorName(anchor), target.Slug, target.Slug)
	}
	fmt.Fprintf(os.Stderr, "Write to %q instead of the active workspace? [y/N] ", target.Name)
	var resp string
	fmt.Fscanln(os.Stdin, &resp)
	switch strings.ToLower(strings.TrimSpace(resp)) {
	case "y", "yes":
		return nil
	default:
		return fmt.Errorf("aborted")
	}
}

// verifyWorkspaceEcho checks a write landed in the intended workspace (P0 —
// closes version-skew/rollback, SPEC 1 §1.3/§7.4). Only called when we set an
// explicit target and the write succeeded.
func verifyWorkspaceEcho(raw []byte, targetID string) error {
	var r struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("could not verify write workspace (unparseable response)")
	}
	if r.Workspace.ID == "" {
		return fmt.Errorf("write response did not echo a workspace — the API may not support workspace selection; refusing to assume success")
	}
	if r.Workspace.ID != targetID {
		return fmt.Errorf("write landed in the wrong workspace (echo %s, expected %s) — aborting", r.Workspace.ID, targetID)
	}
	return nil
}
