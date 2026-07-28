package handlers

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// prunedDirNames lists directory base names never descended into by walkMatch.
// Hidden directories (leading ".") are pruned separately by the walker.
var prunedDirNames = map[string]struct{}{
	"node_modules": {},
	"vendor":       {},
	"target":       {},
	"dist":         {},
	"build":        {},
	"out":          {},
}

// walkMatchMaxVisited is the hard entries-visited cap enforced by walkMatch.
// It is a var (not a const) so tests can lower it to exercise the truncation
// path without needing to synthesize 50k entries.
var walkMatchMaxVisited = 50000

// containsDoublestar reports whether pattern uses recursive semantics.
func containsDoublestar(pattern string) bool {
	return strings.Contains(pattern, "**")
}

// literalPrefix returns the longest literal path prefix of pattern (empty if
// the first segment contains a wildcard). Used to anchor the walk.
func literalPrefix(pattern string) string {
	if pattern == "" {
		return ""
	}
	segs := strings.Split(pattern, "/")
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		if strings.ContainsAny(s, "*?[{") {
			break
		}
		out = append(out, s)
	}
	if len(out) == len(segs) {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "/")
}

// walkMatchOpts describes a bounded recursive walk under a resolved root,
// returning entries whose relative path matches a doublestar pattern.
type walkMatchOpts struct {
	ctx        context.Context
	root       string
	pattern    string
	maxResults int
	maxVisited int
	wantFiles  bool
}

// walkMatchResult carries the (possibly-truncated) match list plus the reason
// for truncation, if any.
type walkMatchResult struct {
	matches   []string
	truncated bool
	reason    string
}

// errStopWalk is a sentinel used to halt fs.WalkDir when a cap is hit.
var errStopWalk = errors.New("walk_match: stop")

// walkMatch performs a bounded recursive walk from opts.root, returning entries
// whose forward-slash relative path matches opts.pattern under doublestar
// semantics. Deadline, results cap and entries-visited cap are all enforced;
// on truncation the partial list is returned with truncated=true and reason set.
func walkMatch(opts walkMatchOpts) walkMatchResult {
	res := walkMatchResult{matches: make([]string, 0, 32)}
	if opts.maxVisited <= 0 {
		opts.maxVisited = walkMatchMaxVisited
	}
	visited := 0
	fsys := os.DirFS(opts.root)
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if ctxErr := opts.ctx.Err(); ctxErr != nil {
			res.truncated = true
			res.reason = "deadline"
			return errStopWalk
		}
		visited++
		if visited > opts.maxVisited {
			res.truncated = true
			res.reason = "visited_cap"
			return errStopWalk
		}
		if d.IsDir() && p != "." {
			base := path.Base(p)
			if strings.HasPrefix(base, ".") {
				return fs.SkipDir
			}
			if _, pruned := prunedDirNames[base]; pruned {
				return fs.SkipDir
			}
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if p == "." {
			return nil
		}
		if opts.wantFiles {
			if !d.Type().IsRegular() {
				return nil
			}
		} else {
			if !d.IsDir() {
				return nil
			}
		}
		ok, mErr := doublestar.PathMatch(opts.pattern, p)
		if mErr != nil || !ok {
			return nil
		}
		res.matches = append(res.matches, p)
		if len(res.matches) >= opts.maxResults {
			res.truncated = true
			res.reason = "results_cap"
			return errStopWalk
		}
		return nil
	})
	return res
}
