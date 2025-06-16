package discovery

import (
	"fmt"
	"testing"
	"time"

	"github.com/hashicorp/serf/serf"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
)

// TestMembership は、クラスタのメンバー参加および離脱処理が正しく機能するかを確認するテストです。
// メンバーリストのサイズやイベントハンドラーの状態を検証します。
func TestMembership(t *testing.T) {
	m, handler := setupMember(t, nil)
	m, _ = setupMember(t, m)
	m, _ = setupMember(t, m)

	require.Eventually(t, func() bool {
		return len(handler.joins) == 2 &&
			len(m[0].Members()) == 3 &&
			len(handler.leaves) == 0
	}, 3*time.Second, 250*time.Millisecond)

	require.NoError(t, m[2].Leave())

	require.Eventually(t, func() bool {
		return len(handler.joins) == 2 &&
			len(m[0].Members()) == 3 &&
			m[0].Members()[2].Status == serf.StatusLeft &&
			len(handler.leaves) == 1
	}, 3*time.Second, 250*time.Millisecond)

	require.Equal(t, "2", <-handler.leaves)
}

// setupMember は、新規メンバーの設定およびクラスタ管理の準備を行うテスト補助関数です。
// テスト用にメンバーリストを拡張し、ハンドラーを初期化して返します。
// 初回メンバーの場合はクラスタ作成のための初期設定を行います。
func setupMember(t *testing.T, members []*Membership) (
	[]*Membership, *handler,
) {
	id := len(members)
	ports := dynaport.Get(1)
	addr := fmt.Sprintf("%s:%d", "127.0.0.1", ports[0])
	tags := map[string]string{
		"rpc_addr": addr,
	}
	c := Config{
		NodeName: fmt.Sprintf("%d", id),
		BindAddr: addr,
		Tags:     tags,
	}
	h := &handler{}
	if len(members) == 0 {
		h.joins = make(chan map[string]string, 3)
		h.leaves = make(chan string, 3)
	} else {
		c.StartJoinAddrs = []string{
			members[0].BindAddr,
		}
	}
	m, err := New(h, c)
	require.NoError(t, err)
	members = append(members, m)
	return members, h
}

// handler は、メンバーの参加および離脱イベントを処理するための構造体です。
// joins は新しい参加メンバーの情報を管理するためのチャネルです。
// leaves は離脱したメンバーの識別子を管理するためのチャネルです。
type handler struct {
	joins  chan map[string]string
	leaves chan string
}

// Join は新しいメンバーのIDとアドレスを指定して参加情報を送信します。
// joins チャネルが存在する場合にのみ動作します。
// id はメンバーの識別子を示す文字列です。
// addr は参加メンバーのアドレスを示す文字列です。
// エラーが発生しない場合は nil を返します。
func (h *handler) Join(id, addr string) error {
	if h.joins != nil {
		h.joins <- map[string]string{
			"id":   id,
			"addr": addr,
		}
	}
	return nil
}

// Leave はメンバーの離脱を通知するために指定されたIDを leaves チャネルに送信します。
// id は離脱するメンバーを識別する文字列です。
// leaves チャネルが存在する場合にのみ動作します。
// エラーが発生しない場合は nil を返します。
func (h *handler) Leave(id string) error {
	if h.leaves != nil {
		h.leaves <- id
	}
	return nil
}
