/*
casbinを使ってACLの処理を行う
*/

package auth

import (
	"fmt"
	"github.com/casbin/casbin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// New は新しい Authorizer を初期化して返す関数です。
// model と policy のパスを指定して認可設定をロードします。
func New(model, policy string) *Authorizer {
	enforcer := casbin.NewEnforcer(model, policy)
	return &Authorizer{
		enforcer: enforcer,
	}
}

// Authorizer は認可を処理するための構造体です。
// casbin.Enforcer を使用して権限の検証を行います。
type Authorizer struct {
	enforcer *casbin.Enforcer
}

// Authorize は、指定された subject、object、および action に基づいてアクセス許可を確認します。
// アクセスが拒否された場合、PermissionDenied エラーを返します。
func (a *Authorizer) Authorize(subject, object, action string) error {
	if !a.enforcer.Enforce(subject, object, action) {
		msg := fmt.Sprintf(
			"%s not permitted to %s to %s",
			subject,
			action,
			object,
		)
		st := status.New(codes.PermissionDenied, msg)
		return st.Err()
	}
	return nil
}
