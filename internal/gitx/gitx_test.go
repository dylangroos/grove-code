package gitx

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

// setupRepo creates a temporary git repo with one commit.
func setupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "initial")
	return dir
}

func TestRepoRootAndLog(t *testing.T) {
	dir := setupRepo(t)
	g := New(dir)
	ctx := context.Background()
	root, err := g.RepoRoot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if root == "" {
		t.Fatal("empty repo root")
	}
	commits, err := g.Log(ctx, "HEAD", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 {
		t.Fatalf("want 1 commit, got %d", len(commits))
	}
	if commits[0].Subject != "initial" {
		t.Fatalf("subject %q", commits[0].Subject)
	}
}

func TestWorktreeLifecycle(t *testing.T) {
	dir := setupRepo(t)
	g := New(dir)
	ctx := context.Background()
	wt := filepath.Join(t.TempDir(), "wt")
	if err := g.WorktreeAdd(ctx, wt, "feature-x", ""); err != nil {
		t.Fatalf("add: %v", err)
	}
	wts, err := g.WorktreeList(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, w := range wts {
		if w.Branch == "feature-x" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("worktree not in list: %+v", wts)
	}
	if err := g.WorktreeRemove(ctx, wt, true); err != nil {
		t.Fatalf("remove: %v", err)
	}
}

func TestDiffHEADEmpty(t *testing.T) {
	dir := setupRepo(t)
	g := New(dir)
	d, err := g.DiffHEAD(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(d) != 0 {
		t.Fatalf("want empty diff, got %q", d)
	}
}

func TestDiffWorktreeIncludesUntracked(t *testing.T) {
	dir := setupRepo(t)
	// Add a new untracked file.
	if err := os.WriteFile(filepath.Join(dir, "NEW.md"), []byte("# hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := New(dir)
	d, err := g.DiffWorktree(context.Background())
	if err != nil {
		t.Fatalf("DiffWorktree: %v", err)
	}
	if len(d) == 0 {
		t.Fatal("want non-empty diff for untracked file")
	}
	files, _, err := gitdiff.Parse(bytes.NewReader(d))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	foundNew := false
	for _, f := range files {
		if f.IsNew && (f.NewName == "NEW.md" || filepath.Base(f.NewName) == "NEW.md") {
			foundNew = true
		}
	}
	if !foundNew {
		t.Fatalf("NEW.md not reported as new file; got: %+v", files)
	}
}

func TestDiffHEADWithChange(t *testing.T) {
	dir := setupRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi there\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := New(dir)
	d, err := g.DiffHEAD(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(d) == 0 {
		t.Fatal("want non-empty diff")
	}
}
