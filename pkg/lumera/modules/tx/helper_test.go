package tx

import (
	"fmt"
	"testing"
)

func TestIsSequenceMismatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "grpc simulation mismatch",
			err: fmt.Errorf(
				"simulation failed: simulation error: rpc error: code = Unknown desc = account sequence mismatch, expected 7855, got 7854: incorrect account sequence [cosmos/cosmos-sdk@v0.53.0/x/auth/ante/sigverify.go:290] with gas used: '35369'",
			),
			want: true,
		},
		{
			name: "broadcast raw_log mismatch",
			err: fmt.Errorf(
				"tx failed: code=32 codespace=sdk height=0 gas_wanted=0 gas_used=0 raw_log=account sequence mismatch, expected 10, got 9: incorrect account sequence",
			),
			want: true,
		},
		{
			name: "unrelated expected/got",
			err:  fmt.Errorf("expected 5, got 4"),
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isSequenceMismatch(tt.err); got != tt.want {
				t.Fatalf("isSequenceMismatch() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseExpectedSequence(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("account sequence mismatch, expected 7855, got 7854: incorrect account sequence")
	if got, ok := parseExpectedSequence(err); !ok || got != 7855 {
		t.Fatalf("parseExpectedSequence() = (%d, %v), want (7855, true)", got, ok)
	}

	if _, ok := parseExpectedSequence(fmt.Errorf("incorrect account sequence")); ok {
		t.Fatalf("parseExpectedSequence() unexpectedly matched message without expected/got")
	}

	if _, ok := parseExpectedSequence(nil); ok {
		t.Fatalf("parseExpectedSequence() unexpectedly matched nil error")
	}
}
