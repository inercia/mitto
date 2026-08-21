// Package pathglob provides a bounded recursive path-glob walker shared by the
// workspace listing HTTP handlers (internal/web/handlers) and by the CEL/template
// FileExists/DirExists helpers (internal/cel). It sits below both callers in
// the dependency graph: no imports of internal/web or internal/cel.
package pathglob

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// PrunedDirNames lists directory base names never descended into by WalkMatch.
// Hidden directories (leading ".") are pruned separately by the walker.
var PrunedDirNames = map[string]struct{}{
	"node_modules": {},
	"vendor":       {},
	"target":       {},
	"dist":         {},
	"build":        {},
	"out":          {},
}

// WalkMatchMaxVisited is the hard entries-visited cap enforced by WalkMatch.
// It is a var (not a const) so tests can lower it to exercise the truncation
// path without needing to synthesize 50k entries.
var WalkMatchMaxVisited = 50000

// ContainsDoublestar reports whether pattern uses recursive semantics.
func ContainsDoublestar(pattern string) bool {
	return strings.Contains(pattern, "**")
}

// PatternsContainDoublestar reports whether ANY pattern in the list uses
// recursive semantics. Used to choose between the non-recursive os.ReadDir
// branch and the recursive walker branch in the handlers.
func PatternsContainDoublestar(patterns []string) bool {
	for _, p := range patterns {
		if ContainsDoublestar(p) {
			return true
		}
	}
	return false
}

// LiteralPrefix returns the longest literal path prefix of pattern (empty if
// the first segment contains a wildcard). Used to anchor the walk.
func LiteralPrefix(pattern string) string {
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

// CommonLiteralPrefix returns the shared literal prefix across all patterns:
// non-empty only when every pattern has the same non-empty LiteralPrefix.
// This keeps the walk-root optimization safe when patterns diverge (e.g.
// "docs/**/*.md" and "spec/**/*.md" share no prefix and must walk from root).
func CommonLiteralPrefix(patterns []string) string {
	if len(patterns) == 0 {
		return ""
	}
	first := LiteralPrefix(patterns[0])
	if first == "" {
		return ""
	}
	for _, p := range patterns[1:] {
		if LiteralPrefix(p) != first {
			return ""
		}
	}
	return first
}

// WalkMatchOpts describes a bounded recursive walk under a resolved root,
// returning entries whose relative path matches any of the doublestar
// patterns (union semantics; a single entry that matches multiple patterns
// is only reported once).
type WalkMatchOpts struct {
	Ctx        context.Context
	Root       string
	Patterns   []string
	MaxResults int
	MaxVisited int
	WantFiles  bool
}

// WalkMatchResult carries the (possibly-truncated) match list plus the reason
// for truncation, if any.
type WalkMatchResult struct {
	Matches   []string
	Truncated bool
	Reason    string
}

// errStopWalk is a sentinel used to halt fs.WalkDir when a cap is hit.
var errStopWalk = errors.New("pathglob: stop")

// WalkMatch performs a bounded recursive walk from opts.Root, returning entries
// whose forward-slash relative path matches opts.Patterns under doublestar
// semantics. Deadline, results cap and entries-visited cap are all enforced;
// on truncation the partial list is returned with Truncated=true and Reason set.
func WalkMatch(opts WalkMatchOpts) WalkMatchResult {
	res := WalkMatchResult{Matches: make([]string, 0, 32)}
	if opts.MaxVisited <= 0 {
		opts.MaxVisited = WalkMatchMaxVisited
	}
	visited := 0
	fsys := os.DirFS(opts.Root)
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if ctxErr := opts.Ctx.Err(); ctxErr != nil {
			res.Truncated = true
			res.Reason = "deadline"
			return errStopWalk
		}
		visited++
		if visited > opts.MaxVisited {
			res.Truncated = true
			res.Reason = "visited_cap"
			return errStopWalk
		}
		if d.IsDir() && p != "." {
			base := path.Base(p)
			if strings.HasPrefix(base, ".") {
				return fs.SkipDir
			}
			if _, pruned := PrunedDirNames[base]; pruned {
				return fs.SkipDir
			}
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if p == "." {
			return nil
		}
		if opts.WantFiles {
			if !d.Type().IsRegular() {
				return nil
			}
		} else {
			if !d.IsDir() {
				return nil
			}
		}
		matched := false
		for _, pat := range opts.Patterns {
			ok, mErr := doublestar.PathMatch(pat, p)
			if mErr != nil || !ok {
				continue
			}
			matched = true
			break
		}
		if !matched {
			return nil
		}
		res.Matches = append(res.Matches, p)
		if len(res.Matches) >= opts.MaxResults {
			res.Truncated = true
			res.Reason = "results_cap"
			return errStopWalk
		}
		return nil
	})
	return res
}
