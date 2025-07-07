package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatus_String(t *testing.T) {
	tests := []struct {
		status   Status
		expected string
	}{
		{StatusTaskStarted, "Task started"},
		{StatusTaskCanceled, "Task Canceled"},
		{StatusTaskCompleted, "Task Completed"},
		{StatusPrimaryMode, ""},
		{StatusSecondaryMode, ""},
		{StatusConnected, ""},
		{Status(255), ""}, // unknown status
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, tt.status.String(), "Status.String() should match expected name")
	}
}

func TestStatus_IsFinal(t *testing.T) {
	tests := []struct {
		status   Status
		expected bool
	}{
		{StatusTaskStarted, false},
		{StatusPrimaryMode, false},
		{StatusSecondaryMode, false},
		{StatusConnected, false},
		{StatusTaskCanceled, true},
		{StatusTaskCompleted, true},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, tt.status.IsFinal(), "Status.IsFinal() mismatch")
	}
}

func TestStatus_IsFailure(t *testing.T) {
	tests := []struct {
		status   Status
		expected bool
	}{
		{StatusTaskStarted, false},
		{StatusTaskCanceled, true},
		{StatusTaskCompleted, false},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, tt.status.IsFailure(), "Status.IsFailure() mismatch")
	}
}
