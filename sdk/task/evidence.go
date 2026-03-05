package task

import (
	"context"
	"strings"
	"time"
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

const cascadeEvidenceSubmitTimeout = 10 * time.Second

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

	targetsCopy := append([]string(nil), targetSupernodeAccounts...)
	detailsCopy := make(map[string]string, len(details))
	for k, v := range details {
		detailsCopy[k] = v
	}

	// Evidence submission should not block retry loops.
	go func(parent context.Context, subject string, actionID string, targets []string, metadata map[string]string) {
		submitCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), cascadeEvidenceSubmitTimeout)
		defer cancel()

		if err := submitter.SubmitCascadeClientFailureEvidence(
			submitCtx,
			subject,
			actionID,
			targets,
			metadata,
		); err != nil {
			t.logger.Warn(submitCtx, "Failed to submit cascade client failure evidence",
				"subject_address", subject,
				"targets", targets,
				"error", err,
			)
		}
	}(ctx, subjectAddress, t.ActionID, targetsCopy, detailsCopy)
}
