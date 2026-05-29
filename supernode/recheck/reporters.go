package recheck

import (
	"context"
	"fmt"
	"sort"
	"strings"

	supernodemodule "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
)

type staticReporterSource struct {
	accounts []string
}

func NewStaticReporterSource(accounts ...string) ReporterSource {
	return staticReporterSource{accounts: accounts}
}

func (s staticReporterSource) ReporterAccounts(ctx context.Context) ([]string, error) {
	return normalizeAccounts(s.accounts), nil
}

type SupernodeReporterSource struct {
	module supernodemodule.Module
	self   string
}

func NewSupernodeReporterSource(module supernodemodule.Module, self string) *SupernodeReporterSource {
	return &SupernodeReporterSource{module: module, self: strings.TrimSpace(self)}
}

func (s *SupernodeReporterSource) ReporterAccounts(ctx context.Context) ([]string, error) {
	if s == nil || s.module == nil {
		return nil, fmt.Errorf("recheck reporter source missing supernode module")
	}
	resp, err := s.module.ListSuperNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list supernodes: %w", err)
	}
	accounts := []string{s.self}
	if resp != nil {
		for _, sn := range resp.Supernodes {
			if sn != nil {
				accounts = append(accounts, sn.SupernodeAccount)
			}
		}
	}
	return normalizeAccounts(accounts), nil
}

func normalizeAccounts(accounts []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(accounts))
	for _, account := range accounts {
		account = strings.TrimSpace(account)
		if account == "" {
			continue
		}
		if _, ok := seen[account]; ok {
			continue
		}
		seen[account] = struct{}{}
		out = append(out, account)
	}
	sort.Strings(out)
	return out
}
