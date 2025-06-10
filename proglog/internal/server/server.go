package server

import (
	"context"

	api "github.com/ishisaka/go_distribute/proglog/api/v1"
	"google.golang.org/grpc"
)

// Config は gRPC サーバーの設定を保持する構造体です。
// CommitLog はログの追加や読み取り操作を管理します。
type Config struct {
	CommitLog CommitLog
}

var _ api.LogServer = (*grpcServer)(nil)

// grpcServer は gRPC サーバーの実装を提供する構造体です。
// api.UnimplementedLogServer を埋め込むことで将来的な互換性を保証します。
// Config を利用してサーバーの挙動を設定します。
type grpcServer struct {
	api.UnimplementedLogServer
	*Config
}

// CommitLog はログにレコードを追加および取得するためのインターフェースです。
// Append メソッドは新しいレコードを追加し、そのオフセットを返します。
// Read メソッドは指定されたオフセットのレコードを取得します。
type CommitLog interface {
	Append(*api.Record) (uint64, error)
	Read(uint64) (*api.Record, error)
}

// NewGRPCServer は gRPC サーバーを初期化して返す関数です。
// 指定された設定に基づいて新しい gRPC サーバーを作成します。
// サーバーの登録時にエラーが発生した場合は、そのエラーを返します。
func NewGRPCServer(config *Config) (*grpc.Server, error) {
	gsrv := grpc.NewServer()
	srv, err := newgrpcServer(config)
	if err != nil {
		return nil, err
	}
	api.RegisterLogServer(gsrv, srv)
	return gsrv, nil
}

// newgrpcServer 関数は指定された Config を使用して新しい grpcServer インスタンスを初期化して返します。
// 実行中に発生したエラーも同時に返します。
func newgrpcServer(config *Config) (srv *grpcServer, err error) {
	srv = &grpcServer{
		Config: config,
	}
	return srv, nil
}

// Produce はクライアントから受け取ったレコードをログに追加し、そのオフセットを含むレスポンスを返します。
// コンテキストとリクエストを受け取り、レスポンスまたはエラーを返します。
func (s *grpcServer) Produce(ctx context.Context, req *api.ProduceRequest) (
	*api.ProduceResponse, error) {
	offset, err := s.CommitLog.Append(req.Record)
	if err != nil {
		return nil, err
	}
	return &api.ProduceResponse{Offset: offset}, nil
}

// Consume は指定されたオフセットからレコードを読み取るリクエストを処理し、そのレコードをレスポンスとして返します。
// コンテキストと ConsumeRequest を受け取り、ConsumeResponse またはエラーを返します。
// オフセットが無効な場合、エラーを返します。
func (s *grpcServer) Consume(ctx context.Context, req *api.ConsumeRequest) (
	*api.ConsumeResponse, error) {
	record, err := s.CommitLog.Read(req.Offset)
	if err != nil {
		return nil, err
	}
	return &api.ConsumeResponse{Record: record}, nil
}

// ProduceStream はクライアントとの双方向ストリーミングRPCを処理し、リクエストを受け取りレスポンスを送信します。
// クライアントから受信したリクエストを処理し、ログに追加して応答レスポンスを送信します。
// ストリームが終了するかエラーが発生した場合に処理を停止してエラーを返します。
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

// ConsumeStream はクライアントからのリクエストを受け取り、ストリームでレコードのレスポンスを連続的に送信します。
// ストリームのコンテキストが終了するか、エラーが発生した場合に処理を終了します。
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

