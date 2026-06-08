package self_healing

import (
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/action"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/action_msg"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/audit"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/audit_msg"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/auth"
	bankmod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/bank"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/node"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode_msg"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/tx"
	"github.com/LumeraProtocol/supernode/v2/pkg/testutil"
)

// fakeLumera satisfies lumera.Client by composing per-test programmable
// audit modules with the existing testutil.MockLumeraClient stubs for the
// other modules. The dispatcher only touches Audit() and AuditMsg(); the
// other methods are present solely to satisfy the interface contract.
type fakeLumera struct {
	audit    audit.Module
	auditMsg audit_msg.Module
	other    lumera.Client // testutil mock; supplies stub for non-audit modules
}

func newFakeLumera(a audit.Module, am audit_msg.Module) lumera.Client {
	c, err := testutil.NewMockLumeraClient(nil, nil)
	if err != nil {
		panic(err)
	}
	return &fakeLumera{audit: a, auditMsg: am, other: c}
}

func (f *fakeLumera) Auth() auth.Module                  { return f.other.Auth() }
func (f *fakeLumera) Action() action.Module              { return f.other.Action() }
func (f *fakeLumera) ActionMsg() action_msg.Module       { return f.other.ActionMsg() }
func (f *fakeLumera) Audit() audit.Module                { return f.audit }
func (f *fakeLumera) AuditMsg() audit_msg.Module         { return f.auditMsg }
func (f *fakeLumera) SuperNode() supernode.Module        { return f.other.SuperNode() }
func (f *fakeLumera) SuperNodeMsg() supernode_msg.Module { return f.other.SuperNodeMsg() }
func (f *fakeLumera) Bank() bankmod.Module               { return f.other.Bank() }
func (f *fakeLumera) Tx() tx.Module                      { return f.other.Tx() }
func (f *fakeLumera) Node() node.Module                  { return f.other.Node() }
func (f *fakeLumera) Close() error                       { return nil }
