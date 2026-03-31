package task

import (
	"context"
	"strings"
	"time"

	"github.com/LumeraProtocol/supernode/v2/sdk/adapters/lumera"
)

// Optional interface so existing test doubles that only implement the base
// sdk/adapters/lumera.Client interface remain valid.
type cascadeClientFailureEvidenceSubmitter interface {
	SubmitCascadeClientFailureEvidence(
		ctx context.Context,
		subjectAddress string,
		actionID string,
		targetSupernodeAccounts []string,
		details lumera.CascadeClientFailureDetails,
	) error
}

const cascadeEvidenceSubmitTimeout = 10 * time.Second

func (t *BaseTask) submitCascadeClientFailureEvidence(
	ctx context.Context,
	subjectAddress string,
	targetSupernodeAccounts []string,
	details lumera.CascadeClientFailureDetails,
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

	if details.TaskID == "" {
		details.TaskID = t.TaskID
	}
	if details.ActionID == "" {
		details.ActionID = t.ActionID
	}

	targetsCopy := append([]string(nil), targetSupernodeAccounts...)
	detailsCopy := details

	// Evidence submission should not block retry loops.
	go func(parent context.Context, subject string, actionID string, targets []string, metadata lumera.CascadeClientFailureDetails) {
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
