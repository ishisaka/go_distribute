package server

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net"
	"os"
	"testing"

	api "github.com/ishisaka/go_distribute/proglog/api/v1"
	"github.com/ishisaka/go_distribute/proglog/internal/log"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestServer はサーバーの動作を異なるシナリオでテストするための関数です。
// 各シナリオごとに対応するテスト関数が実行されます。
// テストごとに設定を初期化し、後処理を行います。
func TestServer(t *testing.T) {
	for scenario, fn := range map[string]func(
		t *testing.T,
		client api.LogClient,
		config *Config,
	){
		"produce/consume a message to/from the log succeeds": testProduceConsume,
		"produce/consume stream succeeds":                    testProduceConsumeStream,
		"consume past log boundary fails":                    testConsumePastBoundary,
	} {
		t.Run(scenario, func(t *testing.T) {
			client, config, teardown := setupTest(t, nil)
			defer teardown()
			fn(t, client, config)
		})
	}
}

// setupTest はテスト環境を準備します。
// テスト用の gRPC サーバーとクライアント、設定、クリーンアップ関数を返します。
// 必要に応じて設定をカスタマイズするためのコールバック関数を受け取ります。
// 引数:
//
//	t testing.Tのインスタンス
//	fn Configを呼び出す関数
//
// 戻り値:
//
//	client gROCクライアントのインスタンス
//	cfg Configのインスタンス
//	teardown あと処理用のクロージャ
func setupTest(t *testing.T, fn func(*Config)) (
	client api.LogClient,
	cfg *Config,
	teardown func(),
) {
	t.Helper()

	// リスナーの作成
	l, err := net.Listen("tcp", ":0")
	require.NoError(t, err)

	// gRPCの接続設定
	clientOptions := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials())}
	// gRPCクライアントを作成
	// 古い（非推奨）: cc, err := grpc.Dial(l.Addr().String(), clientOptions...)
	cc, err := grpc.NewClient(l.Addr().String(), clientOptions...)
	require.NoError(t, err)

	// log（ロジック）の作成
	dir, err := os.MkdirTemp("", "server-test")
	require.NoError(t, err)

	clog, err := log.NewLog(dir, log.Config{})
	require.NoError(t, err)

	// 作ったlogをgRPC ServerのConfigに設定
	cfg = &Config{
		CommitLog: clog,
	}
	if fn != nil {
		fn(cfg)
	}
	// gRPC Serverの作成
	server, err := NewGRPCServer(cfg)
	require.NoError(t, err)
	// goルーチンでサーバーを動かす
	go func() {
		_ = server.Serve(l)
	}()

	//Logサービスのクライアントの作成
	client = api.NewLogClient(cc)

	// クライアントとConfig, 後処理のクロージャーを返す
	return client, cfg, func() {
		_ = cc.Close()
		server.Stop()
		_ = l.Close()
		_ = clog.Remove()
	}
}

// testProduceConsume はメッセージの生成と消費をテストします。
// クライアントを使用してログにメッセージを生成し、その後消費することで正しい動作を検証します。
// テストデータは Produce と Consume の過程で正確に処理されることを確認します。
func testProduceConsume(t *testing.T, client api.LogClient, _ *Config) {
	ctx := context.Background()

	want := &api.Record{
		Value: []byte("hello world"),
	}

	produce, err := client.Produce(
		ctx,
		&api.ProduceRequest{
			Record: want,
		},
	)
	require.NoError(t, err)
	want.Offset = produce.Offset

	consume, err := client.Consume(ctx, &api.ConsumeRequest{
		Offset: produce.Offset,
	})
	require.NoError(t, err)
	require.Equal(t, want.Value, consume.Record.Value)
	require.Equal(t, want.Offset, consume.Record.Offset)
}

// testConsumePastBoundary はログ境界を越えた取得操作が失敗することをテストする関数です。
func testConsumePastBoundary(
	t *testing.T,
	client api.LogClient,
	_ *Config,
) {
	ctx := context.Background()

	produce, err := client.Produce(ctx, &api.ProduceRequest{
		Record: &api.Record{
			Value: []byte("hello world"),
		},
	})
	require.NoError(t, err)

	consume, err := client.Consume(ctx, &api.ConsumeRequest{
		Offset: produce.Offset + 1,
	})
	if consume != nil {
		t.Fatal("consume not nil")
	}
	got := status.Code(err)
	want := codes.OutOfRange
	if got != want {
		t.Fatalf("got err: %v, want: %v", got, want)
	}
}

// testProduceConsumeStream は、クライアントでのストリーム型プロデューサーとコンシューマーの動作をテストします。
// クライアントがメッセージのストリームを正常に送受信するかを確認します。
// テストでは、複数のメッセージの送信やオフセットの照合を行います。
func testProduceConsumeStream(
	t *testing.T,
	client api.LogClient,
	_ *Config,
) {
	ctx := context.Background()

	records := []*api.Record{{
		Value:  []byte("first message"),
		Offset: 0,
	}, {
		Value:  []byte("second message"),
		Offset: 1,
	}}

	{
		stream, err := client.ProduceStream(ctx)
		require.NoError(t, err)

		for offset, record := range records {
			err = stream.Send(&api.ProduceRequest{
				Record: record,
			})
			require.NoError(t, err)
			res, err := stream.Recv()
			require.NoError(t, err)
			if res.Offset != uint64(offset) {
				t.Fatalf(
					"got offset: %d, want: %d",
					res.Offset,
					offset,
				)
			}
		}

	}

	{
		stream, err := client.ConsumeStream(
			ctx,
			&api.ConsumeRequest{Offset: 0},
		)
		require.NoError(t, err)

		for i, record := range records {
			res, err := stream.Recv()
			require.NoError(t, err)
			require.Equal(t, res.Record, &api.Record{
				Value:  record.Value,
				Offset: uint64(i),
			})
		}
	}
}
