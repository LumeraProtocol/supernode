package verifier

import (
	"context"
	"strings"
)

// ConfigVerifierService defines the interface for config verification service
type ConfigVerifierService interface {
	// VerifyConfig performs comprehensive config validation against chain
	VerifyConfig(ctx context.Context) (*VerificationResult, error)
}

// VerificationResult contains the results of config verification
type VerificationResult struct {
	Valid    bool           `json:"valid"`
	Errors   []ConfigError  `json:"errors,omitempty"`
	Warnings []ConfigError  `json:"warnings,omitempty"`
}

// ConfigError represents a configuration validation error or warning
type ConfigError struct {
	Field    string `json:"field"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
	Message  string `json:"message"`
}

// IsValid returns true if all verifications passed
func (vr *VerificationResult) IsValid() bool {
	return vr.Valid && len(vr.Errors) == 0
}

// HasWarnings returns true if there are any warnings
func (vr *VerificationResult) HasWarnings() bool {
	return len(vr.Warnings) > 0
}

// Summary returns a human-readable summary of verification results
func (vr *VerificationResult) Summary() string {
	if vr.IsValid() && !vr.HasWarnings() {
		return "✓ Config verification successful"
	}

	var summary string
	for _, err := range vr.Errors {
		summary += "✗ " + err.Message + "\n"
	}

	for _, warn := range vr.Warnings {
		summary += "⚠ " + warn.Message + "\n"
	}

	return strings.TrimSuffix(summary, "\n")
}