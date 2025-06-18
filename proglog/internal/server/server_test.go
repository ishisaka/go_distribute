package server

import (
	"flag"
	"net"
	"os"
	"testing"
	"time"

	"context"
	"github.com/ishisaka/go_distribute/proglog/internal/auth"
	"go.opencensus.io/examples/exporter"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	api "github.com/ishisaka/go_distribute/proglog/api/v1"
	"github.com/ishisaka/go_distribute/proglog/internal/config"
	"github.com/ishisaka/go_distribute/proglog/internal/log"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// debug は観測可能性を有効にしてデバッグ出力を提供するためのフラグです。
// デフォルト値は false で、`-debug` フラグで有効化できます。
var debug = flag.Bool("debug", false, "Enable observability for debugging.")

// TestMain はテストのエントリーポイントとなる関数です。テスト実行前にフラグやロガーの初期化を行います。
func TestMain(m *testing.M) {
	flag.Parse()
	if *debug {
		logger, err := zap.NewDevelopment()
		if err != nil {
			panic(err)
		}
		zap.ReplaceGlobals(logger)
	}
	os.Exit(m.Run())
}

// TestServer はサーバーのテストを実行し、複数のシナリオで挙動を検証します。
func TestServer(t *testing.T) {
	for scenario, fn := range map[string]func(
		t *testing.T,
		rootClient api.LogClient,
		nobodyClient api.LogClient,
		config *Config, // nolint:all
	){
		"produce/consume a message to/from the log succeeeds": testProduceConsume,
		"produce/consume stream succeeds":                     testProduceConsumeStream,
		"consume past log boundary fails":                     testConsumePastBoundary,
		"unauthorized fails":                                  testUnauthorized,
	} {
		t.Run(scenario, func(t *testing.T) {
			rootClient,
				nobodyClient,
				config1,
				teardown := setupTest(t, nil)
			defer teardown()
			fn(t, rootClient, nobodyClient, config1)
		})
	}
}

// setupTest は gRPC サーバーのテストをセットアップする関数です。テスト用クライアントとサーバーを初期化します。
// t はテストのコンテキストです。
// fn は Config を変更可能なカスタム関数です。
// rootClient と nobodyClient はそれぞれのクライアントを返します。
// cfg はサーバー構成を返します。
// teardown はサーバーやリソースを解放する関数を返します。
func setupTest(t *testing.T, fn func(*Config)) (
	rootClient api.LogClient,
	nobodyClient api.LogClient,
	cfg *Config,
	teardown func(),
) {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	newClient := func(certPath, keyPath string) (
		*grpc.ClientConn,
		api.LogClient,
		[]grpc.DialOption,
	) {
		tlsConfig, err := config.SetupTLSConfig(config.TLSConfig{
			CertFile: certPath,
			KeyFile:  keyPath,
			CAFile:   config.CAFile,
			Server:   false,
		})
		require.NoError(t, err)
		tlsCreds := credentials.NewTLS(tlsConfig)
		opts := []grpc.DialOption{grpc.WithTransportCredentials(tlsCreds)}
		conn, err := grpc.NewClient(l.Addr().String(), opts...)
		require.NoError(t, err)
		client := api.NewLogClient(conn)
		return conn, client, opts
	}

	var rootConn *grpc.ClientConn
	rootConn, rootClient, _ = newClient(
		config.RootClientCertFile,
		config.RootClientKeyFile,
	)

	var nobodyConn *grpc.ClientConn
	nobodyConn, nobodyClient, _ = newClient(
		config.NobodyClientCertFile,
		config.NobodyClientKeyFile,
	)
	serverTLSConfig, err := config.SetupTLSConfig(config.TLSConfig{
		CertFile:      config.ServerCertFile,
		KeyFile:       config.ServerKeyFile,
		CAFile:        config.CAFile,
		ServerAddress: l.Addr().String(),
		Server:        true,
	})
	require.NoError(t, err)
	serverCreds := credentials.NewTLS(serverTLSConfig)

	dir, err := os.MkdirTemp("", "server-test")
	require.NoError(t, err)

	clog, err := log.NewLog(dir, log.Config{})
	require.NoError(t, err)

	authorizer := auth.New(config.ACLModelFile, config.ACLPolicyFile)
	var telemetryExporter *exporter.LogExporter
	if *debug {
		metricsLogFile, err := os.CreateTemp("", "metrics-*.log")
		require.NoError(t, err)
		t.Logf("metrics log file: %s", metricsLogFile.Name())

		tracesLogFile, err := os.CreateTemp("", "traces-*.log")
		require.NoError(t, err)
		t.Logf("traces log file: %s", tracesLogFile.Name())

		telemetryExporter, err = exporter.NewLogExporter(exporter.Options{
			MetricsLogFile:    metricsLogFile.Name(),
			TracesLogFile:     tracesLogFile.Name(),
			ReportingInterval: time.Second,
		})
		require.NoError(t, err)
		err = telemetryExporter.Start()
		require.NoError(t, err)
	}

	cfg = &Config{
		CommitLog:  clog,
		Authorizer: authorizer,
	}
	if fn != nil {
		fn(cfg)
	}
	server, err := NewGRPCServer(cfg, grpc.Creds(serverCreds))
	require.NoError(t, err)

	go func() {
		_ = server.Serve(l)
	}()

	return rootClient, nobodyClient, cfg, func() {
		_ = rootConn.Close()
		_ = nobodyConn.Close()
		server.Stop()
		_ = l.Close()
		// nolint:all
		if telemetryExporter != nil {
			time.Sleep(1500 * time.Millisecond)
			telemetryExporter.Stop()
			telemetryExporter.Close()
		}
	}
}

// testProduceConsume はログクライアントを使用してメッセージの書き込みと読み込みをテストします。
// メッセージが正しいオフセットと値でプロデュース・コンシュームされることを確認します。
func testProduceConsume(t *testing.T, client, _ api.LogClient, _ *Config) {
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

// testConsumePastBoundary 関数は、ログの境界外のオフセットを消費する際にエラーが発生することをテストします。
// Produce リクエストでログにメッセージを追加し、Consume リクエストで存在しないオフセットを指定して検証します。
// エラーコードが codes.OutOfRange であることを確認します。
func testConsumePastBoundary(
	t *testing.T,
	client, _ api.LogClient,
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

// testProduceConsumeStream は ProduceStream および ConsumeStream API の動作をテストする関数です。
// gRPC サーバーを通じたストリーミングの生成と消費が正しく行われることを検証します。
// ProduceStream を使用してメッセージを送信し、期待するオフセットが返されることを確認します。
// ConsumeStream を使用してストリームからメッセージを受信し、予想されるデータと一致することを確認します。
// テスト結果は gRPC ストリーミング操作の成功と失敗を詳細に示します。
func testProduceConsumeStream(
	t *testing.T,
	client, _ api.LogClient,
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

// testUnauthorized は権限のないクライアントによる Produce や Consume の操作が拒否されることを検証します。
// Produce 操作のレスポンスが nil であり、エラーコードが PermissionDenied であるかを確認します。
// Consume 操作のレスポンスが nil であり、エラーコードが PermissionDenied であるかを確認します。
func testUnauthorized(
	t *testing.T,
	_,
	client api.LogClient,
	_ *Config,
) {
	ctx := context.Background()
	produce, err := client.Produce(ctx,
		&api.ProduceRequest{
			Record: &api.Record{
				Value: []byte("hello world"),
			},
		},
	)
	if produce != nil {
		t.Fatalf("produce response should be nil")
	}
	gotCode, wantCode := status.Code(err), codes.PermissionDenied
	if gotCode != wantCode {
		t.Fatalf("got code: %d, want: %d", gotCode, wantCode)
	}
	consume, err := client.Consume(ctx, &api.ConsumeRequest{
		Offset: 0,
	})
	if consume != nil {
		t.Fatalf("consume response should be nil")
	}
	gotCode, wantCode = status.Code(err), codes.PermissionDenied
	if gotCode != wantCode {
		t.Fatalf("got code: %d, want: %d", gotCode, wantCode)
	}
}
