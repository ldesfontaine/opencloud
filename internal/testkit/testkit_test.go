package testkit

import (
	"net"
	"strconv"
	"testing"
)

func TestSHA256Hex_KnownValue(t *testing.T) {
	const emptySum = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := SHA256Hex(nil); got != emptySum {
		t.Fatalf("got %s", got)
	}
}

func TestClosedPort_NobodyListens(t *testing.T) {
	port := ClosedPort(t)
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err == nil {
		_ = conn.Close()
		t.Fatal("the port must be closed")
	}
}

func TestEmptyRoot_StartsEmpty(t *testing.T) {
	root := EmptyRoot(t)
	directory, err := root.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	names, err := directory.Readdirnames(-1)
	if err != nil || len(names) != 0 {
		t.Fatalf("a new root must be empty, got %v (%v)", names, err)
	}
}

func TestMustJSON_EncodesTheValue(t *testing.T) {
	if got := string(MustJSON(t, map[string]int{"a": 1})); got != `{"a":1}` {
		t.Fatalf("got %s", got)
	}
}
