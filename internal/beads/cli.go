package beads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTimeout = 15 * time.Second
	initTimeout    = 60 * time.Second
	syncTimeout    = 120 * time.Second
)

// Runner executes a bd subcommand in a directory. The returned error is the
// raw exec error; the caller is responsible for mapping it to a *CmdError.
type Runner interface {
	Run(ctx context.Context, dir string, args ...string) (stdout []byte, stderr string, err error)
}

// execRunner is the default Runner that invokes the real bd binary.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, string, error) {
	cmd := exec.CommandContext(ctx, "bd", args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		msg := err.Error()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			msg = "bd command timed out"
		} else if errors.As(err, &exitErr) {
			msg = "bd exited with non-zero status"
		}
		return nil, stderr.String(), errors.New(msg)
	}

	return stdout.Bytes(), "", nil
}

// cliClient implements Client using a Runner.
type cliClient struct {
	runner Runner
}

// runRaw executes bd with the given timeout, wrapping any runner error in *CmdError.
func (c *cliClient) runRaw(ctx context.Context, timeout time.Duration, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	out, stderr, err := c.runner.Run(ctx, dir, args...)
	if err != nil {
		return nil, &CmdError{Err: err, Stderr: stderr}
	}
	return out, nil
}

// runJSON executes bd with the default timeout and validates that the output is valid JSON.
func (c *cliClient) runJSON(ctx context.Context, dir string, args ...string) ([]byte, error) {
	out, err := c.runRaw(ctx, defaultTimeout, dir, args...)
	if err != nil {
		return nil, err
	}
	if !json.Valid(out) {
		return nil, &CmdError{Err: errors.New("bd returned invalid JSON")}
	}
	return out, nil
}

func (c *cliClient) List(ctx context.Context, dir string) ([]byte, error) {
	// An uninitialized folder has no issues yet. Return an empty list rather
	// than letting bd fail, so simply opening the Tasks view does not surface an
	// error (and does not create a .beads database just by viewing).
	if !isInitialized(dir) {
		return []byte("[]"), nil
	}
	return c.runJSON(ctx, dir, "list", "--json", "--all", "-n", "0")
}

func (c *cliClient) Status(ctx context.Context, dir string) ([]byte, error) {
	// An uninitialized folder has no issue database. Return an empty summary
	// rather than letting bd fail, so the sidebar stats line renders nothing
	// (and viewing does not create a .beads database just by querying).
	if !isInitialized(dir) {
		return []byte(`{"summary":{}}`), nil
	}
	return c.runJSON(ctx, dir, "status", "--json", "--no-activity")
}

func (c *cliClient) Show(ctx context.Context, dir, id string) ([]byte, error) {
	return c.runJSON(ctx, dir, "show", id, "--json", "--include-comments")
}

func (c *cliClient) Create(ctx context.Context, dir string, p CreateParams) ([]byte, error) {
	// Transparently initialize the beads database on first task creation so the
	// user does not have to run "bd init" manually for a new folder.
	if err := c.EnsureInitialized(ctx, dir); err != nil {
		return nil, err
	}

	args := []string{"create", p.Title, "--json"}
	if p.Type != "" {
		args = append(args, "--type", p.Type)
	}
	if p.Priority != nil {
		args = append(args, "--priority", strconv.Itoa(*p.Priority))
	}
	if p.Description != "" {
		args = append(args, "-d", p.Description)
	}
	if p.Parent != "" {
		args = append(args, "--parent", p.Parent)
	}
	if len(p.Deps) > 0 {
		args = append(args, "--deps", strings.Join(p.Deps, ","))
	}
	if p.Assignee != "" {
		args = append(args, "-a", p.Assignee)
	}
	if p.Notes != "" {
		args = append(args, "--notes", p.Notes)
	}
	return c.runJSON(ctx, dir, args...)
}

func (c *cliClient) Delete(ctx context.Context, dir, id string) error {
	_, err := c.runRaw(ctx, defaultTimeout, dir, "delete", id, "--force")
	return err
}

// listItem is the minimal shape needed to collect issue IDs from bd list.
type listItem struct {
	ID string `json:"id"`
}

// cleanupTimeout scales the bulk-delete timeout with the number of closed
// issues being removed. On the Dolt backend each delete rewrites dependency
// links, updates text references in connected issues, and commits, so large
// closed-issue sets routinely take far longer than defaultTimeout. We budget a
// generous per-issue allowance on top of a high floor.
func cleanupTimeout(n int) time.Duration {
	const perIssue = 750 * time.Millisecond
	d := time.Duration(n) * perIssue
	if d < syncTimeout {
		return syncTimeout
	}
	return d
}

func (c *cliClient) Cleanup(ctx context.Context, dir string) (int, error) {
	out, err := c.runJSON(ctx, dir, "list", "--json", "--status", "closed", "-n", "0")
	if err != nil {
		return 0, err
	}

	var items []listItem
	if err := json.Unmarshal(out, &items); err != nil {
		return 0, &CmdError{Err: errors.New("failed to parse closed issues")}
	}

	ids := make([]string, 0, len(items))
	for _, it := range items {
		if it.ID != "" {
			ids = append(ids, it.ID)
		}
	}

	if len(ids) == 0 {
		return 0, nil
	}

	args := make([]string, 0, len(ids)+2)
	args = append(args, "delete")
	args = append(args, ids...)
	args = append(args, "--force")

	if _, err := c.runRaw(ctx, cleanupTimeout(len(ids)), dir, args...); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func (c *cliClient) SetStatus(ctx context.Context, dir, id, action string) error {
	_, err := c.runRaw(ctx, defaultTimeout, dir, action, id)
	return err
}

func (c *cliClient) Update(ctx context.Context, dir string, p UpdateParams) error {
	args := []string{"update", p.ID}
	if p.Title != nil {
		args = append(args, "--title", *p.Title)
	}
	if p.Type != nil {
		args = append(args, "--type", *p.Type)
	}
	if p.Description != nil {
		args = append(args, "-d", *p.Description)
		if *p.Description == "" {
			args = append(args, "--allow-empty-description")
		}
	}
	if p.Priority != nil {
		args = append(args, "--priority", strconv.Itoa(*p.Priority))
	}
	if p.Assignee != nil {
		args = append(args, "-a", *p.Assignee)
	}
	if p.Notes != nil {
		args = append(args, "--notes", *p.Notes)
	}
	_, err := c.runRaw(ctx, defaultTimeout, dir, args...)
	return err
}

// Comment adds a comment to an issue: "bd comment <id> -- <text>". The "--"
// terminator stops flag parsing so comment text beginning with a dash is
// treated as positional text rather than a flag.
func (c *cliClient) Comment(ctx context.Context, dir, id, text string) error {
	_, err := c.runRaw(ctx, defaultTimeout, dir, "comment", id, "--", text)
	return err
}

func (c *cliClient) Dep(ctx context.Context, dir string, p DepParams) error {
	var args []string
	switch p.Action {
	case "add":
		depType := p.Type
		if depType == "" {
			depType = "blocks"
		}
		args = []string{"dep", "add", p.ID, p.DependsOn, "-t", depType}
	case "remove":
		args = []string{"dep", "remove", p.ID, p.DependsOn}
	default:
		return &CmdError{Err: errors.New("invalid dep action: " + p.Action)}
	}
	_, err := c.runRaw(ctx, defaultTimeout, dir, args...)
	return err
}
