// Package receipt encodes/decodes the node registration receipt that the agent
// installer prints and the panel parses when adding a node (spec §7.1).
package receipt

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

// Receipt is the JSON payload embedded (base64) in the installer's output.
type Receipt struct {
	IP       string `json:"ip"`
	Port     int    `json:"port"`
	Token    string `json:"token"`
	FP       string `json:"fp"`  // TLS cert fingerprint, "sha256:<hex>"
	Ver      string `json:"ver"` // agent version
	FrpsPort int    `json:"frps_port"`
}

// Encode marshals the receipt and base64 (std, no padding stripped) encodes it.
func Encode(r Receipt) (string, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// Decode parses a base64 receipt string. It tolerates surrounding whitespace,
// newlines and both std/url and padded/unpadded encodings so that a user can
// paste a copied block verbatim.
func Decode(s string) (Receipt, error) {
	var r Receipt
	cleaned := strings.Map(func(ch rune) rune {
		switch ch {
		case ' ', '\t', '\r', '\n':
			return -1
		}
		return ch
	}, strings.TrimSpace(s))
	if cleaned == "" {
		return r, errors.New("注册回执为空")
	}
	var raw []byte
	var err error
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		raw, err = enc.DecodeString(cleaned)
		if err == nil {
			break
		}
	}
	if err != nil {
		return r, errors.New("注册回执格式无效（无法 Base64 解码）")
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, errors.New("注册回执格式无效（JSON 解析失败）")
	}
	if r.IP == "" || r.Port == 0 || r.Token == "" || r.FP == "" {
		return r, errors.New("注册回执缺少必要字段（ip/port/token/fp）")
	}
	return r, nil
}
