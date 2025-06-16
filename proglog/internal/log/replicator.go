package log

import (
	"context"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	api "github.com/ishisaka/go_distribute/proglog/api/v1"
)

// Replicator は分散システムのレプリケーションを管理する型です。
// gRPC を使用してデータのプロデュースおよび消費を行います。
// サーバの追加・削除やレプリケーションの開始・停止を管理します。
type Replicator struct {
	DialOptions []grpc.DialOption
	LocalServer api.LogClient

	logger *zap.Logger

	mu      sync.Mutex
	servers map[string]chan struct{}
	closed  bool
	close   chan struct{}
}

// Join は新しいサーバをレプリケーション対象に追加します。name はサーバ名、addr はサーバアドレスを指定します。
// サーバが閉じた状態や既に追加済みの場合は何も処理せずに終了します。
func (r *Replicator) Join(name, addr string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()

	if r.closed {
		return nil
	}

	if _, ok := r.servers[name]; ok {
		// すでにレプリケーションを行っているのでスキップ
		return nil
	}
	r.servers[name] = make(chan struct{})

	go r.replicate(addr, r.servers[name])

	return nil
}

// replicate は指定されたアドレスのサーバと gRPC 接続を確立し、レプリケーションを実行するメソッドです。
// 他のサーバからストリーム形式でレコードを受信し、それをローカルサーバへ保存します。
// close または leave チャネルが受信されると処理を停止します。
func (r *Replicator) replicate(addr string, leave chan struct{}) {
	cc, err := grpc.NewClient(addr, r.DialOptions...)
	if err != nil {
		r.logError(err, "failed to dial", addr)
		return
	}
	defer func() { _ = cc.Close() }()

	client := api.NewLogClient(cc)

	ctx := context.Background()
	stream, err := client.ConsumeStream(ctx,
		&api.ConsumeRequest{
			Offset: 0,
		},
	)
	if err != nil {
		r.logError(err, "failed to consume", addr)
		return
	}

	records := make(chan *api.Record)
	go func() {
		for {
			recv, err := stream.Recv()
			if err != nil {
				r.logError(err, "failed to receive", addr)
				return
			}
			records <- recv.Record
		}
	}()
	for {
		select {
		case <-r.close:
			return
		case <-leave:
			return
		case record := <-records:
			_, err = r.LocalServer.Produce(ctx,
				&api.ProduceRequest{
					Record: record,
				},
			)
			if err != nil {
				r.logError(err, "failed to produce", addr)
				return
			}
		}
	}
}

// Leave は指定された名前のサーバをレプリケーション対象から削除します。
// サーバが存在しない場合は何もせずに終了します。
func (r *Replicator) Leave(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	if _, ok := r.servers[name]; !ok {
		return nil
	}
	close(r.servers[name])
	delete(r.servers, name)
	return nil
}

// init は Replicator 構造体の初期化を行います。内部フィールドが未初期化の場合に初期値を設定します。
func (r *Replicator) init() {
	if r.logger == nil {
		r.logger = zap.L().Named("replicator")
	}
	if r.servers == nil {
		r.servers = make(map[string]chan struct{})
	}
	if r.close == nil {
		r.close = make(chan struct{})
	}
}

// Close は Replicator を閉じるメソッドです。内部リソースを解放し、今後の操作を無効化します。
// 閉じた状態で再度呼び出してもエラーは返されません。
func (r *Replicator) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()

	if r.closed {
		return nil
	}
	r.closed = true
	close(r.close)
	return nil
}

// logError はエラーログを記録するためのメソッドです。エラーメッセージ、アドレス、およびエラー内容を出力します。
func (r *Replicator) logError(err error, msg, addr string) {
	r.logger.Error(
		msg,
		zap.String("addr", addr),
		zap.Error(err),
	)
}
