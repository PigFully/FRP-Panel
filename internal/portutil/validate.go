package portutil

import "fmt"

// ValidatePort checks a port is in the legal 1..65535 range.
func ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("端口号必须在 1-65535 之间")
	}
	return nil
}

// ReservedReason returns a non-empty Chinese reason if the port falls in a
// reserved segment that must not be used as a public remote_port. Empty string
// means the port is allowed by policy.
func ReservedReason(port int) string {
	switch {
	case port == PortSSH:
		return "该端口为 SSH 保留端口（22）"
	case port == PortFrpsBind:
		return "该端口为 frps 绑定端口（7000）"
	case port == PortAgentMgmt:
		return "该端口为 Agent 管理端口（8443）"
	case port >= AdminLow && port <= AdminHigh:
		return fmt.Sprintf("该端口落在 frpc admin 保留段（%d-%d）", AdminLow, AdminHigh)
	case port < 1024:
		return "该端口为系统保留低端口（<1024），请使用 1024 以上端口"
	}
	return ""
}

// IsReserved reports whether the port is in a reserved segment.
func IsReserved(port int) bool { return ReservedReason(port) != "" }

// ValidateRemotePort combines range + reserved-segment validation and returns
// a user-facing Chinese error, or nil if acceptable.
func ValidateRemotePort(port int) error {
	if err := ValidatePort(port); err != nil {
		return err
	}
	if r := ReservedReason(port); r != "" {
		return fmt.Errorf("%s", r)
	}
	return nil
}
