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
		switch s := q.(type) {
		case *query.Substring:
			if s.FileName {
				if s.CaseSensitive {
					args = append(args, "-g", "*"+s.Pattern+"*")
				} else {
					args = append(args, "--iglob", "*"+s.Pattern+"*")
				}
			} else {
				observeCaseSensitive(s.CaseSensitive)
				reParts = append(reParts, regexp.QuoteMeta(s.Pattern))
			}
		case *query.Regexp:
			if s.FileName {
				return nil, fmt.Errorf("Unexpected file regexp filter")
			}
			observeCaseSensitive(s.CaseSensitive)
			reParts = append(reParts, s.Regexp.String())
		default:
			return nil, fmt.Errorf("Unexpected query type %T", q)
		}
	}
	re, err := syntax.Parse(fmt.Sprintf("(%s)", strings.Join(reParts, ").*(")), syntax.PerlX|syntax.UnicodeGroups)
	if err != nil {
		return nil, err
	}
	re = re.Simplify()

	// OpAnyCharNotNL is written as (?-s:.) which is unneccessary
	reStr := strings.Replace(re.String(), "(?-s:.)", ".", -1)

	if isCaseSensitive == 3 {
		return nil, fmt.Errorf("Query mixes case sensitivity")
	}
	if isCaseSensitive != 1 {
		args = append([]string{"-i"}, args...)
	}

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

func runrg(dir string, args []string) int {
	if dir != "" {
		args = append(args, "--", dir)
	}
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
	var args []string
	if len(args) > 1 {
		args = os.Args[1:]
	}
	if len(args) == 0 || (len(args) == 1 && (args[0] == "--help" || args[1] == "-h")) {
		code := runrg("", args)
		fmt.Println()
		fmt.Printf("USAGE: %s [ripgrep flags...] PATTERN\n", os.Args[0])
		os.Exit(code)
	}

	q, err := query.Parse(args[len(args)-1])
	if err != nil {
		log.Fatal(err)
	}
	q = query.Simplify(q)
	passthrough := args[:len(args)-1]

	// if we don't have a repo query, root the search from cwd
	if !hasRepoQuery(q) {
		args, err := ripgrep(q)
		if err != nil {
			log.Fatal(err)
		}
		code := runrg("", append(passthrough, args...))
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

	hasMatch := false
	for _, srcpath := range srcpaths {
		err := filepath.Walk(srcpath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}

			if !info.IsDir() {
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

			// Check if query matches repo
			q2 := query.Simplify(query.Map(q, func(q query.Q) query.Q {
				if r, ok := q.(*query.Repo); ok {
					return &query.Const{Value: strings.Contains(repo, r.Pattern)}
				}
				return q
			}))
			if c, ok := q2.(*query.Const); ok {
				if c.Value {
					hasMatch = true
					fmt.Println(path)
				}
				return filepath.SkipDir
			}

			args, err := ripgrep(q2)
			if err != nil {
				return err
			}
			code := runrg(path, append(passthrough, args...))
			if code == 0 {
				hasMatch = true
			} else if code != 1 {
				os.Exit(code)
			}

			return filepath.SkipDir
		})
		if err != nil {
			log.Fatal(err)
		}
	}

	if !hasMatch {
		os.Exit(1)
	}
}
