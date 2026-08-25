package portutil

import (
	"strings"
	"testing"
)

const sampleTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000 100 0 0 10 0
   1: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 23456 1 0000 100 0 0 10 0
   2: 0100007F:C1B4 0100007F:1F90 01 00000000:00000000 00:00000000 00000000  1000        0 34567 1 0000 100 0 0 10 0
`

const sampleTCP6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:0016 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 55555 1 0000 100 0 0 10 0
`

const sampleUDP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops
   0: 00000000:0035 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 99999 2 0000000000000000 0
`

func TestParseProcNetTCPListen(t *testing.T) {
	entries, err := ParseProcNet(strings.NewReader(sampleTCP))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}
	lp := ListenPorts(entries, "tcp")
	if lp[22] != "12345" {
		t.Errorf("port 22 inode = %q, want 12345", lp[22])
	}
	if lp[8080] != "23456" {
		t.Errorf("port 8080 inode = %q, want 23456", lp[8080])
	}
	if _, ok := lp[49588]; ok {
		t.Errorf("established socket 49588 should not be reported as listening")
	}
}

func TestParseProcNetTCP6(t *testing.T) {
	entries, _ := ParseProcNet(strings.NewReader(sampleTCP6))
	lp := ListenPorts(entries, "tcp")
	if lp[22] != "55555" {
		t.Errorf("tcp6 port 22 inode = %q, want 55555", lp[22])
	}
}

func TestParseProcNetUDPBound(t *testing.T) {
	entries, _ := ParseProcNet(strings.NewReader(sampleUDP))
	lp := ListenPorts(entries, "udp")
	if lp[53] != "99999" { // 0035 hex = 53
		t.Errorf("udp port 53 inode = %q, want 99999 (udp counts any bound socket)", lp[53])
	}
}

func TestReservedReason(t *testing.T) {
	cases := map[int]bool{
		22: true, 7000: true, 8443: true, 7400: true, 7450: true, 7500: true,
		80: true, // <1024 reserved
		1024: false, 8080: false, 18443: false, 65535: false, 7399: false, 7501: false,
	}
	for port, reserved := range cases {
		if IsReserved(port) != reserved {
			t.Errorf("IsReserved(%d) = %v, want %v (reason=%q)", port, IsReserved(port), reserved, ReservedReason(port))
		}
	}
}

func TestValidateRemotePort(t *testing.T) {
	if err := ValidateRemotePort(0); err == nil {
		t.Error("port 0 should be invalid")
	}
	if err := ValidateRemotePort(70000); err == nil {
		t.Error("port 70000 should be invalid")
	}
	if err := ValidateRemotePort(7000); err == nil {
		t.Error("port 7000 should be reserved")
	}
	if err := ValidateRemotePort(18443); err != nil {
		t.Errorf("port 18443 should be valid, got %v", err)
	}
}
