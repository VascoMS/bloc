package main

import (
	"strings"
	"testing"
)

func TestRunValidatesRequiredReportArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing corpus", args: nil, want: "corpus"},
		{name: "missing cluster", args: []string{"-corpus", "corpus.jsonl"}, want: "cluster"},
		{
			name: "empty output",
			args: []string{"-corpus", "corpus.jsonl", "-cluster-config", "cluster.json", "-out", ""},
			want: "output",
		},
		{
			name: "zero slot",
			args: []string{"-corpus", "corpus.jsonl", "-cluster-config", "cluster.json", "-slot", "0"},
			want: "slot",
		},
		{
			name: "too few samples",
			args: []string{"-corpus", "corpus.jsonl", "-cluster-config", "cluster.json", "-samples-per-class", "99"},
			want: "exactly 100",
		},
		{
			name: "too many samples",
			args: []string{"-corpus", "corpus.jsonl", "-cluster-config", "cluster.json", "-samples-per-class", "101"},
			want: "exactly 100",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := run(test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want fragment %q", err, test.want)
			}
		})
	}
}
