package server


import (
	"fmt"
	"sync"
)

// Log はログを表します。
type Log struct {
	mu      sync.Mutex
	records []Record
}

// NewLog は新しいLog構造体のインスタンスを初期化して返します。
// この関数は空のレコードスライスを含むLogを生成します。
func NewLog() *Log {
	return &Log{}
}

// Append は新しいレコードをログに追加し、そのレコードのオフセットを返します。
// レコードがエラーなく追加される場合、nil を返します。
// この操作はスレッドセーフです。
func (c *Log) Append(record Record) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	record.Offset = uint64(len(c.records))
	c.records = append(c.records, record)
	return record.Offset, nil
}

// Read は指定されたオフセットのレコードを取得します。
// 有効なオフセットの場合はレコードを返し、無効な場合は ErrOffsetNotFound を返します。
// この操作はスレッドセーフです。
func (c *Log) Read(offset uint64) (Record, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if offset >= uint64(len(c.records)) {
		return Record{}, ErrOffsetNotFound
	}
	return c.records[offset], nil
}

// Record はログ内の個別レコードを表します。
// Value はレコードのデータを格納するバイトスライスです。
// Offset はレコードの位置を一意に識別するためのオフセット値です。
type Record struct {
	Value  []byte `json:"value"`
	Offset uint64 `json:"offset"`
}

// ErrOffsetNotFound は、指定されたオフセットがログ内に存在しない場合に返されます。
var ErrOffsetNotFound = fmt.Errorf("offset not found")