package agent

import (
	"crypto/tls"
	"fmt"
	"net"
	"sync"

	"go.uber.org/zap"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	api "github.com/ishisaka/go_distribute/proglog/api/v1"
	"github.com/ishisaka/go_distribute/proglog/internal/auth"
	"github.com/ishisaka/go_distribute/proglog/internal/discovery"
	"github.com/ishisaka/go_distribute/proglog/internal/log"
	"github.com/ishisaka/go_distribute/proglog/internal/server"
)

// Agent は分散システムのノードとして機能するための主要な構造体です。
// 様々なコンポーネント（ログ、サービス、メンバーシップ管理など）を含んでいます。
type Agent struct {
	Config

	log        *log.Log
	server     *grpc.Server
	membership *discovery.Membership
	replicator *log.Replicator

	shutdown     bool
	shutdowns    chan struct{}
	shutdownLock sync.Mutex
}

// Config はシステムの設定情報を格納するための構造体です。
type Config struct {
	ServerTLSConfig *tls.Config
	PeerTLSConfig   *tls.Config
	DataDir         string
	BindAddr        string
	RPCPort         int
	NodeName        string
	StartJoinAddrs  []string
	ACLModelFile    string
	ACLPolicyFile   string
}

// RPCAddr は Config 構造体の BindAddr フィールドと RPCPort フィールドから RPC アドレスの文字列を生成して返します。
// Host とポートの分離に失敗した場合、エラーを返します。
func (c Config) RPCAddr() (string, error) {
	host, _, err := net.SplitHostPort(c.BindAddr)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%d", host, c.RPCPort), nil
}

// New は Config 構造体を基に Agent インスタンスを生成して初期化します。初期化に失敗した場合エラーを返します。
func New(config Config) (*Agent, error) {
	a := &Agent{
		Config:    config,
		shutdowns: make(chan struct{}),
	}
	setup := []func() error{
		a.setupLogger,
		a.setupLog,
		a.setupServer,
		a.setupMembership,
	}
	for _, fn := range setup {
		if err := fn(); err != nil {
			return nil, err
		}
	}
	return a, nil
}

// setupLogger は、開発用の logger を初期化し、グローバルロガーとして設定します。エラーが発生した場合は返します。
func (a *Agent) setupLogger() error {
	logger, err := zap.NewDevelopment()
	if err != nil {
		return err
	}
	zap.ReplaceGlobals(logger)
	return nil
}

// setupLog はログシステムを初期化し、エージェント内で使用可能にします。初期化に失敗した場合はエラーを返します。
func (a *Agent) setupLog() error {
	var err error
	a.log, err = log.NewLog(
		a.Config.DataDir,
		log.Config{},
	)
	return err
}

// setupServer は gRPC サーバーを初期化して起動するメソッドです。設定ファイルとセキュリティ情報を使用します。
func (a *Agent) setupServer() error {
	authorizer := auth.New(
		a.Config.ACLModelFile,
		a.Config.ACLPolicyFile,
	)
	serverConfig := &server.Config{
		CommitLog:  a.log,
		Authorizer: authorizer,
	}
	var opts []grpc.ServerOption
	if a.Config.ServerTLSConfig != nil {
		creds := credentials.NewTLS(a.Config.ServerTLSConfig)
		opts = append(opts, grpc.Creds(creds))
	}
	var err error
	a.server, err = server.NewGRPCServer(serverConfig, opts...)
	if err != nil {
		return err
	}
	rpcAddr, err := a.Config.RPCAddr()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", rpcAddr)
	if err != nil {
		return err
	}
	go func() {
		if err := a.server.Serve(ln); err != nil {
			_ = a.Shutdown()
		}
	}()
	return err
}

// setupMembership メソッドはメンバーシップ管理を初期化し、分散システムのノード間通信を可能にします。
// RPC アドレスを取得し、TLS 設定も考慮した gRPC 接続を作成します。
// replicator と discovery パッケージを用いてノードの同期および参加を構成します。
func (a *Agent) setupMembership() error {
	rpcAddr, err := a.Config.RPCAddr()
	if err != nil {
		return err
	}
	var opts []grpc.DialOption
	if a.Config.PeerTLSConfig != nil {
		opts = append(opts, grpc.WithTransportCredentials(
			credentials.NewTLS(a.Config.PeerTLSConfig),
		),
		)
	}
	conn, err := grpc.NewClient(rpcAddr, opts...)
	if err != nil {
		return err
	}
	client := api.NewLogClient(conn)
	a.replicator = &log.Replicator{
		DialOptions: opts,
		LocalServer: client,
	}
	a.membership, err = discovery.New(a.replicator, discovery.Config{
		NodeName: a.Config.NodeName,
		BindAddr: a.Config.BindAddr,
		Tags: map[string]string{
			"rpc_addr": rpcAddr,
		},
		StartJoinAddrs: a.Config.StartJoinAddrs,
	})
	return err
}

// Shutdown メソッドはエージェントの安全な終了処理を行います。
// すべてのサブコンポーネントの停止とリソース開放を処理します。
// 複数回の呼び出しに対しても安全に動作します。
func (a *Agent) Shutdown() error {
	a.shutdownLock.Lock()
	defer a.shutdownLock.Unlock()
	if a.shutdown {
		return nil
	}
	a.shutdown = true
	close(a.shutdowns)

	shutdown := []func() error{
		a.membership.Leave,
		a.replicator.Close,
		func() error {
			a.server.GracefulStop()
			return nil
		},
		a.log.Close,
	}
	for _, fn := range shutdown {
		if err := fn(); err != nil {
			return err
		}
	}
	return nil
}
