// Package cascadekit provides small, pure utilities for generating,
// parsing, signing and validating Cascade artefacts used by the supernode
// register/download flows.
//
// Scope:
//   - Build and sign layout metadata (RaptorQ layout) and index files
//   - Generate redundant metadata files and index files + their IDs
//   - Extract and decode index payloads from the on-chain signatures string
//   - Compute data hashes for request metadata
//   - Verify single-block layout consistency (explicit error if more than 1 block)
//
// Non-goals:
//   - No network or chain dependencies (verification is left to callers)
//   - No logging; keep functions small and deterministic
//   - No orchestration helpers; this package exposes building blocks only
package cascadekit

