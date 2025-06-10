package server

import (
	"context"

	api "github.com/ishisaka/go_distribute/proglog/api/v1"
	"google.golang.org/grpc"
)

// Config は、サーバーの設定を保持する構造体です。
// CommitLog によるログ操作を管理します。
type Config struct {
	CommitLog CommitLog
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
	Append(*api.Record) (uint64, error)
	Read(uint64) (*api.Record, error)
}

// NewGRPCServer は、指定された設定を使用して新しい gRPC サーバーを作成し、初期化します。
func NewGRPCServer(config *Config) (*grpc.Server, error) {
	gsrv := grpc.NewServer()
	srv, err := newgrpcServer(config)
	if err != nil {
		return nil, err
	}
	api.RegisterLogServer(gsrv, srv)
	return gsrv, nil
}

// newgrpcServer は、新しい gRPC サーバーを作成し、初期化します。
// Config 構造体を受け取り、その設定を使用して grpcServer を生成します。
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
