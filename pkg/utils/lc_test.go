package utils

import (
	"reflect"
	"testing"
)

func TestPrefix(t *testing.T) {
	tests := []struct {
		name string
		strs []string
		want string
	}{
		{"empty slice", []string{}, ""},
		{"single element", []string{"hello"}, "hello"},
		{"no common", []string{"abc", "def"}, ""},
		{"common prefix", []string{"abcdef", "abcxyz", "abc123"}, "abc"},
		{"partial", []string{"prefix", "prelude", "prevent"}, "pre"},
		// This test works because all strings share the same byte sequence for "mañ"
		{"unicode", []string{"mañana", "mañito", "mañana"}, "mañ"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Prefix(tt.strs)
			if got != tt.want {
				t.Errorf("Prefix(%v) = %q, want %q", tt.strs, got, tt.want)
			}
		})
	}
}

func TestSuffix(t *testing.T) {
	tests := []struct {
		name string
		strs []string
		want string
	}{
		{"empty slice", []string{}, ""},
		{"single element", []string{"hello"}, "hello"},
		{"no common", []string{"abc", "def"}, ""},
		{"common suffix", []string{"123abc", "xyzabc", "fooabc"}, "abc"},
		{"partial", []string{"ending", "trending", "pending"}, "ending"},
		{"unicode", []string{"mañana", "banana", "cabana"}, "ana"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Suffix(tt.strs)
			if got != tt.want {
				t.Errorf("Suffix(%v) = %q, want %q", tt.strs, got, tt.want)
			}
		})
	}
}

func TestTrimPrefix(t *testing.T) {
	tests := []struct {
		name string
		strs []string
		want []string
	}{
		{
			"no common",
			[]string{"abc", "def"},
			[]string{"abc", "def"},
		},
		{
			"common prefix",
			[]string{"abcdef", "abcxyz", "abc123"},
			[]string{"def", "xyz", "123"},
		},
		{
			"all equal",
			[]string{"test", "test", "test"},
			[]string{"", "", ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a copy so that the original input remains unchanged for reporting.
			input := make([]string, len(tt.strs))
			copy(input, tt.strs)
			TrimPrefix(input)
			if !reflect.DeepEqual(input, tt.want) {
				t.Errorf("TrimPrefix(%v) = %v, want %v", tt.strs, input, tt.want)
			}
		})
	}
}

func TestTrimSuffix(t *testing.T) {
	tests := []struct {
		name string
		strs []string
		want []string
	}{
		{
			"no common",
			[]string{"abc", "def"},
			[]string{"abc", "def"},
		},
		{
			"common suffix",
			[]string{"123abc", "xyzabc", "fooabc"},
			[]string{"123", "xyz", "foo"},
		},
		{
			"all equal",
			[]string{"test", "test", "test"},
			[]string{"", "", ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := make([]string, len(tt.strs))
			copy(input, tt.strs)
			TrimSuffix(input)
			if !reflect.DeepEqual(input, tt.want) {
				t.Errorf("TrimSuffix(%v) = %v, want %v", tt.strs, input, tt.want)
			}
		})
	}
}
