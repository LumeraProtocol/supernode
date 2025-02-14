package grpctest

import (
	"testing"

	"google.golang.org/grpc/grpclog"
)

type s struct {
	Tester
}

func Test(t *testing.T) {
	RunSubTests(t, s{})
}

func (s) TestInfo(*testing.T) {
	grpclog.Info("Info", "message.")
}

func (s) TestInfoln(*testing.T) {
	grpclog.Infoln("Info", "message.")
}

func (s) TestInfof(*testing.T) {
	grpclog.Infof("%v %v.", "Info", "message")
}

func (s) TestInfoDepth(*testing.T) {
	grpclog.InfoDepth(0, "Info", "depth", "message.")
}

func (s) TestWarning(*testing.T) {
	grpclog.Warning("Warning", "message.")
}

func (s) TestWarningln(*testing.T) {
	grpclog.Warningln("Warning", "message.")
}

func (s) TestWarningf(*testing.T) {
	grpclog.Warningf("%v %v.", "Warning", "message")
}

func (s) TestWarningDepth(*testing.T) {
	grpclog.WarningDepth(0, "Warning", "depth", "message.")
}

func (s) TestError(*testing.T) {
	const numErrors = 10
	TLogger.ExpectError("Expected error")
	TLogger.ExpectError("Expected ln error")
	TLogger.ExpectError("Expected formatted error")
	TLogger.ExpectErrorN("Expected repeated error", numErrors)
	grpclog.Error("Expected", "error")
	grpclog.Errorln("Expected", "ln", "error")
	grpclog.Errorf("%v %v %v", "Expected", "formatted", "error")
	for i := 0; i < numErrors; i++ {
		grpclog.Error("Expected repeated error")
	}
}
