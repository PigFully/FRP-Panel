package panel

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// decodeJSON parses a JSON request body (bounded) into v. Unknown fields stay
// rejected (that strictness catches client/server drift), but the offending
// field name is passed through: collapsing every decode failure into a bare
// "格式错误" is what made a stray `id` in an edit body so hard to pin down.
func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if f, ok := unknownField(err); ok {
			return Err(CodeBadRequest, "请求体包含未知字段 "+f+"（前端与面板版本可能不一致，请强制刷新页面）")
		}
		return Err(CodeBadRequest, "请求体格式错误")
	}
	return nil
}

// unknownField pulls the field name out of encoding/json's unknown-field error.
// The stdlib does not expose a typed error for it, so matching the message is
// the only option; a miss just falls back to the generic message.
func unknownField(err error) (string, bool) {
	const pfx = `json: unknown field "`
	s := err.Error()
	i := strings.Index(s, pfx)
	if i < 0 {
		return "", false
	}
	rest := s[i+len(pfx):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return "", false
	}
	return rest[:j], true
}

// pathID reads a numeric path parameter.
func pathID(r *http.Request, name string) (int64, error) {
	s := chi.URLParam(r, name)
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, Err(CodeBadRequest, "无效的 ID")
	}
	return id, nil
}

// requireDB fails fast with a DB-down business error.
func (a *App) requireDB(w http.ResponseWriter) bool {
	if !a.DBUp() {
		fail(w, ErrDBDown)
		return false
	}
	return true
}
