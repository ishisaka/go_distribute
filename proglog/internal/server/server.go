package server

import (
	"context"
	"time"

	api "github.com/ishisaka/go_distribute/proglog/api/v1"

	grpcMiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpcAuth "github.com/grpc-ecosystem/go-grpc-middleware/auth"
	grpcZap "github.com/grpc-ecosystem/go-grpc-middleware/logging/zap"
	grpcCtxtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"

	"go.opencensus.io/plugin/ocgrpc"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// Config は gRPC サーバー構築時に必要な設定情報を保持する構造体です。
// CommitLog と Authorizer を管理します。
type Config struct {
	CommitLog  CommitLog
	Authorizer Authorizer
}

const (
	objectWildcard = "*"
	produceAction  = "produce"
	consumeAction  = "consume"
)

// Authorizer インターフェースは、特定の主題、対象、アクションに対するアクセスを許可または拒否する機能を提供します。
// 主に認可ロジックの実装を目的としています。
type Authorizer interface {

	// Authorize メソッドは、指定された主題、対象、アクションに基づきアクセス権を確認し、適切なエラーを返します。
	Authorize(subject, object, action string) error
}

var _ api.LogServer = (*grpcServer)(nil)

// grpcServer は gRPC サーバーの主要な構造体です。
// api.UnimplementedLogServer を埋め込み、LogServer インターフェースに準拠します。
// Config を利用してログ操作を管理します。
type grpcServer struct {
	api.UnimplementedLogServer
	*Config
}

// CommitLog は、ログへのデータの追加と読み取りを管理するインターフェースです。
// Append メソッドでデータを追加し、Read メソッドで特定のオフセットのデータを読み取ります。
type CommitLog interface {

	// Append は新しいレコードをログに追加し、そのレコードのオフセットを返します。
	// エラーが発生した場合、エラーを返します。
	Append(*api.Record) (uint64, error)

	// Read は指定されたオフセットに対応するログレコードを取得します。
	// 存在しない場合はエラーを返します。
	Read(uint64) (*api.Record, error)
}

// NewGRPCServer は、新しい gRPC サーバーを作成して返す関数です。
// 指定された設定および任意の gRPC サーバーオプションを使用して初期化されます。
// Config 構造体に基づいて grpcServer を生成し、LogServer として登録します。
// エラーが発生した場合は nil とエラーを返します。
func NewGRPCServer(config *Config, grpcOpts ...grpc.ServerOption) (
	*grpc.Server,
	error,
) {
	// Zapの設定
	logger := zap.L().Named("server")
	zapOpts := []grpcZap.Option{
		grpcZap.WithDurationField(
			func(duration time.Duration) zapcore.Field {
				return zap.Int64(
					"grpc.time_ns",
					duration.Nanoseconds(),
				)
			},
		),
	}

	// OpenCensusがメトリクスとトレースを設定
	// 全てのリクエストをトレース
	trace.ApplyConfig(trace.Config{DefaultSampler: trace.AlwaysSample()})
	err := view.Register(ocgrpc.DefaultServerViews...)
	if err != nil {
		return nil, err
	}

	grpcOpts = append(grpcOpts, grpc.StreamInterceptor(
		grpcMiddleware.ChainStreamServer(
			// インターセプターとしてZapログを組み込む
			grpcCtxtags.StreamServerInterceptor(),
			grpcZap.StreamServerInterceptor(logger, zapOpts...),
			// インターセプターとしてauthenticateを組み込む
			grpcAuth.StreamServerInterceptor(authenticate),
		)), grpc.UnaryInterceptor(grpcMiddleware.ChainUnaryServer(
		grpcCtxtags.UnaryServerInterceptor(),
		grpcZap.UnaryServerInterceptor(logger, zapOpts...),
		grpcAuth.UnaryServerInterceptor(authenticate),
	)),
		grpc.StatsHandler(&ocgrpc.ServerHandler{}),
	)
	gsrv := grpc.NewServer(grpcOpts...)
	srv, err := newgrpcServer(config)
	if err != nil {
		return nil, err
	}
	api.RegisterLogServer(gsrv, srv)
	return gsrv, nil
}

// newgrpcServer は、新しい gRPC サーバーを作成し、初期化します。
// Config 構造体を受け取り、その設定を使用して grpcServer を生成します。
// nolint:all
func newgrpcServer(config *Config) (srv *grpcServer, err error) {
	srv = &grpcServer{
		Config: config,
	}
	return srv, nil
}

// Produce メソッドは、指定されたリクエストに基づき新しいレコードをログに追加し、結果のオフセットをレスポンスとして返します。
// コンテキストを受け取り、エラーが発生した場合は nil とエラーを返します。
func (s *grpcServer) Produce(ctx context.Context, req *api.ProduceRequest) (
	*api.ProduceResponse, error) {
	// 認可できるのかの確認
	if err := s.Authorizer.Authorize(
		subject(ctx),
		objectWildcard,
		produceAction,
	); err != nil {
		return nil, err
	}
	offset, err := s.CommitLog.Append(req.Record)
	if err != nil {
		return nil, err
	}
	return &api.ProduceResponse{Offset: offset}, nil
}

// Consume メソッドは指定されたオフセットからログレコードを読み取り、レスポンスとして返します。
// エラーが発生した場合は nil とエラーを返します。
func (s *grpcServer) Consume(ctx context.Context, req *api.ConsumeRequest) (
	*api.ConsumeResponse, error) {
	// 認可できるかの確認
	if err := s.Authorizer.Authorize(
		subject(ctx),
		objectWildcard,
		consumeAction,
	); err != nil {
		return nil, err
	}
	record, err := s.CommitLog.Read(req.Offset)
	if err != nil {
		return nil, err
	}
	return &api.ConsumeResponse{Record: record}, nil
}

// ProduceStream は双方向ストリーミングを実現する RPC メソッドです。リクエストを受信しレスポンスを送信します。
// ストリーム内でエラーが発生した場合、その時点で処理を終了しエラーを返却します。
// 各リクエストは Produce メソッドを呼び出すことで処理されます。
func (s *grpcServer) ProduceStream(
	stream api.Log_ProduceStreamServer,
) error {
	for {
		req, err := stream.Recv()
		if err != nil {
			return err
		}
		res, err := s.Produce(stream.Context(), req)
		if err != nil {
			return err
		}
		if err = stream.Send(res); err != nil {
			return err
		}
	}
}

// ConsumeStream はサーバーストリーミング RPC を処理し、指定されたオフセットのログレコードを継続的に送信します。
// クライアントがストリームを終了させると、処理を終了して nil を返します。
// 無効なオフセットの場合、適切なエラーハンドリングを行い、処理を続行します。
func (s *grpcServer) ConsumeStream(
	req *api.ConsumeRequest,
	stream api.Log_ConsumeStreamServer,
) error {
	for {
		select {
		case <-stream.Context().Done():
			return nil
		default:
			res, err := s.Consume(stream.Context(), req)
			switch err.(type) {
			case nil:
			case api.ErrOffsetOutOfRange:
				continue
			default:
				return err
			}
			if err = stream.Send(res); err != nil {
				return err
			}
			req.Offset++
		}
	}
}

// authenticate は gRPC の認証用インターセプタ関数です。
// コンテキストからクライアント情報を取得し、認証情報に基づいて主題を設定します。
// 必要な認証情報が不足している場合でも、エラーではなく適切な値を設定して処理を継続します。
// 主題情報はコンテキストに保存され、後続の処理で利用されます。
// エラーが発生した場合は context.Context と共にエラーを返却します。
func authenticate(ctx context.Context) (context.Context, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return ctx, status.New(
			codes.Unknown,
			"couldn't find peer info",
		).Err()
	}

	if p.AuthInfo == nil {
		return context.WithValue(ctx, subjectContextKey{}, ""), nil
	}

	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return ctx, status.New(
			codes.Unauthenticated,
			"couldn't find TLS info",
		).Err()
	}
	subject := tlsInfo.State.VerifiedChains[0][0].Subject.CommonName
	ctx = context.WithValue(ctx, subjectContextKey{}, subject)

	return ctx, nil
}

// subject はコンテキストから現在の認証主体 (subject) を取得します。
func subject(ctx context.Context) string {
	v, ok := ctx.Value(subjectContextKey{}).(string)
	if !ok {
		return ""
	}
	return v
}

type subjectContextKey struct{}
