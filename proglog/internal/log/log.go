package log

import (
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"

	api "github.com/ishisaka/go_distribute/proglog/api/v1"
)

// Log はスレッドセーフな永続化ログを管理するための構造体です。
// ディレクトリ内のセグメントを利用してレコードを保存および管理します。
type Log struct {
	mu sync.RWMutex

	Dir    string
	Config Config

	activeSegment *segment
	segments      []*segment
}

// NewLog は新しい永続ログシステムを初期化します。
// 指定したディレクトリと設定を用いてログを作成します。
// セグメントの最大サイズが未設定の場合、デフォルト値が使用されます。
func NewLog(dir string, c Config) (*Log, error) {
	if c.Segment.MaxStoreBytes == 0 {
		c.Segment.MaxStoreBytes = 1024
	}
	if c.Segment.MaxIndexBytes == 0 {
		c.Segment.MaxIndexBytes = 1024
	}
	l := &Log{
		Dir:    dir,
		Config: c,
	}

	return l, l.setup()
}

// setup はログの初期化を行い、既存のセグメントを読み込んで管理対象に設定します。
// セグメントが存在しない場合は新しいセグメントを作成します。
func (l *Log) setup() error {
	files, err := os.ReadDir(l.Dir)
	if err != nil {
		return err
	}
	var baseOffsets []uint64
	for _, file := range files {
		offStr := strings.TrimSuffix(
			file.Name(),
			path.Ext(file.Name()),
		)
		off, _ := strconv.ParseUint(offStr, 10, 0)
		baseOffsets = append(baseOffsets, off)
	}
	sort.Slice(baseOffsets, func(i, j int) bool {
		return baseOffsets[i] < baseOffsets[j]
	})
	for i := 0; i < len(baseOffsets); i++ {
		if err = l.newSegment(baseOffsets[i]); err != nil {
			return err
		}
		// baseOffsetsは、インデックスとストアの二つの重複を含んで
		// いるので、重複しているものをスキップする
		i++
	}
	if l.segments == nil {
		if err = l.newSegment(
			l.Config.Segment.InitialOffset,
		); err != nil {
			return err
		}
	}
	return nil
}

// Append は指定されたレコードを現在のアクティブセグメントに追加し、その記録のオフセットを返します。
// 必要に応じて新しいセグメントを作成し、エラーが発生した場合はそれを返します。
func (l *Log) Append(record *api.Record) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	highestOffset, err := l.highestOffset()
	if err != nil {
		return 0, err
	}

	if l.activeSegment.IsMaxed() {
		err = l.newSegment(highestOffset + 1)
		if err != nil {
			return 0, err
		}
	}

	off, err := l.activeSegment.Append(record)
	if err != nil {
		return 0, err
	}

	return off, err
}

// Read は指定されたオフセットからレコードを読み込みます。
// 該当するセグメントが見つからない場合、エラーを返します。
// メソッドはスレッドセーフであり、読み取りロックを使用します。
func (l *Log) Read(off uint64) (*api.Record, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var s *segment
	for _, segment := range l.segments {
		if segment.baseOffset <= off && off < segment.nextOffset {
			s = segment
			break
		}
	}
	if s == nil || s.nextOffset <= off {
		return nil, api.ErrOffsetOutOfRange{Offset: off}
	}
	return s.Read(off)
}

// Close はログとその内部セグメントをクローズし、必要に応じてリソースを解放します。
// エラーが発生した場合、そのエラーを返します。スレッドセーフです。
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, segment := range l.segments {
		if err := segment.Close(); err != nil {
			return err
		}
	}
	return nil
}

// Remove はログを閉じて参照している全てのリソースを解放し、ディレクトリを削除します。
// エラーが発生した場合はそのエラーを返します。
func (l *Log) Remove() error {
	if err := l.Close(); err != nil {
		return err
	}
	return os.RemoveAll(l.Dir)
}

// Reset はログを初期状態にリセットします。現在のリソースを解放し、新しいセグメント構造をセットアップします。
func (l *Log) Reset() error {
	if err := l.Remove(); err != nil {
		return err
	}
	return l.setup()
}

// LowestOffset はログ内で利用可能な最小のオフセットを返します。スレッドセーフで読み取りロックを使用します。
// エラーが発生した場合はそのエラーを返します。
func (l *Log) LowestOffset() (uint64, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.segments[0].baseOffset, nil
}

// HighestOffset は、現在のログ内で利用可能な最大のオフセットを取得します。スレッドセーフで読み取りロックを使用します。
// エラーが発生した場合、そのエラーを返します。
func (l *Log) HighestOffset() (uint64, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.highestOffset()
}

// highestOffset は、現在のセグメントにおける最大のオフセットを計算して返します。
// セグメントが空の場合は 0 を返します。
func (l *Log) highestOffset() (uint64, error) {
	off := l.segments[len(l.segments)-1].nextOffset
	if off == 0 {
		return 0, nil
	}
	return off - 1, nil
}

// Truncate は指定されたオフセットよりも小さい範囲のログセグメントを削除し、リソースを解放します。
func (l *Log) Truncate(lowest uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var segments []*segment
	for _, s := range l.segments {
		if s.nextOffset <= lowest+1 {
			if err := s.Remove(); err != nil {
				return err
			}
			continue
		}
		segments = append(segments, s)
	}
	l.segments = segments
	return nil
}

// Reader はログ全体を結合した io.Reader を返します。スレッドセーフな読み取り専用ロックを使用します。
func (l *Log) Reader() io.Reader {
	l.mu.RLock()
	defer l.mu.RUnlock()
	readers := make([]io.Reader, len(l.segments))
	for i, segment := range l.segments {
		readers[i] = &originReader{segment.store, 0}
	}
	return io.MultiReader(readers...)
}

// originReader は store を基盤とした構造体で、現在のオフセット位置 off を持ち、データの読み取りをサポートします。
type originReader struct {
	*store
	off int64
}

// Read は originReader の現在のオフセット位置からバイトスライス p にデータを読み込むメソッドです。
// データ読み込み後、オフセットを読み取ったバイト数分進めます。
// 読み取ったバイト数およびエラーを返します。
func (o *originReader) Read(p []byte) (int, error) {
	n, err := o.ReadAt(p, o.off)
	o.off += int64(n)
	return n, err
}

// newSegment は指定されたオフセットを基準に新しいセグメントを作成し、現在のアクティブセグメントとして設定します。
// セグメント作成に失敗した場合はエラーを返します。
func (l *Log) newSegment(off uint64) error {
	s, err := newSegment(l.Dir, off, l.Config)
	if err != nil {
		return err
	}
	l.segments = append(l.segments, s)
	l.activeSegment = s
	return nil
}
