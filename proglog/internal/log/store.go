package log

import (
	"bufio"
	"encoding/binary"
	"os"
	"sync"
)

var (
	enc = binary.BigEndian
)

const (
	lenWidth = 8
)

// store はファイル操作を扱うための構造体です。
// os.File を埋め込み、排他制御とバッファリング機能を提供します。
// size フィールドでファイルサイズを管理します。
type store struct {
	*os.File
	mu   sync.Mutex
	buf  *bufio.Writer
	size uint64
}

// newStore は指定された os.File を元に store 構造体を初期化して返します。
// ファイルのサイズ取得に失敗した場合はエラーを返します。
func newStore(f *os.File) (*store, error) {
	fi, err := os.Stat(f.Name())
	if err != nil {
		return nil, err
	}
	size := uint64(fi.Size())
	return &store{
		File: f,
		size: size,
		buf:  bufio.NewWriter(f),
	}, nil
}

// Append はデータ p をバッファに書き込み、書き込んだバイト数、開始位置、およびエラーを返します。
func (s *store) Append(p []byte) (n uint64, pos uint64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pos = s.size
	if err := binary.Write(s.buf, enc, uint64(len(p))); err != nil {
		return 0, 0, err
	}
	w, err := s.buf.Write(p)
	if err != nil {
		return 0, 0, err
	}
	w += lenWidth
	s.size += uint64(w)
	return uint64(w), pos, nil
}

// Read は指定された位置 pos からデータを読み出し、デコードしたバイトスライスとエラーを返します。
// ファイルロックとバッファフラッシュを行い、整合性を確保します。
// エラーが発生した場合は空のスライスとエラーを返します。
func (s *store) Read(pos uint64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.buf.Flush(); err != nil {
		return nil, err
	}
	size := make([]byte, lenWidth)
	if _, err := s.File.ReadAt(size, int64(pos)); err != nil {
		return nil, err
	}
	b := make([]byte, enc.Uint64(size))
	if _, err := s.File.ReadAt(b, int64(pos+lenWidth)); err != nil {
		return nil, err
	}
	return b, nil
}

// ReadAt は指定されたオフセット off からバイトスライス p にデータを読み込み、読み取ったバイト数とエラーを返します。
// 排他制御とバッファフラッシュを行い、データ整合性を確保します。
func (s *store) ReadAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.buf.Flush(); err != nil {
		return 0, err
	}
	return s.File.ReadAt(p, off)
}

// Close は、バッファをフラッシュし、基となるファイルをクローズします。エラーが発生した場合はそれを返します。
func (s *store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.buf.Flush()
	if err != nil {
		return err
	}
	return s.File.Close()
}