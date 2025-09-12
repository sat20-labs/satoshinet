package rpcserver

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	//"github.com/gin-contrib/logger"
	"github.com/gin-gonic/gin"
	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
	//"github.com/rs/zerolog"
	"github.com/sat20-labs/satoshinet/indexer/indexer"

	// indexerrpc "github.com/sat20-labs/indexer/rpcserver"

	sindexer "github.com/sat20-labs/satoshinet/indexer/rpcserver/indexer"
	"github.com/sat20-labs/satoshinet/indexer/rpcserver/satoshinet"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

const (
	STRICT_TRANSPORT_SECURITY   = "strict-transport-security"
	CONTENT_SECURITY_POLICY     = "content-security-policy"
	CACHE_CONTROL               = "cache-control"
	VARY                        = "vary"
	ACCESS_CONTROL_ALLOW_ORIGIN = "access-control-allow-origin"
	TRANSFER_ENCODING           = "transfer-encoding"
	CONTENT_ENCODING            = "content-encoding"
)

const (
	CONTEXT_TYPE_TEXT = "text/html; charset=utf-8"
	CONTENT_TYPE_JSON = "application/json"
)

type Rpc struct {
	indexerService    *sindexer.Service
	satoshinetService *satoshinet.Service
}

func NewRpc(baseIndexer *indexer.IndexerMgr) *Rpc {
	return &Rpc{
		indexerService:    sindexer.NewService(baseIndexer),
		satoshinetService: satoshinet.NewService(),
	}
}

func (s *Rpc) Start(rpcUrl, rpcProxy, rpcLogFile string) error {
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	var writers []io.Writer
	if rpcLogFile != "" {
		exePath, _ := os.Executable()
		executableName := filepath.Base(exePath)
		if strings.Contains(executableName, "debug") {
			executableName = "debug"
		}
		executableName += ".rpc"
		fileHook, err := rotatelogs.New(
			rpcLogFile+"/"+executableName+".%Y%m%d%H%M.log",
			rotatelogs.WithLinkName(rpcLogFile+"/"+executableName+".log"),
			rotatelogs.WithMaxAge(7*24*time.Hour),
			rotatelogs.WithRotationTime(24*time.Hour),
		)
		if err != nil {
			return fmt.Errorf("failed to create RotateFile hook, error %s", err)
		}
		writers = append(writers, fileHook)
	}
	writers = append(writers, os.Stdout)
	gin.DefaultWriter = io.MultiWriter(writers...)
	// 记录每一个请求，数据太多
	// r.Use(logger.SetLogger(
	// 	logger.WithLogger(logger.Fn(func(c *gin.Context, l zerolog.Logger) zerolog.Logger {
	// 		if c.Request.Header["Authorization"] == nil {
	// 			return l
	// 		}
	// 		return l.With().
	// 			Str("Authorization", c.Request.Header["Authorization"][0]).
	// 			Logger()
	// 	})),
	// ))
	r.Use(gin.Recovery())

	config := cors.Config{
		AllowOrigins: []string{"*", "sat20.org", "ordx.market", "localhost"},
		AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders: []string{"Origin", "Content-Length", "Content-Type", "Authorization"},
		// ExposeHeaders:    []string{"Content-Length"},
		// AllowCredentials: true,
		MaxAge: 12 * time.Hour,
	}
	config.AllowOrigins = []string{"*"}
	config.OptionsResponseStatusCode = 200
	r.Use(cors.New(config))

	// doc
	//indexerrpc.InitApiDoc(swaggerHost, swaggerSchemes, rpcProxy)
	r.GET(rpcProxy+"/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// common header
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set(VARY, "Origin")
		c.Writer.Header().Add(VARY, "Access-Control-Request-Method")
		c.Writer.Header().Add(VARY, "Access-Control-Request-Headers")

		c.Writer.Header().Del(CONTENT_SECURITY_POLICY)
		c.Writer.Header().Set(
			CONTENT_SECURITY_POLICY,
			"default-src 'self'",
		)

		c.Writer.Header().Set(
			STRICT_TRANSPORT_SECURITY,
			"max-age=31536000; includeSubDomains; preload",
		)

		c.Writer.Header().Set(
			ACCESS_CONTROL_ALLOW_ORIGIN,
			"*",
		)

		c.Next()
	})

	// // zip encoding
	// r.Use(
	// 	gzip.Gzip(gzip.DefaultCompression,
	// 		gzip.WithExcludedPathsRegexs(
	// 			[]string{
	// 				// `.*\/btc\/.*`,
	// 			},
	// 		),
	// 	),
	// )

	// Compression middleware
	// r.Use(indexerrpc.CompressionMiddleware())

	// router
	s.indexerService.InitRouter(r, rpcProxy)
	s.satoshinetService.InitRouter(r, rpcProxy)

	parts := strings.Split(rpcUrl, ":")
	var port string
	if len(parts) < 2 {
		return fmt.Errorf("invalid url")
	}
	port = parts[1]
	
	// 先检查端口
	if err := checkPort(port); err != nil {
		return err
	}

	go r.Run(rpcUrl)
	return nil
}

func checkPort(port string) error {
	// 方法1: 尝试监听该端口
	addr := fmt.Sprintf(":%s", port)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("port %s is in use: %v", port, err)
	}
	l.Close()
	return nil
}
