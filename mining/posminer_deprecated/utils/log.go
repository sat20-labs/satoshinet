// Copyright (c) 2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package utils

import (
	"io"

	"github.com/sirupsen/logrus"
)

// log is a logger that is initialized with no output filters.  This
// means the package will not perform any logging by default until the caller
// requests it.
var Log *logrus.Entry

// The default amount of logging is none.
func init() {
	DisableLog()
}

// DisableLog disables all library log output.  Logging output is disabled
// by default until UseLogger is called.
func DisableLog() {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	Log = logger.WithField("", "")
}

// UseLogger uses a specified Logger to output package logging info.
func UseLogger(logger *logrus.Entry) {
	Log = logger
}
