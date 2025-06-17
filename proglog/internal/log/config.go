package log

// Config はログセグメントに関連する設定を管理する構造体です。
// Segment フィールドは各セグメントの容量制限や初期オフセットを設定します。
// nolint:revive
type Config struct {
	Segment struct {
		MaxStoreBytes uint64
		MaxIndexBytes uint64
		InitialOffset uint64
	}
}
