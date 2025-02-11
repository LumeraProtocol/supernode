package mixins

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/LumeraProtocol/supernode/common/errors"
	"github.com/LumeraProtocol/supernode/common/storage/memory"
	storageMock "github.com/LumeraProtocol/supernode/common/storage/test"
	pastelMock "github.com/LumeraProtocol/supernode/pastel/test"
	"github.com/tj/assert"
)

func TestStoreFileNameIntoStorage(t *testing.T) {
	type args struct {
		amount  int64
		percent uint
		burnErr error
		txid    string
	}

	testCases := map[string]struct {
		args    args
		wantErr error
	}{
		"success": {
			args: args{
				txid:    "test-txid",
				amount:  10,
				percent: 5,
				burnErr: nil,
			},
			wantErr: errors.New("test"),
		},
	}

	for name, tc := range testCases {
		tc := tc

		t.Run(fmt.Sprintf("testCase-%v", name), func(t *testing.T) {
			t.Parallel()

			fsMock := storageMock.NewMockFileStorage()

			fileMock := storageMock.NewMockFile()
			fileMock.ListenOnClose(nil).ListenOnRead(0, io.EOF)
			fsMock.ListenOnOpen(fileMock, nil)

			pastelClientMock := pastelMock.NewMockClient(t)
			pastelClientMock.ListenOnSendFromAddress(tc.args.txid, tc.args.burnErr)

			h := NewFilesHandler(fsMock, memory.NewKeyValue(), 5*time.Second)
			name := "test"
			_, _, err := h.StoreFileNameIntoStorage(context.Background(), &name)
			if tc.wantErr != nil {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}
