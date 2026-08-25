package panel

import (
	"net/http"
)

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := ClientIP(r)
	if !a.auth.LoginAllowed(ip) {
		failCode(w, CodeLoginLocked, "登录失败次数过多，请 10 分钟后再试")
		return
	}
	if !a.requireDB(w) {
		return
	}
	var req loginReq
	if err := decodeJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	u, err := a.store.GetUserByUsername(r.Context(), req.Username)
	if err != nil {
		fail(w, ErrDBDown)
		return
	}
	if u == nil || !CheckPassword(u.PasswordHash, req.Password) {
		a.auth.LoginFail(ip)
		a.AddLog("panel_op", req.Username, nil, "登录失败（账户或密码错误）")
		failCode(w, CodeUnauthorized, "账户或密码错误")
		return
	}
	a.auth.LoginSuccess(ip)
	token, err := a.auth.Issue(u)
	if err != nil {
		fail(w, err)
		return
	}
	a.auth.SetSession(w, token)
	a.AddLog("panel_op", u.Username, nil, "登录成功")
	ok(w, map[string]any{"username": u.Username, "panel_name": a.PanelName()})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c := FromContext(r.Context()); c != nil {
		a.AddLog("panel_op", c.Uname, nil, "退出登录")
	}
	a.auth.ClearSession(w)
	ok(w, nil)
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	c := FromContext(r.Context())
	// Refresh the CSRF cookie if absent so a reloaded tab keeps working.
	if _, err := r.Cookie(cookieCSRF); err != nil {
		http.SetCookie(w, &http.Cookie{Name: cookieCSRF, Value: RandomToken(16), Path: "/", HttpOnly: false, SameSite: http.SameSiteLaxMode, Secure: a.cfg.TLS.Enabled, MaxAge: int(jwtTTL.Seconds())})
	}
	base := a.cfg.UpdateBaseURL
	if base == "" {
		base, _ = a.store.GetSetting(r.Context(), "update_base_url")
	}
	ok(w, map[string]any{"username": c.Uname, "panel_name": a.PanelName(), "version": a.versionString(), "install_base": base})
}

type changePwReq struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

func (a *App) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if !a.requireDB(w) {
		return
	}
	c := FromContext(r.Context())
	var req changePwReq
	if err := decodeJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	if len(req.NewPassword) < 8 {
		failCode(w, CodeValidation, "新密码长度至少 8 位")
		return
	}
	u, err := a.store.GetUserByID(r.Context(), c.UID)
	if err != nil {
		fail(w, ErrDBDown)
		return
	}
	if u == nil || !CheckPassword(u.PasswordHash, req.OldPassword) {
		failCode(w, CodeValidation, "原密码不正确")
		return
	}
	hash, err := HashPassword(req.NewPassword)
	if err != nil {
		fail(w, err)
		return
	}
	newPV, err := a.store.UpdatePassword(r.Context(), c.UID, hash)
	if err != nil {
		fail(w, ErrDBDown)
		return
	}
	// Re-issue this session's JWT with the new pwd_version so the current tab
	// stays logged in while every other (old) JWT is instantly invalidated.
	u.PwdVersion = newPV
	token, err := a.auth.Issue(u)
	if err != nil {
		fail(w, err)
		return
	}
	a.auth.SetSession(w, token)
	a.AddLog("panel_op", c.Uname, nil, "修改管理员密码（其他会话已失效）")
	ok(w, nil)
}
