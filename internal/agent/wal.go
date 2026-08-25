package agent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ProxyDelta is a per-proxy byte increment within a WAL record.
type ProxyDelta struct {
	RemotePort int    `json:"rp"`
	Proto      string `json:"p"`
	In         int64  `json:"in"`
	Out        int64  `json:"out"`
	Status     string `json:"st"`
}

// TrafficRec is one WAL entry: the traffic-accounting slice of a 5s sample.
type TrafficRec struct {
	Seq         int64        `json:"seq"`
	AtMs        int64        `json:"at_ms"`
	NodeRxDelta int64        `json:"nrx"`
	NodeTxDelta int64        `json:"ntx"`
	Proxies     []ProxyDelta `json:"px,omitempty"`
}

// WAL is an append-only, seq-tagged traffic log surviving agent restarts. It
// backs exactly-once accounting: on (re)connect the agent replays entries with
// seq greater than the panel's committed watermark.
type WAL struct {
	mu   sync.Mutex
	path string
	f    *os.File
	seq  int64
}

// OpenWAL opens (creating if needed) <dir>/traffic.wal for append and recovers
// the max seq so numbering stays monotonic across restarts.
func OpenWAL(dir string) (*WAL, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "traffic.wal")
	w := &WAL{path: path}
	if recs, err := readAll(path); err == nil {
		for _, r := range recs {
			if r.Seq > w.seq {
				w.seq = r.Seq
			}
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	w.f = f
	return w, nil
}

// NextSeq returns the next monotonic sequence number.
func (w *WAL) NextSeq() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seq++
	return w.seq
}

// Append writes one record as a JSON line.
func (w *WAL) Append(rec TrafficRec) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err = w.f.Write(b)
	return err
}

// Stream calls fn for each record with Seq > afterSeq, in file order (ascending
// seq), stopping early if fn returns false.
//
// Streaming rather than returning a slice keeps a long WAL out of memory: at the
// 7-day retention and a 5s sample interval that is ~120k records (~18 MB of
// JSON), which would otherwise blow well past the agent's ~10 MB RSS budget
// every time a panel reconnects.
func (w *WAL) Stream(afterSeq int64, fn func(TrafficRec) bool) error {
	w.mu.Lock()
	path := w.path
	w.mu.Unlock()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r TrafficRec
		if json.Unmarshal(line, &r) != nil {
			continue // torn/corrupt tail line: skip it, keep replaying the rest
		}
		if r.Seq <= afterSeq {
			continue
		}
		if !fn(r) {
			return nil
		}
	}
	return sc.Err()
}

// Rotate rewrites the WAL dropping entries older than maxAge.
func (w *WAL) Rotate(maxAge time.Duration) error {
	cutoff := time.Now().Add(-maxAge).UnixMilli()
	w.mu.Lock()
	defer w.mu.Unlock()
	recs, err := readAll(w.path)
	if err != nil {
		return err
	}
	kept := make([]TrafficRec, 0, len(recs))
	for _, r := range recs {
		if r.AtMs >= cutoff {
			kept = append(kept, r)
		}
	}
	if len(kept) == len(recs) {
		return nil // nothing to drop
	}
	tmp := w.path + ".tmp"
	tf, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(tf)
	enc := json.NewEncoder(bw)
	for _, r := range kept {
		if err := enc.Encode(r); err != nil {
			tf.Close()
			return err
		}
	}
	bw.Flush()
	tf.Close()
	if w.f != nil {
		w.f.Close()
	}
	if err := os.Rename(tmp, w.path); err != nil {
		return err
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	w.f = f
	return nil
}

// Close closes the WAL file.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		return w.f.Close()
	}
	return nil
}

func readAll(path string) ([]TrafficRec, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []TrafficRec
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r TrafficRec
		if json.Unmarshal(line, &r) == nil {
			out = append(out, r)
		}
	}
	return out, sc.Err()
}
