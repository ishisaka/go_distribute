package log

import (
	"fmt"
	"os"
	"path/filepath"

	api "github.com/ishisaka/go_distribute/StructureDataWithProtobuf/api/v1"
	"google.golang.org/protobuf/proto"
)

// segment は永続的なログセグメントを表す構造体です。
// データ保存用の store とインデックス管理用の index を内部に持ちます。
// baseOffset はセグメントの開始オフセットを示し、nextOffset は次に書き込むオフセットを示します。
// config はセグメントに関連する設定を保持します。
type segment struct {
	store                  *store
	index                  *index
	baseOffset, nextOffset uint64
	config                 Config
}

// newSegment は新しいログセグメントを作成し、初期化された segment 構造体ポインタを返します。
// ディレクトリパス、基準オフセット、設定値を引数に取り、必要なストアおよびインデックスファイルを生成します。
// ストアおよびインデックスの初期化に失敗した場合はエラーを返します。
// また、インデックスの現在の状態に基づき、次に書き込むべきオフセットを設定します。
func newSegment(dir string, baseOffset uint64, c Config) (*segment, error) {
	s := &segment{
		baseOffset: baseOffset,
		config:     c,
	}
	storeFile, err := os.OpenFile(
		filepath.Join(dir, fmt.Sprintf("%d%s", baseOffset, ".store")),
		os.O_RDWR|os.O_CREATE|os.O_APPEND,
		0600,
	)
	if err != nil {
		return nil, err
	}
	if s.store, err = newStore(storeFile); err != nil {
		return nil, err
	}
	indexFile, err := os.OpenFile(
		filepath.Join(dir, fmt.Sprintf("%d%s", baseOffset, ".index")),
		os.O_RDWR|os.O_CREATE,
		0600,
	)
	if err != nil {
		return nil, err
	}
	if s.index, err = newIndex(indexFile, c); err != nil {
		return nil, err
	}
	if off, _, err := s.index.Read(-1); err != nil {
		s.nextOffset = baseOffset
	} else {
		s.nextOffset = baseOffset + uint64(off) + 1
	}
	return s, nil
}

// Append はレコードをセグメントに追加し、そのオフセットとエラーを返します。
func (s *segment) Append(record *api.Record) (offset uint64, err error) {
	cur := s.nextOffset
	record.Offset = cur
	p, err := proto.Marshal(record)
	if err != nil {
		return 0, err
	}
	_, pos, err := s.store.Append(p)
	if err != nil {
		return 0, err
	}
	if err = s.index.Write(
		// インデックスのオフセットは、ベースオフセットからの相対
		uint32(s.nextOffset-uint64(s.baseOffset)),
		pos,
	); err != nil {
		return 0, err
	}
	s.nextOffset++
	return cur, nil
}

// Read は指定されたオフセットのレコードをセグメントから読み取り、レコードとエラーを返します。
func (s *segment) Read(off uint64) (*api.Record, error) {
	_, pos, err := s.index.Read(int64(off - s.baseOffset))
	if err != nil {
		return nil, err
	}
	p, err := s.store.Read(pos)
	if err != nil {
		return nil, err
	}
	record := &api.Record{}
	err = proto.Unmarshal(p, record)
	return record, err
}

// IsMaxed は、セグメントの保存容量またはインデックス容量が設定された上限に達しているかを判定します。
func (s *segment) IsMaxed() bool {
	return s.store.size >= s.config.Segment.MaxStoreBytes ||
		s.index.size >= s.config.Segment.MaxIndexBytes ||
		s.index.isMaxed()
}

// Remove はセグメントを削除します。内部のリソースを閉じた後、関連するファイルを削除します。エラーを返す場合があります。
func (s *segment) Remove() error {
	if err := s.Close(); err != nil {
		return err
	}
	if err := os.Remove(s.index.Name()); err != nil {
		return err
	}
	if err := os.Remove(s.store.Name()); err != nil {
		return err
	}
	return nil
}

// Close は、セグメント内のリソースである index と store を閉じる処理を行います。エラーがあれば最初に遭遇したものを返します。
func (s *segment) Close() error {
	if err := s.index.Close(); err != nil {
		return err
	}
	if err := s.store.Close(); err != nil {
		return err
	}
	return nil
}
