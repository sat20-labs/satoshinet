package rpcserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	server            *http.Server
	cancelRequests    context.CancelFunc
	handlerMu         sync.Mutex
	handlerWG         sync.WaitGroup
	stopping          bool
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
	r.Use(func(c *gin.Context) {
		// Serialize admission with Stop so no database user can enter after
		// the drain starts, even if net/http had already accepted a request.
		s.handlerMu.Lock()
		if s.stopping {
			s.handlerMu.Unlock()
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		s.handlerWG.Add(1)
		s.handlerMu.Unlock()
		defer s.handlerWG.Done()
		c.Next()
	})
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

	listener, err := net.Listen("tcp", rpcUrl)
	if err != nil {
		return err
	}
	s.handlerMu.Lock()
	s.stopping = false
	s.handlerMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelRequests = cancel
	s.server = &http.Server{Addr: listener.Addr().String(), Handler: r,
		BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() {
		if err := s.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "indexer RPC serve failed: %v\n", err)
		}
	}()
	return nil
}

// Stop cancels requests and forcibly closes their network connections, then
// waits for database users. Server.Close alone does not wait for handlers;
// canceling a context alone does not interrupt a blocked request-body read.
func (s *Rpc) Stop() error {
	if s == nil || s.server == nil {
		return nil
	}
	s.handlerMu.Lock()
	s.stopping = true
	s.handlerMu.Unlock()
	s.cancelRequests()
	err := s.server.Close()
	s.handlerWG.Wait()
	return err
}
