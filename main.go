package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"strings"
	"syscall"

	"github.com/google/zoekt/query"
	"github.com/keegancsmith/rgp/internal/fastwalk"
)

func ripgrep(q query.Q) ([]string, error) {
	// Q is fully hierarchical with many token types, but we are only
	// supporting a very flat limited subset of that.
	and, ok := q.(*query.And)
	if !ok {
		and = &query.And{Children: []query.Q{q}}
	}

	isCaseSensitive := 0 // 0=unknown, 1=yes, 2=no, 3=both
	observeCaseSensitive := func(cs bool) {
		if cs {
			isCaseSensitive = isCaseSensitive | 1
		} else {
			isCaseSensitive = isCaseSensitive | 2
		}
	}

	var args []string
	var reParts []string
	for _, q := range and.Children {
		isNot := false
		if s, ok := q.(*query.Not); ok {
			isNot = true
			q = s.Child
		}

		switch s := q.(type) {
		case *query.Glob:
			pattern := s.Pattern
			if isNot {
				pattern = "!" + pattern
			}
			if s.CaseSensitive {
				args = append(args, "-g", pattern)
			} else {
				args = append(args, "--iglob", pattern)
			}
		case *query.Substring:
			if s.FileName {
				return nil, fmt.Errorf("Unexpected file substr filter")
			}
			if isNot {
				return nil, fmt.Errorf("Do not support negative pattern matches")
			}
			observeCaseSensitive(s.CaseSensitive)
			reParts = append(reParts, regexp.QuoteMeta(s.Pattern))
		case *query.Regexp:
			if s.FileName {
				return nil, fmt.Errorf("Unexpected file regexp filter")
			}
			if isNot {
				return nil, fmt.Errorf("Do not support negative pattern matches")
			}
			observeCaseSensitive(s.CaseSensitive)
			reParts = append(reParts, s.Regexp.String())
		default:
			return nil, fmt.Errorf("Unexpected query type %T", q)
		}
	}

	if isCaseSensitive == 3 {
		return nil, fmt.Errorf("Query mixes case sensitivity")
	}
	if isCaseSensitive != 1 {
		args = append([]string{"-i"}, args...)
	}

	if len(reParts) == 0 {
		// No regexes specified, so return matching files
		args = append(args, "--files")
		return args, nil
	}

	re, err := syntax.Parse(fmt.Sprintf("(%s)", strings.Join(reParts, ").*?(")), syntax.PerlX|syntax.UnicodeGroups)
	if err != nil {
		return nil, err
	}
	re = re.Simplify()

	// OpAnyCharNotNL is written as (?-s:.) which is unneccessary
	reStr := strings.Replace(re.String(), "(?-s:.)", ".", -1)

	args = append(args, "-e", reStr)
	return args, nil
}

func hasRepoQuery(q query.Q) bool {
	hasRepo := false
	query.VisitAtoms(q, func(q query.Q) {
		if _, ok := q.(*query.Repo); ok {
			hasRepo = true
		}
	})
	return hasRepo
}

func simplifyRepoQuery(q query.Q, repo string) query.Q {
	return query.Simplify(query.Map(q, func(q query.Q) query.Q {
		if r, ok := q.(*query.Repo); ok {
			return &query.Const{Value: strings.Contains(repo, r.Pattern)}
		}
		return q
	}))
}

func runrg(args []string) int {
	cmd := exec.Command("rg", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			if ws, ok := e.Sys().(syscall.WaitStatus); ok {
				return ws.ExitStatus()
			}
		}
		log.Fatal(err)
	}
	return 0
}

func main() {
	var (
		passthrough []string
		q           query.Q
	)
	{
		var args []string
		if len(os.Args) > 1 {
			args = os.Args[1:]
		}
		if len(args) == 0 || (len(args) == 1 && (args[0] == "--help" || args[0] == "-h")) {
			code := runrg(args)
			fmt.Println()
			fmt.Printf("USAGE: %s [ripgrep flags...] PATTERN\n", os.Args[0])
			os.Exit(code)
		}

		dashDash := false
		var rawQ string
		for i, arg := range args {
			if arg == "--" {
				dashDash = true
				passthrough = args[:i]
				rawQ = strings.Join(args[i+1:], " ")
				break
			}
		}
		if !dashDash {
			rawQ = strings.Join(args, " ")
		}

		// TODO maybe a mode which takes a regex emacs ivy builds and
		// splitting it back into a pattern.

		var err error
		q, err = query.Parse(rawQ)
		if err != nil {
			log.Fatal(err)
		}
		q = query.Simplify(q)
	}

	// if we don't have a repo query, root the search from cwd
	if !hasRepoQuery(q) {
		args, err := ripgrep(q)
		if err != nil {
			log.Fatal(err)
		}
		code := runrg(append(passthrough, args...))
		os.Exit(code)
	}

	srcpaths := filepath.SplitList(os.Getenv("SRCPATH"))
	if len(srcpaths) == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			log.Fatal(err)
		}
		srcpaths = []string{cwd}
	}

	var noRepoQ query.Q
	var paths []string
	for _, srcpath := range srcpaths {
		err := fastwalk.Walk(srcpath, func(path string, typ os.FileMode) error {
			if typ != os.ModeDir {
				return nil
			}

			if base := filepath.Base(path); len(base) > 0 && base[0] == '.' {
				return filepath.SkipDir
			}

			if _, err := os.Stat(filepath.Join(path, ".git")); os.IsNotExist(err) {
				return nil
			}

			repo, err := filepath.Rel(srcpath, path)
			if err != nil {
				return err
			}

			q2 := simplifyRepoQuery(q, repo)
			if c, ok := q2.(*query.Const); ok && !c.Value {
				return filepath.SkipDir
			}
			noRepoQ = q2
			paths = append(paths, path)
			return filepath.SkipDir
		})
		if err != nil {
			log.Fatal(err)
		}
	}

	// Update q to be the pattern without the repo atoms.
	if noRepoQ == nil {
		// we didn't match anything
		os.Exit(1)
	}
	q = noRepoQ

	if _, ok := q.(*query.Const); ok {
		// If we simplify down to a constant, we are a repo query only.
		for _, path := range paths {
			fmt.Println(path)
		}
		return
	}

	args, err := ripgrep(q)
	if err != nil {
		log.Fatal(err)
	}
	args = append(passthrough, args...)
	args = append(args, paths...)
	code := runrg(args)
	os.Exit(code)
}
