package testutil

import (
	"errors"

	"github.com/LumeraProtocol/supernode/v2/pkg/github"
)

// FakeClient is a small test double for github.GithubClient.
// It is intentionally minimal (avoids generated mocks) and can be reused
// across packages' tests.
type FakeClient struct {
	LatestStable *github.Release
	Releases     []*github.Release

	ListReleasesErr        error
	LatestStableReleaseErr error

	CallsLatestStable int
	CallsListReleases int
}

func (f *FakeClient) GetLatestRelease() (*github.Release, error) {
	return nil, errors.New("not implemented")
}

func (f *FakeClient) GetLatestStableRelease() (*github.Release, error) {
	f.CallsLatestStable++
	if f.LatestStableReleaseErr != nil {
		return nil, f.LatestStableReleaseErr
	}
	if f.LatestStable == nil {
		return nil, errors.New("no stable release configured")
	}
	return f.LatestStable, nil
}

func (f *FakeClient) ListReleases() ([]*github.Release, error) {
	f.CallsListReleases++
	if f.ListReleasesErr != nil {
		return nil, f.ListReleasesErr
	}
	return f.Releases, nil
}

func (f *FakeClient) GetRelease(tag string) (*github.Release, error) {
	return nil, errors.New("not implemented")
}

func (f *FakeClient) GetReleaseTarballURL(version string) (string, error) {
	return "", errors.New("not implemented")
}
