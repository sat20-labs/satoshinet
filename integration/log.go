//go:build rpctest
// +build rpctest

package integration

import (
	"os"

	"github.com/sat20-labs/satoshinet/rpcclient"
	"github.com/sirupsen/logrus"
)

type logWriter struct{}

func (logWriter) Write(p []byte) (n int, err error) {
	os.Stdout.Write(p)
	return len(p), nil
}

func init() {
	backendLog := logrus.New()
	testLog := backendLog.WithField("module", "ITEST")
	backendLog.SetLevel(logrus.DebugLevel)

	rpcclient.UseLogger(testLog)
}
