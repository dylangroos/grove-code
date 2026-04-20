// Package agent loads the YAML config and resolves an agent launch spec.
package agent

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/dylangroos/grove-code/internal/config"
	"gopkg.in/yaml.v3"
)

type File struct {
	Version  int       `yaml:"version"`
	Defaults Defaults  `yaml:"defaults"`
	Agents   []Agent   `yaml:"agents"`
	Repos    []RepoCfg `yaml:"repos"`
}

type Defaults struct {
	WorktreeRoot string `yaml:"worktree_root"`
	BranchPrefix string `yaml:"branch_prefix"`
	Shell        string `yaml:"shell"`
	// Layout is "split" (terminal + diff side-by-side) or "tabbed"
	// (single pane at a time). Toggle at runtime with `s`.
	Layout string `yaml:"layout"`
}

type Agent struct {
	ID      string            `yaml:"id"`
	Name    string            `yaml:"name"`
	Command []string          `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env"`
	Cwd     string            `yaml:"cwd"`
}

type RepoCfg struct {
	Path         string `yaml:"path"`
	DefaultAgent string `yaml:"default_agent"`
}

// TemplateVars is the set of variables available in templated fields.
type TemplateVars struct {
	WorktreePath string
	Branch       string
	RepoRoot     string
}

// Spec is the fully resolved launch spec for an agent + worktree.
type Spec struct {
	AgentID string
	Command []string
	Env     []string // KEY=VALUE entries to overlay on os.Environ
	Cwd     string
}

func Load() (*File, error) {
	path := config.ConfigFile()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultConfig(), nil
		}
		return nil, err
	}
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Version == 0 {
		f.Version = 1
	}
	if f.Defaults.BranchPrefix == "" {
		f.Defaults.BranchPrefix = "grove/"
	}
	if f.Defaults.WorktreeRoot == "" {
		f.Defaults.WorktreeRoot = config.DataDir() + "/worktrees"
	}
	if f.Defaults.Layout == "" {
		f.Defaults.Layout = "tabbed"
	}
	return &f, nil
}

func Save(f *File) error {
	if err := config.EnsureDir(config.ConfigDir()); err != nil {
		return err
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	return os.WriteFile(config.ConfigFile(), data, 0o644)
}

// Resolve builds a Spec from an Agent + template vars, expanding ${ENV} and {{.X}}.
func Resolve(a Agent, vars TemplateVars) (Spec, error) {
	env, err := resolveEnv(a.Env)
	if err != nil {
		return Spec{}, err
	}
	cwd := a.Cwd
	if cwd == "" {
		cwd = "{{.WorktreePath}}"
	}
	cwd, err = applyTemplate(cwd, vars)
	if err != nil {
		return Spec{}, fmt.Errorf("agent %s cwd: %w", a.ID, err)
	}
	cwd = config.ExpandHome(cwd)

	cmd := append([]string{}, a.Command...)
	cmd = append(cmd, a.Args...)
	for i, c := range cmd {
		c, err = applyTemplate(c, vars)
		if err != nil {
			return Spec{}, fmt.Errorf("agent %s command: %w", a.ID, err)
		}
		cmd[i] = c
	}
	if len(cmd) == 0 {
		return Spec{}, fmt.Errorf("agent %s: empty command", a.ID)
	}
	return Spec{AgentID: a.ID, Command: cmd, Env: env, Cwd: cwd}, nil
}

func resolveEnv(m map[string]string) ([]string, error) {
	var out []string
	for k, v := range m {
		expanded, err := expandEnvStrict(v)
		if err != nil {
			return nil, fmt.Errorf("env %s: %w", k, err)
		}
		out = append(out, k+"="+expanded)
	}
	return out, nil
}

// expandEnvStrict expands ${VAR} references; errors on missing vars.
func expandEnvStrict(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		if i+1 < len(s) && s[i] == '$' && s[i+1] == '{' {
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				return "", fmt.Errorf("unterminated ${ in %q", s)
			}
			name := s[i+2 : i+2+end]
			val, ok := os.LookupEnv(name)
			if !ok {
				return "", fmt.Errorf("env var %s is not set", name)
			}
			b.WriteString(val)
			i += 2 + end + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String(), nil
}

func applyTemplate(s string, vars TemplateVars) (string, error) {
	if !strings.Contains(s, "{{") {
		return s, nil
	}
	tmpl, err := template.New("v").Parse(s)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (f *File) FindAgent(id string) (Agent, bool) {
	for _, a := range f.Agents {
		if a.ID == id {
			return a, true
		}
	}
	return Agent{}, false
}

func defaultConfig() *File {
	return &File{
		Version: 1,
		Defaults: Defaults{
			BranchPrefix: "grove/",
			WorktreeRoot: config.DataDir() + "/worktrees",
			Layout:       "split",
		},
		Agents: []Agent{
			{ID: "claude", Name: "Claude Code", Command: []string{"claude"}},
			{ID: "codex", Name: "OpenAI Codex CLI", Command: []string{"codex"}},
			{ID: "opencode", Name: "opencode", Command: []string{"opencode"}},
		},
	}
}
