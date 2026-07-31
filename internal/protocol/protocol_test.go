package protocol

import (
	"encoding/json"
	"testing"
)

func TestBase64RoundTrip(t *testing.T) {
	src := []byte("hello\x00world")
	enc := EncodeBase64(src)
	got, err := DecodeBase64(enc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(src) {
		t.Fatalf("got %q", got)
	}
}

func TestSessionDataFrame(t *testing.T) {
	frame := NewSessionData(TypeProxyData, "sess-1", []byte{1, 2, 3})
	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	typ, err := DecodeType(raw)
	if err != nil || typ != TypeProxyData {
		t.Fatalf("type=%q err=%v", typ, err)
	}
	var decoded SessionData
	if err := DecodeJSON(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	payload, err := DecodeBase64(decoded.Data)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != string([]byte{1, 2, 3}) {
		t.Fatalf("payload=%v", payload)
	}
}

func TestTaskResultStatuses(t *testing.T) {
	code := 0
	r := NewTaskResult("tid", StatusSuccess, &code, "out", "")
	raw, _ := json.Marshal(r)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if m["type"] != TypeTaskResult || m["status"] != StatusSuccess {
		t.Fatalf("%v", m)
	}
}
