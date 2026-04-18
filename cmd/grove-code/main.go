// grove-code: TUI agent/worktree/diff manager.
package main

import (
	"context"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/dgroos/grove-code/internal/agent"
	"github.com/dgroos/grove-code/internal/app"
	"github.com/dgroos/grove-code/internal/gitx"
	"github.com/dgroos/grove-code/internal/session"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "grove-code:", err)
		os.Exit(1)
	}
}

func run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	g := gitx.New(cwd)
	repoRoot, err := g.RepoRoot(context.Background())
	if err != nil {
		return fmt.Errorf("not a git repo (run from inside a git checkout): %w", err)
	}
	cfg, err := agent.Load()
	if err != nil {
		return err
	}
	reg, err := session.Load()
	if err != nil {
		return err
	}
	m := app.New(cfg, repoRoot, reg)
	p := tea.NewProgram(m)
	_, err = p.Run()
	return err
}
