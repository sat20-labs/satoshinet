package wire

import (
	"bytes"
	"strings"
	"testing"
)

func TestDirectMessageLargeCanonicalCodec(t *testing.T) {
	message := &DirectMessage{
		SenderAccount: strings.Repeat("1", 64), RecipientAccount: strings.Repeat("2", 64),
		SenderMsgID: 7, MessageID: strings.Repeat("a", MessageIDHexSize), Ciphertext: bytes.Repeat([]byte{0x5a}, 128*1024), SenderSignature: bytes.Repeat([]byte{1}, 64),
	}
	encoded, err := SerializeDirectMessage(message, true)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DeserializeDirectMessage(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SenderMsgID != 7 || !bytes.Equal(decoded.Ciphertext, message.Ciphertext) {
		t.Fatal("direct codec mismatch")
	}
}

func TestMailMessageWireLimitDoesNotWidenOtherMailKeys(t *testing.T) {
	direct := "/mail/" + strings.Repeat("a", 64) + "/msg/" + strings.Repeat("b", 64) + "/0"
	share := "/mail/" + strings.Repeat("a", 64) + "/share/pkg/share"
	if DKVSValueSizeLimit(direct) != MaxDKVSBlobValueSize {
		t.Fatal("direct mail must use large internal wire bound")
	}
	if DKVSValueSizeLimit(share) != MaxDKVSValueSize {
		t.Fatal("share wire bound widened unexpectedly")
	}
}
