// Package gitx wraps the git binary. We shell out to avoid go-git's worktree
// performance and API gaps — see plan risks.
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type Runner struct {
	Dir string
}

func New(dir string) *Runner { return &Runner{Dir: dir} }

func (r *Runner) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if r.Dir != "" {
		cmd.Dir = r.Dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (r *Runner) RepoRoot(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "rev-parse", "--show-toplevel")
	return strings.TrimSpace(string(out)), err
}

func (r *Runner) CurrentBranch(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	return strings.TrimSpace(string(out)), err
}

type Worktree struct {
	Path   string
	Branch string
	Head   string
	Bare   bool
}

func (r *Runner) WorktreeList(ctx context.Context) ([]Worktree, error) {
	out, err := r.run(ctx, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	var wts []Worktree
	var cur Worktree
	for _, rec := range strings.Split(string(out), "\x00\x00") {
		rec = strings.Trim(rec, "\x00")
		if rec == "" {
			continue
		}
		cur = Worktree{}
		for _, line := range strings.Split(rec, "\x00") {
			switch {
			case strings.HasPrefix(line, "worktree "):
				cur.Path = strings.TrimPrefix(line, "worktree ")
			case strings.HasPrefix(line, "HEAD "):
				cur.Head = strings.TrimPrefix(line, "HEAD ")
			case strings.HasPrefix(line, "branch "):
				cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
			case line == "bare":
				cur.Bare = true
			}
		}
		if cur.Path != "" {
			wts = append(wts, cur)
		}
	}
	return wts, nil
}

// WorktreeAdd creates a new worktree at path on a new branch forked from base.
// If base is empty, branches off the current HEAD.
func (r *Runner) WorktreeAdd(ctx context.Context, path, branch, base string) error {
	args := []string{"worktree", "add", "-b", branch, path}
	if base != "" {
		args = append(args, base)
	}
	_, err := r.run(ctx, args...)
	return err
}

func (r *Runner) WorktreeRemove(ctx context.Context, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	_, err := r.run(ctx, args...)
	return err
}

type StatusEntry struct {
	X, Y byte // porcelain codes
	Path string
}

func (r *Runner) StatusPorcelain(ctx context.Context) ([]StatusEntry, error) {
	out, err := r.run(ctx, "status", "--porcelain=v1", "-z")
	if err != nil {
		return nil, err
	}
	var entries []StatusEntry
	data := string(out)
	for len(data) > 0 {
		// Record ends at NUL; renames have an extra NUL-terminated "from" field.
		end := strings.IndexByte(data, 0)
		if end < 0 {
			end = len(data)
		}
		if end < 3 {
			break
		}
		rec := data[:end]
		data = data[end+1:]
		e := StatusEntry{X: rec[0], Y: rec[1], Path: rec[3:]}
		if e.X == 'R' || e.X == 'C' {
			// Consume the "from" path (one more NUL-terminated field).
			if sep := strings.IndexByte(data, 0); sep >= 0 {
				data = data[sep+1:]
			}
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// DiffHEAD returns unified diff bytes for all uncommitted changes vs HEAD,
// including staged + unstaged + untracked (untracked handled separately).
func (r *Runner) DiffHEAD(ctx context.Context) ([]byte, error) {
	out, err := r.run(ctx, "diff", "--no-color", "HEAD")
	if err != nil {
		// No HEAD yet (fresh repo) -> return empty.
		if strings.Contains(err.Error(), "unknown revision") || strings.Contains(err.Error(), "bad revision") {
			return nil, nil
		}
		return nil, err
	}
	return out, nil
}

// DiffCommit returns the diff introduced by a single commit.
func (r *Runner) DiffCommit(ctx context.Context, sha string) ([]byte, error) {
	return r.run(ctx, "show", "--no-color", "--format=", sha)
}

type Commit struct {
	SHA     string
	Short   string
	Author  string
	Date    time.Time
	Subject string
}

func (r *Runner) Log(ctx context.Context, rev string, limit int) ([]Commit, error) {
	if rev == "" {
		rev = "HEAD"
	}
	args := []string{"log", "--format=%H%x1f%h%x1f%an%x1f%at%x1f%s", fmt.Sprintf("-n%d", limit), rev}
	out, err := r.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 5)
		if len(parts) < 5 {
			continue
		}
		var t time.Time
		if secs, ok := parseUnix(parts[3]); ok {
			t = time.Unix(secs, 0)
		}
		commits = append(commits, Commit{
			SHA: parts[0], Short: parts[1], Author: parts[2], Date: t, Subject: parts[4],
		})
	}
	return commits, nil
}

func parseUnix(s string) (int64, bool) {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
}

func (r *Runner) Push(ctx context.Context, remote, branch string, setUpstream bool) ([]byte, error) {
	args := []string{"push"}
	if setUpstream {
		args = append(args, "-u")
	}
	args = append(args, remote, branch)
	return r.run(ctx, args...)
}

func (r *Runner) Add(ctx context.Context, paths ...string) error {
	args := append([]string{"add", "--"}, paths...)
	_, err := r.run(ctx, args...)
	return err
}

func (r *Runner) Commit(ctx context.Context, msg string) error {
	_, err := r.run(ctx, "commit", "-m", msg)
	return err
}
