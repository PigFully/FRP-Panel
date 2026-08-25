package panel

// Business error codes. 0 = ok. Non-zero codes carry a user-facing Chinese
// message the frontend can display directly.
const (
	CodeOK           = 0
	CodeBadRequest   = 40000
	CodeValidation   = 40001
	CodeUnauthorized = 40101 // 未登录
	CodeSessionStale = 40102 // 会话已失效，请重新登录
	CodeForbidden    = 40301
	CodeNotFound     = 40401
	CodePortBusy     = 40901 // 端口被占用
	CodeConflict     = 40902 // 配置已被他人修改
	CodeReservedPort = 40903 // 保留段端口
	CodeLoginLocked  = 42901 // 登录失败次数过多，账户锁定
	CodeNodeOffline  = 50301 // 节点离线
	CodeLocalNoListen = 50302 // 本地端口未监听
	CodeDBDown       = 50303 // 数据库故障
	CodeInternal     = 50000
)

// AppError is a business error carrying a code and a Chinese message.
type AppError struct {
	Code int
	Msg  string
}

func (e *AppError) Error() string { return e.Msg }

// Err builds an AppError.
func Err(code int, msg string) *AppError { return &AppError{Code: code, Msg: msg} }

// Common ready-made errors.
var (
	ErrUnauthorized = Err(CodeUnauthorized, "未登录或登录状态无效")
	ErrSessionStale = Err(CodeSessionStale, "会话已失效，请重新登录")
	ErrNotFound     = Err(CodeNotFound, "资源不存在")
	ErrDBDown       = Err(CodeDBDown, "数据库暂时不可用，请稍后重试")
	ErrCSRF         = Err(CodeForbidden, "CSRF 校验失败，请刷新页面后重试")
)
