// Package gh wraps the `gh` CLI for PR operations.
package gh

import (
	"context"
	"os/exec"
)

// Available reports whether the gh binary is on PATH.
func Available() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// CreatePRWeb opens `gh pr create --web` in the given directory. Returns
// stdout on success. In practice --web launches the user's browser and exits
// quickly, which suits a TUI that can't host interactive gh prompts.
func CreatePRWeb(ctx context.Context, dir, title string) ([]byte, error) {
	args := []string{"pr", "create", "--web"}
	if title != "" {
		args = append(args, "--title", title)
	}
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}
