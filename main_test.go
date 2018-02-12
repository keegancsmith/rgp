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
		{"foo", []string{"-i", "-e", "foo"}},
		{"foo bar", []string{"-i", "-e", "foo.*?bar"}},
		{"foo bar case:yes", []string{"-e", "foo.*?bar"}},
		{"foo Bar", []string{"-e", "(?i:foo).*?Bar"}},
		{"foo.*bar", []string{"-i", "-e", "foo.*bar"}},
		{"foo f:bar", []string{"--iglob", "*bar*", "-i", "-e", "foo"}},
		{"foo f:bar case:yes", []string{"-g", "*bar*", "-e", "foo"}},
		{"f:bar", []string{"--iglob", "*bar*", "--files"}},
		{"f:bar f:baz", []string{"--iglob", "*bar*", "--iglob", "*baz*", "--files"}},
		{"f:bar -f:baz", []string{"--iglob", "*bar*", "--iglob", "!*baz*", "--files"}},
		{"foo f:bar*.go case:yes", []string{"-g", "bar*.go", "-e", "foo"}},
		{"foo f:bar*", []string{"--iglob", "bar*", "-i", "-e", "foo"}},
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
