package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
	"syscall"

	prompt "github.com/c-bata/go-prompt"
	"github.com/google/zoekt/query"
	"github.com/keegancsmith/rgp/internal/fastwalk"
)

const debug = false

func ripgrep(q query.Q) ([]string, error) {
	// Q is fully hierarchical with many token types, but we are only
	// supporting a very flat limited subset of that.
	and, ok := q.(*query.And)
	if !ok {
		and = &query.And{Children: []query.Q{q}}
	}

	var args []string
	var reParts []*syntax.Regexp
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
			re, err := syntax.Parse(regexp.QuoteMeta(s.Pattern), syntax.PerlX|syntax.UnicodeGroups)
			if err != nil {
				log.Fatal(err)
			}
			if !s.CaseSensitive {
				re.Flags = re.Flags | syntax.FoldCase
			}
			reParts = append(reParts, re)
		case *query.Regexp:
			if s.FileName {
				return nil, fmt.Errorf("Unexpected file regexp filter")
			}
			if isNot {
				return nil, fmt.Errorf("Do not support negative pattern matches")
			}
			re := s.Regexp
			if !s.CaseSensitive {
				re.Flags = re.Flags | syntax.FoldCase
			}
			reParts = append(reParts, re)
		default:
			return nil, fmt.Errorf("Unexpected query type %T", q)
		}
	}

	if len(reParts) == 0 {
		// No regexes specified, so return matching files
		args = append(args, "--files")
		return args, nil
	}

	// We simplify case insensitive regexes by removing the regex flag and
	// adding a ripgrep flag. This is done so we create readable
	// regexes. However, it doesn't effect correctness.
	caseInsensitive := true
	for _, re := range reParts {
		caseInsensitive = caseInsensitive && (re.Flags&syntax.FoldCase != 0)
	}
	if caseInsensitive {
		args = append(args, "-i")
		for _, re := range reParts {
			re.Flags = re.Flags &^ syntax.FoldCase
		}
	}

	// Join up the regexp
	var joined *syntax.Regexp
	sep, err := syntax.Parse(".*?", syntax.PerlX|syntax.UnicodeGroups)
	if err != nil {
		log.Fatal(err)
	}
	joined = &syntax.Regexp{Op: syntax.OpConcat}
	for i, re := range reParts {
		if i != 0 {
			joined.Sub = append(joined.Sub, sep, re)
		} else {
			joined.Sub = append(joined.Sub, re)
		}
	}
	joined = joined.Simplify()

	// OpAnyCharNotNL is written as (?-s:.) which is unneccessary
	reStr := strings.Replace(joined.String(), "(?-s:.)", ".", -1)

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
	if debug {
		log.Println(args)
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

func srcpaths() []string {
	paths := filepath.SplitList(os.Getenv("SRCPATH"))
	if len(paths) == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			log.Fatal(err)
		}
		paths = []string{cwd}
	}
	return paths
}

type repoPath struct {
	Path string
	Repo string
	Err  error
}

func walkSRCPath() <-chan repoPath {
	c := make(chan repoPath, 8)
	go func() {
		defer close(c)
		for _, srcpath := range srcpaths() {
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

				c <- repoPath{
					Repo: repo,
					Path: path,
				}
				return filepath.SkipDir
			})
			if err != nil {
				c <- repoPath{Err: err}
				return
			}
		}
	}()
	return c
}

func executor(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}

	cmd := exec.Command(os.Args[0], strings.Fields(s)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("Got error: %s\n", err.Error())
	}
	return
}

func completer(d prompt.Document) []prompt.Suggest {
	word := strings.TrimSpace(d.GetWordBeforeCursor())
	idx := strings.Index(word, ":")
	if idx < 0 {
		s := []prompt.Suggest{
			{Text: "case:", Description: "Sets case sensitivity yes|no|auto. Defaults to auto."},
			{Text: "file:", Description: "Limit results to files matching glob."},
			{Text: "repo:", Description: "Limit results to files matching repo substring."},
		}
		if word != "" {
			s = append([]prompt.Suggest{{Text: word, Description: "Search for lines matching " + word}}, s...)
		}
		return prompt.FilterHasPrefix(s, word, true)
	}
	typ, query := word[:idx], word[idx+1:]
	switch typ {
	case "case":
		s := []prompt.Suggest{
			{Text: "case:yes", Description: "Searches matching case."},
			{Text: "case:no", Description: "Searches case insensitively."},
			{Text: "case:auto", Description: "(Default) Searches case insensitively if the pattern is all lowercase."},
		}
		return prompt.FilterHasPrefix(s, word, true)
	case "r", "repo":
		type scoredRepo struct {
			Score int
			Repo  string
		}
		var repos []scoredRepo
		for rp := range walkSRCPath() {
			if rp.Err != nil {
				log.Println("srcpath walk failed:", rp.Err)
				continue
			}
			idx := strings.LastIndex(rp.Repo, query)
			if idx >= 0 {
				// Prefer matches near the end of the string
				score := len(rp.Repo) - idx
				repos = append(repos, scoredRepo{Score: score, Repo: rp.Repo})
			}
		}
		sort.Slice(repos, func(i, j int) bool {
			if repos[i].Score != repos[j].Score {
				return repos[i].Score < repos[j].Score
			}
			return repos[i].Repo < repos[j].Repo
		})
		var s []prompt.Suggest
		if len(repos) > 1 {
			s = append(s, prompt.Suggest{Text: word, Description: fmt.Sprintf("Limit results %d repos", len(repos))})
		}
		for _, r := range repos {
			s = append(s, prompt.Suggest{Text: typ + ":" + r.Repo})
		}
		return s
	case "f", "file":
		return []prompt.Suggest{{Text: word, Description: "Limit results to files matching glob " + query}}

	}
	return nil
}

func main() {
	if len(os.Args) == 1 {
		fmt.Printf("SRCPATH=%s\n", strings.Join(srcpaths(), string(os.PathListSeparator)))
		fmt.Println("Please use `Ctrl-D` to exit this program.")
		defer fmt.Println("Bye!")
		p := prompt.New(
			executor,
			completer,
		)
		p.Run()
		return
	}

	var (
		passthrough []string
		q           query.Q
	)
	{
		args := os.Args[1:]
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

	var noRepoQ query.Q
	var paths []string
	for rp := range walkSRCPath() {
		if rp.Err != nil {
			log.Fatal(rp.Err)
		}
		q2 := simplifyRepoQuery(q, rp.Repo)
		if c, ok := q2.(*query.Const); ok && !c.Value {
			continue
		}
		noRepoQ = q2
		paths = append(paths, rp.Path)
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
