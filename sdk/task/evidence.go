package task

import (
	"context"
	"strings"
)

// Optional interface so existing test doubles that only implement the base
// sdk/adapters/lumera.Client interface remain valid.
type cascadeClientFailureEvidenceSubmitter interface {
	SubmitCascadeClientFailureEvidence(
		ctx context.Context,
		subjectAddress string,
		actionID string,
		targetSupernodeAccounts []string,
		details map[string]string,
	) error
}

func (t *BaseTask) submitCascadeClientFailureEvidence(
	ctx context.Context,
	subjectAddress string,
	targetSupernodeAccounts []string,
	details map[string]string,
) {
	subjectAddress = strings.TrimSpace(subjectAddress)
	if subjectAddress == "" {
		return
	}

	submitter, ok := any(t.client).(cascadeClientFailureEvidenceSubmitter)
	if !ok {
		t.logger.Debug(ctx, "Cascade client failure evidence submitter not configured")
		return
	}

	if details == nil {
		details = map[string]string{}
	}
	if _, exists := details["task_id"]; !exists {
		details["task_id"] = t.TaskID
	}
	if _, exists := details["action_id"]; !exists {
		details["action_id"] = t.ActionID
	}

	if err := submitter.SubmitCascadeClientFailureEvidence(
		ctx,
		subjectAddress,
		t.ActionID,
		targetSupernodeAccounts,
		details,
	); err != nil {
		t.logger.Warn(ctx, "Failed to submit cascade client failure evidence",
			"subject_address", subjectAddress,
			"targets", targetSupernodeAccounts,
			"error", err,
		)
	}
}
