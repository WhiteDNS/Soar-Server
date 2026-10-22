package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCottenCLIReportsActualStartupFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
	path := filepath.Join(t.TempDir(), "probe.json")
	key := "test-only-cotten-check-key-material-000001"
	raw, err := json.Marshal(map[string]string{"domain": "private.test.example", "key": key})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	code := run([]string{"-cotten-profile-file", path, "-resolver", listener.Addr().String(), "-tcp", "only", "-timeout", "1s", "-json"}, &output)
	var actual result
	if err = json.Unmarshal(output.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	if code != 1 || actual.OK || actual.Handshake || actual.Stage != "startup" || actual.ErrorClass != "deadline_exceeded" {
		t.Fatalf("startup failure information was lost: code=%d, %+v", code, actual)
	}
	for _, private := range []string{key, "private.test.example", listener.Addr().String(), path} {
		if strings.Contains(output.String(), private) {
			t.Fatal("probe JSON exposed credentials or traffic metadata")
		}
	}
}
