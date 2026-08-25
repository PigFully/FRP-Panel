package protocol

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strconv"
)

// ReplayToleranceSec is the max clock skew accepted for anti-replay (spec: +/-60s).
const ReplayToleranceSec = 60

// canonical builds the string that gets HMAC'd. Order is fixed and must match
// on both ends. Payload bytes are included verbatim so tampering is detected.
func canonical(e *Envelope) []byte {
	buf := make([]byte, 0, 64+len(e.Payload))
	buf = append(buf, strconv.Itoa(e.Ver)...)
	buf = append(buf, '\n')
	buf = append(buf, e.Type...)
	buf = append(buf, '\n')
	buf = append(buf, e.ID...)
	buf = append(buf, '\n')
	buf = append(buf, strconv.FormatInt(e.TS, 10)...)
	buf = append(buf, '\n')
	buf = append(buf, e.Nonce...)
	buf = append(buf, '\n')
	buf = append(buf, e.Payload...)
	return buf
}

// Sign computes the HMAC-SHA256 signature over the envelope using token as key
// and stores it in e.Sig (hex).
func (e *Envelope) Sign(token string) {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write(canonical(e))
	e.Sig = hex.EncodeToString(mac.Sum(nil))
}

var (
	// ErrBadSig means the HMAC did not match.
	ErrBadSig = errors.New("bad signature")
	// ErrReplay means the timestamp is outside the tolerance window.
	ErrReplay = errors.New("timestamp outside replay window")
)

// Verify checks the signature and (when now>0) the anti-replay timestamp window.
// Pass now=0 to skip the timestamp check (e.g. for the very first hello where
// clocks are being established). token is the per-node shared secret.
func (e *Envelope) Verify(token string, now int64) error {
	want := e.Sig
	tmp := *e
	tmp.Sig = ""
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write(canonical(&tmp))
	got := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return ErrBadSig
	}
	if now > 0 {
		d := now - e.TS
		if d < 0 {
			d = -d
		}
		if d > ReplayToleranceSec {
			return ErrReplay
		}
	}
	return nil
}
