package main

import (
	"reflect"
	"testing"

	"github.com/google/zoekt/query"
)

func TestRipGrep(t *testing.T) {
	cases := []struct {
		Query string
		Args  []string
	}{
		{"foo", []string{"-i", "-e", "(foo)"}},
		{"foo bar", []string{"-i", "-e", "(foo).*(bar)"}},
		{"foo bar case:yes", []string{"-e", "(foo).*(bar)"}},
		{"foo.*bar", []string{"-i", "-e", "(foo.*bar)"}},
	}
	for _, tt := range cases {
		q, err := query.Parse(tt.Query)
		if err != nil {
			t.Fatal(tt.Query, err)
		}
		q = query.Simplify(q)
		got, err := ripgrep(q)
		if err != nil && tt.Args != nil {
			t.Errorf("%s got error %v", q, err)
		}
		if !reflect.DeepEqual(got, tt.Args) {
			t.Errorf("%s == %v != %v", q, got, tt.Args)
		}
	}
}
