package verifier

import "context"

// ConfigVerifierService defines verification methods
type ConfigVerifierService interface {
    VerifyConfig(ctx context.Context) (*VerificationResult, error)
}

// ConfigError represents a config validation error or warning
type ConfigError struct {
    Field    string
    Expected string
    Actual   string
    Message  string
}

// VerificationResult holds the outcome of config verification
type VerificationResult struct {
    Valid    bool
    Errors   []ConfigError
    Warnings []ConfigError
}

func (r *VerificationResult) IsValid() bool { return r.Valid && len(r.Errors) == 0 }
func (r *VerificationResult) HasWarnings() bool { return len(r.Warnings) > 0 }
func (r *VerificationResult) Summary() string {
    if !r.IsValid() { return "invalid: check errors" }
    if r.HasWarnings() { return "valid with warnings" }
    return "valid"
}
