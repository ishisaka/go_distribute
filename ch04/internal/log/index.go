package log

import (
	"io"
	"os"

	"github.com/tysonmote/gommap"
)

const (
	offWidth uint64 = 4
	posWidth uint64 = 8
	entWidth        = offWidth + posWidth
)

// index はファイルを使用したメモリマッピングとそのサイズを管理する構造体です。
type index struct {
	file *os.File
	mmap gommap.MMap
	size uint64
}

// newIndex は、新しいindexを初期化し、指定されたファイルを使用してマッピングされたメモリ領域を作成します。
// 引数fは操作対象のos.Fileポインタを、cは設定情報を受け取ります。
// エラーが発生する可能性があるため、構造体ポインタとエラー値を返します。
// ファイルサイズを取得し、指定されたバイトサイズにtruncate処理を行います。
// gommapを使用してメモリマッピングを作成し、読み書き・共有属性を設定します。
func newIndex(f *os.File, c Config) (*index, error) {
	idx := &index{
		file: f,
	}
	fi, err := os.Stat(f.Name())
	if err != nil {
		return nil, err
	}
	idx.size = uint64(fi.Size())
	if err = os.Truncate(
		f.Name(), int64(c.Segment.MaxIndexBytes),
	); err != nil {
		return nil, err
	}
	if idx.mmap, err = gommap.Map(
		idx.file.Fd(),
		gommap.PROT_READ|gommap.PROT_WRITE,
		gommap.MAP_SHARED,
	); err != nil {
		return nil, err
	}
	return idx, nil
}

// Close はindexを閉じる処理を行います。メモリマップの同期、ファイルの同期、トランケーション、およびクローズを実行します。
// エラーが発生した場合、最初に遭遇したエラーを返します。
func (i *index) Close() error {
	if err := i.mmap.Sync(gommap.MS_SYNC); err != nil {
		return err
	}
	if err := i.file.Sync(); err != nil {
		return err
	}
	if err := i.file.Truncate(int64(i.size)); err != nil {
		return err
	}
	return i.file.Close()
}

// Read は、指定された位置からエントリを読み取り、値、オフセット、およびエラーを返します。
// 位置が -1 の場合、最後のエントリを読み取ります。
// ファイルサイズが不正な場合、または範囲外の位置にアクセスすると io.EOF を返します。
func (i *index) Read(in int64) (out uint32, pos uint64, err error) {
	if i.size == 0 {
		return 0, 0, io.EOF
	}
	if in == -1 {
		out = uint32((i.size / entWidth) - 1)
	} else {
		out = uint32(in)
	}
	pos = uint64(out) * entWidth
	if i.size < pos+entWidth {
		return 0, 0, io.EOF
	}
	out = enc.Uint32(i.mmap[pos : pos+offWidth])
	pos = enc.Uint64(i.mmap[pos+offWidth : pos+entWidth])
	return out, pos, nil
}

// Write は、指定されたオフセットと位置をエントリとしてメモリマップに保存します。マックスに達した場合は io.EOF を返します。
func (i *index) Write(off uint32, pos uint64) error {
	if i.isMaxed() {
		return io.EOF
	}
	enc.PutUint32(i.mmap[i.size:i.size+offWidth], off)
	enc.PutUint64(i.mmap[i.size+offWidth:i.size+entWidth], pos)
	i.size += uint64(entWidth)
	return nil
}

// isMaxed は、メモリマップが容量の上限に達しているかを判定し、達していれば true を返します。
func (i *index) isMaxed() bool {
	return uint64(len(i.mmap)) < i.size+entWidth
}

// Name は、関連付けられたファイルの名前を文字列として返します。
func (i *index) Name() string {
	return i.file.Name()
}
