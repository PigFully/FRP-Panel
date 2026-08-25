package receipt

import "testing"

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := Receipt{IP: "203.0.113.10", Port: 8443, Token: "abc123", FP: "sha256:deadbeef", Ver: "v1.0.0", FrpsPort: 7000}
	enc, err := Encode(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := Decode(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out != in {
		t.Errorf("round trip mismatch: %+v != %+v", out, in)
	}
}

func TestDecodeTolerantOfWhitespace(t *testing.T) {
	in := Receipt{IP: "1.2.3.4", Port: 8443, Token: "t", FP: "sha256:aa", Ver: "v1", FrpsPort: 7000}
	enc, _ := Encode(in)
	// Simulate a pasted block with newlines/spaces.
	messy := "  " + enc[:10] + "\n" + enc[10:] + "  \r\n"
	out, err := Decode(messy)
	if err != nil {
		t.Fatalf("decode messy: %v", err)
	}
	if out.IP != in.IP || out.Token != in.Token {
		t.Errorf("messy decode mismatch: %+v", out)
	}
}

func TestDecodeErrors(t *testing.T) {
	if _, err := Decode(""); err == nil {
		t.Error("empty should error")
	}
	if _, err := Decode("!!!!not base64!!!!"); err == nil {
		t.Error("invalid base64 should error")
	}
	// Valid base64 but missing fields.
	enc, _ := Encode(Receipt{IP: "1.2.3.4"})
	if _, err := Decode(enc); err == nil {
		t.Error("missing token/fp should error")
	}
}
