package utils

import (
	"strings"
)

// TrimPrefix removes the longest common prefix from all provided strings.
func TrimPrefix(strs []string) {
	p := Prefix(strs)
	if p == "" {
		return
	}
	for i, s := range strs {
		strs[i] = strings.TrimPrefix(s, p)
	}
}

// TrimSuffix removes the longest common suffix from all provided strings.
func TrimSuffix(strs []string) {
	s := Suffix(strs)
	if s == "" {
		return
	}
	for i, str := range strs {
		strs[i] = strings.TrimSuffix(str, s)
	}
}

// Prefix returns the longest common prefix of the provided strings.
func Prefix(strs []string) string {
	return longestCommonXfix(strs, true)
}

// Suffix returns the longest common suffix of the provided strings.
func Suffix(strs []string) string {
	return longestCommonXfix(strs, false)
}

func longestCommonXfix(strs []string, pre bool) string {
	if len(strs) == 0 {
		return ""
	}

	common := strs[0]
	for _, s := range strs[1:] {
		// If common is empty, no need to continue.
		if common == "" {
			break
		}

		// Determine the maximum index to check.
		n := len(common)
		if len(s) < n {
			n = len(s)
		}

		if pre {
			// Compare characters from the start.
			i := 0
			for ; i < n; i++ {
				if common[i] != s[i] {
					break
				}
			}
			common = common[:i]
		} else {
			// Compare characters from the end.
			i := 0
			for ; i < n; i++ {
				if common[len(common)-1-i] != s[len(s)-1-i] {
					break
				}
			}
			common = common[len(common)-i:]
		}
	}
	return common
}
