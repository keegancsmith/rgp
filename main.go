package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
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

	var reParts []string
	for _, q := range and.Children {
		switch s := q.(type) {
		case *query.Substring:
			observeCaseSensitive(s.CaseSensitive)
			reParts = append(reParts, regexp.QuoteMeta(s.Pattern))
		case *query.Regexp:
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

	var args []string
	if isCaseSensitive == 3 {
		return nil, fmt.Errorf("Query mixes case sensitivity")
	}
	if isCaseSensitive != 1 {
		args = []string{"-i"}
	}

	args = append(args, "-e", reStr)

	return args, nil
}

func main() {
	q, err := query.Parse(strings.Join(os.Args[1:], " "))
	if err != nil {
		log.Fatal(err)
	}
	q = query.Simplify(q)
	args, err := ripgrep(q)
	if err != nil {
		log.Fatal(err)
	}
	cmd := exec.Command("rg", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			if ws, ok := e.Sys().(syscall.WaitStatus); ok {
				os.Exit(ws.ExitStatus())
			}
		}
		log.Fatal(err)
	}
}
