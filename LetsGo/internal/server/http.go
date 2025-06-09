package server

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
)

// NewHTTPServer は、新しいHTTPサーバーを指定されたアドレスで初期化して返します。
// POSTメソッドでのプロデュース処理、およびGETメソッドでのコンシューム処理を提供します。
func NewHTTPServer(addr string) *http.Server {
	httpsrv := newHTTPServer()
	r := mux.NewRouter()
	r.HandleFunc("/", httpsrv.handleProduce).Methods("POST")
	r.HandleFunc("/", httpsrv.handleConsume).Methods("GET")
	return &http.Server{
		Addr:    addr,
		Handler: r,
	}
}

// httpServer は、HTTPリクエストの処理を行うサーバを表します。
// 主にログデータの生産および消費操作を処理します。
// Log フィールドはログデータの管理を担当します。
type httpServer struct {
	Log *Log
}

// newHTTPServer は、新しい httpServer インスタンスを作成して返します。
// この関数は内部で Log インスタンスを初期化し、httpServer に割り当てます。
func newHTTPServer() *httpServer {
	return &httpServer{
		Log: NewLog(),
	}
}

// ProduceRequest はログに新しいレコードを追加するリクエストを表します。
type ProduceRequest struct {
	Record Record `json:"record"`
}

// ProduceResponse はログに新しいレコードを追加した際の応答を表します。
// Offset フィールドは追加されたレコードのオフセット値を含みます。
type ProduceResponse struct {
	Offset uint64 `json:"offset"`
}

// ConsumeRequest はログレコードを消費するためのリクエストデータを表します。
// Offset フィールドは対象となるログレコードの位置を指定します。
type ConsumeRequest struct {
	Offset uint64 `json:"offset"`
}

// ConsumeResponse はログレコードを消費する際のレスポンスデータを表します。
// Record フィールドに消費されたログレコードが格納されます。
type ConsumeResponse struct {
	Record Record `json:"record"`
}

// handleProduce はHTTPリクエストからレコードをデコードし、ログに追加してオフセットを応答として返します。
func (s *httpServer) handleProduce(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var req ProduceRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	off, err := s.Log.Append(req.Record)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	res := ProduceResponse{Offset: off}
	err = json.NewEncoder(w).Encode(res)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// handleConsume はHTTPリクエストを介して指定されたオフセットのログレコードを取得してレスポンスとして返します。
// リクエストボディをConsumeRequest構造体としてデコードし、ログから対応するレコードを取得します。
// オフセットが無効な場合は404エラー、有効でない場合は500エラーを返します。
// 正常時にConsumeResponse構造体としてログレコードをJSONエンコードしてレスポンスします。
func (s *httpServer) handleConsume(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var req ConsumeRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	record, err := s.Log.Read(req.Offset)
	if err == ErrOffsetNotFound {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	res := ConsumeResponse{Record: record}
	err = json.NewEncoder(w).Encode(res)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}
