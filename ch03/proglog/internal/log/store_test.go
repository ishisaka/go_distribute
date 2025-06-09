package log

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	write = []byte("hello world")
	width = uint64(len(write)) + lenWidth
)

// TestStoreAppendRead は、store 構造体の Append, Read, ReadAt メソッドの動作確認を行うテストです。
// 一時ファイルにデータを追加・読み取りし、再初期化後も期待通り動作することを検証します。
func TestStoreAppendRead(t *testing.T) {
	f, err := os.CreateTemp("", "store_append_read_test")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	s, err := newStore(f)
	require.NoError(t, err)

	testAppend(t, s)
	testRead(t, s)
	testReadAt(t, s)

	s, err = newStore(f)
	require.NoError(t, err)
	testRead(t, s)
}

// testAppend は store の Append メソッドをテストし、データの追加動作が正しいことを検証します。
// 書き込み後の位置とサイズを確認し、エラーが発生しないことをテストします。
func testAppend(t *testing.T, s *store) {
	t.Helper()
	for i := uint64(1); i < 4; i++ {
		n, pos, err := s.Append(write)
		require.NoError(t, err)
		require.Equal(t, pos+n, width*i)
	}
}

// testRead は store の Read メソッドをテストし、正しくデータが読み込まれることを検証します。
// 各位置からの読み取り結果が期待する値と一致することを確認します。
// 必要に応じてヘルパーメソッドとして設定され、複数テストで再利用されます。
func testRead(t *testing.T, s *store) {
	t.Helper()
	var pos uint64
	for i := uint64(1); i < 4; i++ {
		read, err := s.Read(pos)
		require.NoError(t, err)
		require.Equal(t, write, read)
		pos += width
	}
}

// testReadAt は store 構造体の ReadAt メソッドをテストし、指定された位置で正確にデータが読み取れることを確認します。
// また、データサイズと内容が期待通りであることを検証します。
// テスト中にエラーが発生しないことを確認します。
func testReadAt(t *testing.T, s *store) {
	t.Helper()
	for i, off := uint64(1), int64(0); i < 4; i++ {
		b := make([]byte, lenWidth)
		n, err := s.ReadAt(b, off)
		require.NoError(t, err)
		require.Equal(t, lenWidth, n)
		off += int64(n)

		size := enc.Uint64(b)
		b = make([]byte, size)
		n, err = s.ReadAt(b, off)
		require.NoError(t, err)
		require.Equal(t, write, b)
		require.Equal(t, int(size), n)
		off += int64(n)
	}
}

// TestStoreClose は store 構造体の Close メソッドの動作をテストします。
// ファイルクローズ後のサイズの変化をチェックし、適切な動作を検証します。
func TestStoreClose(t *testing.T) {
	f, err := os.CreateTemp("", "store_close_test")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	s, err := newStore(f)
	require.NoError(t, err)
	_, _, err = s.Append(write)
	require.NoError(t, err)

	f, beforeSize, err := openFile(f.Name())
	require.NoError(t, err)

	err = s.Close()
	require.NoError(t, err)

	_, afterSize, err := openFile(f.Name())
	require.NoError(t, err)
	require.True(t, afterSize > beforeSize)
}

// openFile は指定された名前のファイルを開き、ファイルポインタ、ファイルサイズ、エラーを返します。
// ファイルが存在しない場合、新規作成されます。
// ファイル操作時に読み取り、書き込み、追記モードでオープンします。
func openFile(name string) (file *os.File, size int64, err error) {
	f, err := os.OpenFile(
		name,
		os.O_RDWR|os.O_CREATE|os.O_APPEND,
		0600,
	)
	if err != nil {
		return nil, 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	return f, fi.Size(), nil
}
