// Copyright (c) 2013-2017 The btcsuite developers
// Copyright (c) 2017 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/sat20-labs/indexer/share/btclucky"
	"github.com/sat20-labs/satoshinet/addrmgr"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/blockchain/indexers"
	"github.com/sat20-labs/satoshinet/connmgr"
	"github.com/sat20-labs/satoshinet/database"
	assetIndexer "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/mempool"
	"github.com/sat20-labs/satoshinet/mining"
	"github.com/sat20-labs/satoshinet/mining/cpuminer"
	posminer "github.com/sat20-labs/satoshinet/mining/posminer/utils"
	"github.com/sat20-labs/satoshinet/netsync"
	"github.com/sat20-labs/satoshinet/peer"
	"github.com/sat20-labs/satoshinet/txscript"

	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
	"github.com/sirupsen/logrus"
)

var logger = NewLogger()

func NewLogger() *logrus.Logger {
	log := logrus.New()
	log.SetLevel(logrus.TraceLevel)
	log.SetFormatter(&CustomTextFormatter{})
	return log
}

// 创建一个带模块名的日志实例
func GetLoggerEntry(module string) *logrus.Entry {
	return logger.WithField("module", module)
}

type CustomTextFormatter struct{}

func (f *CustomTextFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	var b bytes.Buffer

	timestamp := entry.Time.Format("2006-01-02 15:04:05")
	b.WriteString(fmt.Sprintf("%s ", timestamp))
	b.WriteString(fmt.Sprintf("[%s] ", entry.Level.String()))
	moduleName, ok := entry.Data["module"].(string)
	if !ok {
		moduleName = "default"
	}
	b.WriteString(fmt.Sprintf("%s: ", moduleName))
	b.WriteString(entry.Message)
	b.WriteByte('\n')

	return b.Bytes(), nil
}

// Loggers per subsystem.  A single backend logger is created and all subsystem
// loggers created from it will write to the backend.  When adding new
// subsystems, add the subsystem logger variable here and to the
// subsystemLoggers map.
//
// Loggers can not be used before the log rotator has been initialized with a
// log file.  This must be performed early during application startup by calling
// initLogRotator.
var (
	adxrLog         = GetLoggerEntry("ADXR")
	amgrLog         = GetLoggerEntry("AMGR")
	cmgrLog         = GetLoggerEntry("CMGR")
	bcdbLog         = GetLoggerEntry("BCDB")
	btcdLog         = GetLoggerEntry("SNET")
	btclLog         = GetLoggerEntry("BTCL")
	chanLog         = GetLoggerEntry("CHAN")
	discLog         = GetLoggerEntry("DISC")
	indxLog         = GetLoggerEntry("INDX")
	minrLog         = GetLoggerEntry("MINR")
	peerLog         = GetLoggerEntry("PEER")
	rpcsLog         = GetLoggerEntry("RPCS")
	scrpLog         = GetLoggerEntry("SCRP")
	srvrLog         = GetLoggerEntry("SRVR")
	syncLog         = GetLoggerEntry("SYNC")
	txmpLog         = GetLoggerEntry("TXMP")
	anchorLog       = GetLoggerEntry("ANCH")
	assetIndexerLog = GetLoggerEntry("AIDX")
)

// Initialize package-global logger variables.
func init() {

	addrmgr.UseLogger(amgrLog)
	connmgr.UseLogger(cmgrLog)
	database.UseLogger(bcdbLog)
	blockchain.UseLogger(chanLog)
	indexers.UseLogger(indxLog)
	mining.UseLogger(minrLog)
	cpuminer.UseLogger(minrLog)
	posminer.UseLogger(minrLog)
	peer.UseLogger(peerLog)
	txscript.UseLogger(scrpLog)
	netsync.UseLogger(syncLog)
	mempool.UseLogger(txmpLog)
	anchortx.UseLogger(anchorLog)
	assetIndexer.UseLogger(assetIndexerLog)
	btclucky.UseLogger(btclLog)

}

// subsystemLoggers maps each subsystem identifier to its associated logger.
var subsystemLoggers = map[string]*logrus.Entry{
	"ADXR": adxrLog,
	"AMGR": amgrLog,
	"CMGR": cmgrLog,
	"BCDB": bcdbLog,
	"BTCD": btcdLog,
	"BTCL": btclLog,
	"CHAN": chanLog,
	"DISC": discLog,
	"INDX": indxLog,
	"MINR": minrLog,
	"PEER": peerLog,
	"RPCS": rpcsLog,
	"SCRP": scrpLog,
	"SRVR": srvrLog,
	"SYNC": syncLog,
	"TXMP": txmpLog,
	"ANCH": anchorLog,
	"AIDX": assetIndexerLog,
}

// initLogRotator initializes the logging rotater to write logs to logFile and
// create roll files in the same directory.  It must be called before the
// package-global log rotater variables are used.
func initLogRotator(logFile string) {
	logDir, file := filepath.Split(logFile)
	err := os.MkdirAll(logDir, 0700)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create log directory: %v\n", err)
		os.Exit(1)
	}

	fileHook, err := rotatelogs.New(
		logDir+"/"+file+".%Y%m%d%H%M.log",
		rotatelogs.WithLinkName(logDir+"/"+file+".log"),
		rotatelogs.WithMaxAge(30*24*time.Hour),
		rotatelogs.WithRotationTime(24*time.Hour),
	)
	if err == nil {
		var writers []io.Writer = []io.Writer{os.Stdout}
		writers = append(writers, fileHook)
		logger.SetOutput(io.MultiWriter(writers...))
	}

}

// setLogLevel sets the logging level for provided subsystem.  Invalid
// subsystems are ignored.  Uninitialized subsystems are dynamically created as
// needed.
func setLogLevel(subsystemID string, logLevel string) {
	// Ignore invalid subsystems.
	logger, ok := subsystemLoggers[subsystemID]
	if !ok {
		return
	}

	// Defaults to info if the log level is invalid.
	level, _ := logrus.ParseLevel(logLevel)
	logger.Level = level
}

// setLogLevels sets the log level for all subsystem loggers to the passed
// level.  It also dynamically creates the subsystem loggers as needed, so it
// can be used to initialize the logging system.
func setLogLevels(logLevel string) {
	// Configure all sub-systems with the new logging level.  Dynamically
	// create loggers as needed.
	level, _ := logrus.ParseLevel(logLevel)
	logger.SetLevel(level)
	for subsystemID := range subsystemLoggers {
		setLogLevel(subsystemID, logLevel)
	}
}

// directionString is a helper function that returns a string that represents
// the direction of a connection (inbound or outbound).
func directionString(inbound bool) string {
	if inbound {
		return "inbound"
	}
	return "outbound"
}

// pickNoun returns the singular or plural form of a noun depending
// on the count n.
func pickNoun(n uint64, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
