package discovery

import (
	"net"

	"go.uber.org/zap"

	"github.com/hashicorp/serf/serf"
)

// Membership はクラスタ内のノード管理を行う構造体です。
// ノードの参加・離脱やイベント処理をサポートします。
type Membership struct {
	Config
	handler Handler
	serf    *serf.Serf
	events  chan serf.Event
	logger  *zap.Logger
}

// New は Membership 構造体の新しいインスタンスを作成し、初期設定を行います。
// handler はクラスタのイベントを管理するためのインターフェースです。
// config はクラスタの設定を提供する構造体です。
// 初期化中にエラーが発生した場合は nil とエラーを返します。
func New(handler Handler, config Config) (*Membership, error) {
	c := &Membership{
		Config:  config,
		handler: handler,
		logger:  zap.L().Named("membership"),
	}
	if err := c.setupSerf(); err != nil {
		return nil, err
	}
	return c, nil
}

// Config はクラスタの基本設定を提供する構造体です。
// NodeName はノードの一意な識別子を設定します。
// BindAddr はノードがバインドするアドレスを指定します。
// Tags はノードのメタデータを保持するマップです。
// StartJoinAddrs はクラスタ参加時に接続する初期アドレス一覧を指定します。
type Config struct {
	NodeName       string
	BindAddr       string
	Tags           map[string]string
	StartJoinAddrs []string
}

// setupSerf は Serf インスタンスを初期化し、クラスタイベントを処理する準備を行います。
// ネットワークアドレスの解決、タグ設定、イベントチャネルの作成を行います。
// エラーが発生した場合は、そのエラーを返します。
func (m *Membership) setupSerf() (err error) {
	addr, err := net.ResolveTCPAddr("tcp", m.BindAddr)
	if err != nil {
		return err
	}
	config := serf.DefaultConfig()
	config.Init()
	config.MemberlistConfig.BindAddr = addr.IP.String()
	config.MemberlistConfig.BindPort = addr.Port
	m.events = make(chan serf.Event)
	config.EventCh = m.events
	config.Tags = m.Tags
	config.NodeName = m.NodeName
	m.serf, err = serf.Create(config)
	if err != nil {
		return err
	}
	go m.eventHandler()
	if m.StartJoinAddrs != nil {
		_, err = m.serf.Join(m.StartJoinAddrs, true)
		if err != nil {
			return err
		}
	}
	return nil
}

// Handler はクラスタ内のノードイベントを処理するためのインターフェースです。
// Join は指定されたノードの参加処理を行い、エラーがある場合は返します。
// Leave は指定されたノードの離脱処理を行い、エラーがある場合は返します。
type Handler interface {
	Join(name, addr string) error
	Leave(name string) error
}

// eventHandler は、Serf から発生するイベントを処理するためのメソッドです。
// メンバーの参加、離脱、障害発生イベントを監視・対応します。
func (m *Membership) eventHandler() {
	for e := range m.events {
		switch e.EventType() {
		case serf.EventMemberJoin:
			for _, member := range e.(serf.MemberEvent).Members {
				if m.isLocal(member) {
					continue
				}
				m.handleJoin(member)
			}
		case serf.EventMemberLeave, serf.EventMemberFailed:
			for _, member := range e.(serf.MemberEvent).Members {
				if m.isLocal(member) {
					return
				}
				m.handleLeave(member)
			}
		default:
			m.logger.Warn("unknown event", zap.String("event", e.EventType().String()))
		}
	}
}

// handleJoin は、新しいメンバーがクラスタに参加した際の処理を行います。
// メンバーの情報をハンドラーを通じて登録します。
// エラーが発生した場合は記録します。
func (m *Membership) handleJoin(member serf.Member) {
	if err := m.handler.Join(
		member.Name,
		member.Tags["rpc_addr"],
	); err != nil {
		m.logError(err, "failed to join", member)
	}
}

// handleLeave は、メンバーがクラスタから離脱する際の処理を行います。
// メンバー名をハンドラーを通じて削除し、失敗した場合はエラーを記録します。
func (m *Membership) handleLeave(member serf.Member) {
	if err := m.handler.Leave(
		member.Name,
	); err != nil {
		m.logError(err, "failed to leave", member)
	}
}

// isLocal は、指定されたメンバーがローカルノードであるかを判定します。
// Serf のローカルノード名と指定メンバー名を比較して一致を確認します。
func (m *Membership) isLocal(member serf.Member) bool {
	return m.serf.LocalMember().Name == member.Name
}

// Members は、クラスタ内の現在のメンバー一覧を取得します。
// Serfの管理するメンバー情報を返します。
func (m *Membership) Members() []serf.Member {
	return m.serf.Members()
}

// Leave は、現在のノードをクラスタから離脱させる処理を行います。エラーが発生した場合はそのエラーを返します。
func (m *Membership) Leave() error {
	return m.serf.Leave()
}

// logError はエラー発生時に、エラーメッセージと関連情報をログに記録します。
// err は発生したエラーを指定します。
// msg はログに表示するメッセージを指定します。
// member は関連するメンバー情報を指定します。
func (m *Membership) logError(err error, msg string, member serf.Member) {
	m.logger.Error(
		msg,
		zap.Error(err),
		zap.String("name", member.Name),
		zap.String("rpc_addr", member.Tags["rpc_addr"]),
	)
}
