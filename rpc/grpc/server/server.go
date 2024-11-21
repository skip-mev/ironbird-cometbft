package server

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"google.golang.org/grpc"

	pbblksvc "github.com/cometbft/cometbft/api/cometbft/services/block/v1"
	pbblkressvc "github.com/cometbft/cometbft/api/cometbft/services/block_results/v1"
	pbprunesvc "github.com/cometbft/cometbft/api/cometbft/services/pruning/v1"
	pbversvc "github.com/cometbft/cometbft/api/cometbft/services/version/v1"
	"github.com/cometbft/cometbft/libs/log"
	grpcerr "github.com/cometbft/cometbft/rpc/grpc/errors"
	blkressvc "github.com/cometbft/cometbft/rpc/grpc/server/services/blockresultservice"
	blksvc "github.com/cometbft/cometbft/rpc/grpc/server/services/blockservice"
	prunesvc "github.com/cometbft/cometbft/rpc/grpc/server/services/pruningservice"
	versvc "github.com/cometbft/cometbft/rpc/grpc/server/services/versionservice"
	"github.com/cometbft/cometbft/state"
	"github.com/cometbft/cometbft/store"
	"github.com/cometbft/cometbft/types"
)

// Server is a gRPC server that provides CometBFT RPC services.
type Server struct {
	grpcSrv *grpc.Server
	logger  log.Logger
	network string
	address string
}

type config struct {
	logger     log.Logger
	listenAddr string

	// services
	verSvc    pbversvc.VersionServiceServer
	blkSvc    pbblksvc.BlockServiceServer
	blkResSvc pbblkressvc.BlockResultsServiceServer
	pruneSvc  pbprunesvc.PruningServiceServer
}

func New(cfg *config) (*Server, error) {
	if cfg == nil {
		errMsg := "could not create gRPC server because its configuration is missing"
		return nil, errors.New(errMsg)
	}

	parts := strings.SplitN(cfg.listenAddr, "://", 2)
	if len(parts) != 2 {
		return nil, grpcerr.ErrInvalidRemoteAddress{Addr: cfg.listenAddr}
	}

	grpcSrv := grpc.NewServer()

	if cfg.verSvc != nil {
		pbversvc.RegisterVersionServiceServer(grpcSrv, cfg.verSvc)
		cfg.logger.Debug("Registered version service")
	}
	if cfg.blkSvc != nil {
		pbblksvc.RegisterBlockServiceServer(grpcSrv, cfg.blkSvc)
		cfg.logger.Debug("Registered block service")
	}
	if cfg.blkResSvc != nil {
		pbblkressvc.RegisterBlockResultsServiceServer(grpcSrv, cfg.blkResSvc)
		cfg.logger.Debug("Registered block results service")
	}

	srv := &Server{
		logger:  cfg.logger,
		grpcSrv: grpcSrv,
		network: parts[0],
		address: parts[1],
	}

	return srv, nil
}

func NewPrivileged(cfg *config) (*Server, error) {
	if cfg == nil {
		errMsg := "could not create gRPC server because its configuration is missing"
		return nil, errors.New(errMsg)
	}

	parts := strings.SplitN(cfg.listenAddr, "://", 2)
	if len(parts) != 2 {
		return nil, grpcerr.ErrInvalidRemoteAddress{Addr: cfg.listenAddr}
	}

	grpcSrv := grpc.NewServer()

	if cfg.pruneSvc != nil {
		pbprunesvc.RegisterPruningServiceServer(grpcSrv, cfg.pruneSvc)
		cfg.logger.Debug("Registered pruning service")
	}

	srv := &Server{
		logger:  cfg.logger,
		grpcSrv: grpcSrv,
		network: parts[0],
		address: parts[1],
	}

	return srv, nil
}

func (s *Server) Serve() error {
	lis, err := net.Listen(s.network, s.address)
	if err != nil {
		formatStr := "setting up network endpoint at %s://%s: %w"
		return fmt.Errorf(formatStr, s.network, s.address, err)
	}

	var (
		lisAddr = lis.Addr().String()
		logMsg  = "Starting CometBFT gRPC server on " + lisAddr
	)
	s.logger.Info("serve", "msg", logMsg)

	go func() {
		if err := s.grpcSrv.Serve(lis); err != nil {
			s.logger.Error("gRPC server serving requests on %s: %w", lisAddr, err)
		}
	}()

	return nil
}

func (s *Server) GracefulStop() {
	s.grpcSrv.GracefulStop()
}

func (s *Server) Stop() {
	s.grpcSrv.Stop()
}

//nolint:revive
func NewConfig(l log.Logger, listenAddr string) *config {
	return &config{
		logger:     l.With("module", "grpc-server"),
		listenAddr: listenAddr,
	}
}

func (c *config) WithVersionService() *config {
	c.verSvc = versvc.New()
	return c
}

func (c *config) WithBlockService(
	blkStore *store.BlockStore,
	eBus *types.EventBus,
) *config {
	c.blkSvc = blksvc.New(blkStore, eBus, c.logger)
	return c
}

func (c *config) WithBlockResultsService(
	blkStore *store.BlockStore,
	stStore state.Store,
) *config {
	c.blkResSvc = blkressvc.New(blkStore, stStore, c.logger)
	return c
}

func (c *config) WithPruningService(pruner *state.Pruner) *config {
	c.pruneSvc = prunesvc.New(pruner, c.logger)
	return c
}

// Option is any function that allows for configuration of the gRPC server
// during its creation.
type Option func(*serverBuilder)

type serverBuilder struct {
	listener            net.Listener
	versionService      pbversvc.VersionServiceServer
	blockService        pbblksvc.BlockServiceServer
	blockResultsService pbblkressvc.BlockResultsServiceServer
	logger              log.Logger
	grpcOpts            []grpc.ServerOption
}

func newServerBuilder(listener net.Listener) *serverBuilder {
	return &serverBuilder{
		listener: listener,
		logger:   log.NewNopLogger(),
		grpcOpts: make([]grpc.ServerOption, 0),
	}
}

// Listen starts a new net.Listener on the given address.
//
// The address must conform to the standard listener address format used by
// CometBFT, i.e. "<protocol>://<address>". For example,
// "tcp://127.0.0.1:26670".
func Listen(addr string) (net.Listener, error) {
	parts := strings.SplitN(addr, "://", 2)
	if len(parts) != 2 {
		return nil, grpcerr.ErrInvalidRemoteAddress{Addr: addr}
	}
	return net.Listen(parts[0], parts[1])
}

// WithVersionService enables the version service on the CometBFT server.
func WithVersionService() Option {
	return func(b *serverBuilder) {
		b.versionService = versvc.New()
	}
}

// WithBlockService enables the block service on the CometBFT server.
func WithBlockService(store *store.BlockStore, eventBus *types.EventBus, logger log.Logger) Option {
	return func(b *serverBuilder) {
		b.blockService = blksvc.New(store, eventBus, logger)
	}
}

func WithBlockResultsService(bs *store.BlockStore, ss state.Store, logger log.Logger) Option {
	return func(b *serverBuilder) {
		b.blockResultsService = blkressvc.New(bs, ss, logger)
	}
}

// WithLogger enables logging using the given logger. If not specified, the
// gRPC server does not log anything.
func WithLogger(logger log.Logger) Option {
	return func(b *serverBuilder) {
		b.logger = logger.With("module", "grpc-server")
	}
}

// WithGRPCOption allows one to specify Google gRPC server options during the
// construction of the CometBFT gRPC server.
func WithGRPCOption(opt grpc.ServerOption) Option {
	return func(b *serverBuilder) {
		b.grpcOpts = append(b.grpcOpts, opt)
	}
}

// Serve constructs and runs a CometBFT gRPC server using the given listener
// and options.
//
// This function only returns upon error, otherwise it blocks the current
// goroutine.
func Serve(listener net.Listener, opts ...Option) error {
	b := newServerBuilder(listener)
	for _, opt := range opts {
		opt(b)
	}
	server := grpc.NewServer(b.grpcOpts...)
	if b.versionService != nil {
		pbversvc.RegisterVersionServiceServer(server, b.versionService)
		b.logger.Debug("Registered version service")
	}
	if b.blockService != nil {
		pbblksvc.RegisterBlockServiceServer(server, b.blockService)
		b.logger.Debug("Registered block service")
	}
	if b.blockResultsService != nil {
		pbblkressvc.RegisterBlockResultsServiceServer(server, b.blockResultsService)
		b.logger.Debug("Registered block results service")
	}
	b.logger.Info("serve", "msg", fmt.Sprintf("Starting gRPC server on %s", listener.Addr()))
	return server.Serve(b.listener)
}
