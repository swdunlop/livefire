package tarantula

import (
	"encoding/hex"
	"testing"
	"time"
)

var key = make([]byte, 16)
var auth = make([]byte, 16)

func TestSeal(t *testing.T) {
	message := "hello, seal!"
	data := []byte(message)

	seal, err := Seal(auth, key, data, time.Now().Add(time.Hour))
	if err != nil {
		t.Error(err.Error())
		println(hex.Dump(seal))
		return
	}

	data, err = Unseal(auth, key, seal)
	if err != nil {
		t.Error(err.Error())
		println(hex.Dump(seal))
		return
	}

	if string(data) != message {
		t.Errorf("data mismatch, expected %#v, got %#v", message, string(data))
		hex.Dump(seal)
		return
	}
}
