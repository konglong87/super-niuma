package runtime

import "testing"

func TestAESGCMCodecRoundTripAndCiphertextIsNotPlaintext(t *testing.T) {
	codec, err := NewAESGCMCodec([]byte("01234567890123456789012345678901"), "v1")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := codec.Encode(map[string]string{"text": "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == `{"text":"secret"}` {
		t.Fatal("payload was stored as plaintext")
	}
	var got map[string]string
	if err := codec.Decode(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got["text"] != "secret" {
		t.Fatalf("decoded = %#v", got)
	}
}
