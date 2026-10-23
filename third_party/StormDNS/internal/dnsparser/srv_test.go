// ==============================================================================
// StormDNS
// Author: nullroute1970
// Github: https://github.com/nullroute1970/StormDNS
// Year: 2026
// ==============================================================================
package dnsparser

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	Enums "stormdns-go/internal/enums"
	VpnProto "stormdns-go/internal/vpnproto"
)

// buildSRVAnswerTestPacket crafts a single-question, single-answer response
// whose answer type is SRV and whose rdata is exactly the bytes given. The
// owner is written as a compression pointer to the question name (0xC00C).
func buildSRVAnswerTestPacket(t *testing.T, rdata []byte) []byte {
	t.Helper()
	question, err := BuildTXTQuestionPacket("x.v.example.com", Enums.DNS_RECORD_TYPE_SRV, 0)
	if err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 0, len(question)+2+10+len(rdata))
	packet = append(packet, question...)
	binary.BigEndian.PutUint16(packet[6:8], 1) // ANCount = 1
	packet = append(packet, 0xC0, 0x0C)        // owner: pointer to qname
	var fixed [10]byte
	binary.BigEndian.PutUint16(fixed[0:2], Enums.DNS_RECORD_TYPE_SRV)
	binary.BigEndian.PutUint16(fixed[2:4], Enums.DNSQ_CLASS_IN)
	binary.BigEndian.PutUint16(fixed[8:10], uint16(len(rdata)))
	packet = append(packet, fixed[:]...)
	packet = append(packet, rdata...)
	return packet
}

// srvRData prepends the six-byte priority/weight/port prefix to a target name.
func srvRData(targetWire []byte) []byte {
	rdata := make([]byte, 0, 6+len(targetWire))
	rdata = append(rdata, 0, 0, 0, 0, 0, 0)
	return append(rdata, targetWire...)
}

func TestParsePacketDecodesSRVTargetNamePlain(t *testing.T) {
	target, err := encodeDNSNameStrict("srv1.v.example.com")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePacket(buildSRVAnswerTestPacket(t, srvRData(target)))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Answers) != 1 {
		t.Fatalf("want 1 answer, got %d", len(parsed.Answers))
	}
	if got := parsed.Answers[0].RDataName; got != "srv1.v.example.com" {
		t.Fatalf("RDataName = %q, want %q", got, "srv1.v.example.com")
	}
	if len(parsed.Answers[0].RData) != 6+len(target) {
		t.Fatalf("RData len = %d, want %d", len(parsed.Answers[0].RData), 6+len(target))
	}
}

func TestParsePacketDecodesSRVTargetAfterFixedFields(t *testing.T) {
	// The fixed fields spell a decodable name ("fak") if the parser wrongly
	// starts at rdata offset 0; the real target only appears at offset 6.
	target, err := encodeDNSNameStrict("real.v.example.com")
	if err != nil {
		t.Fatal(err)
	}
	rdata := append([]byte{3, 'f', 'a', 'k', 0, 0}, target...)
	parsed, err := ParsePacket(buildSRVAnswerTestPacket(t, rdata))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Answers) != 1 {
		t.Fatalf("want 1 answer, got %d", len(parsed.Answers))
	}
	if got := parsed.Answers[0].RDataName; got != "real.v.example.com" {
		t.Fatalf("RDataName = %q, want %q", got, "real.v.example.com")
	}
}

func TestParsePacketDecodesSRVTargetCompressed(t *testing.T) {
	// rdata target is a compression pointer to the question name (0xC00C).
	parsed, err := ParsePacket(buildSRVAnswerTestPacket(t, []byte{0, 0, 0, 0, 0, 0, 0xC0, 0x0C}))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Answers) != 1 {
		t.Fatalf("want 1 answer, got %d", len(parsed.Answers))
	}
	if got := parsed.Answers[0].RDataName; got != "x.v.example.com" {
		t.Fatalf("RDataName = %q, want %q", got, "x.v.example.com")
	}
}

func TestParsePacketDecodesSRVRootTarget(t *testing.T) {
	parsed, err := ParsePacket(buildSRVAnswerTestPacket(t, []byte{1, 2, 3, 4, 5, 6, 0}))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Answers) != 1 {
		t.Fatalf("want 1 answer, got %d", len(parsed.Answers))
	}
	if got := parsed.Answers[0].RDataName; got != "" && got != "." {
		t.Fatalf("root target must decode to the root/empty name, got %q", got)
	}
	if !bytes.Equal(parsed.Answers[0].RData, []byte{1, 2, 3, 4, 5, 6, 0}) {
		t.Fatalf("RData = %v, want fixed bytes + root", parsed.Answers[0].RData)
	}
}

func TestParsePacketToleratesShortSRVRData(t *testing.T) {
	parsed, err := ParsePacket(buildSRVAnswerTestPacket(t, []byte{1, 2, 3}))
	if err != nil {
		t.Fatalf("parse must not fail on short SRV rdata: %v", err)
	}
	if len(parsed.Answers) != 1 || parsed.Answers[0].RDataName != "" {
		t.Fatalf("want 1 answer with empty RDataName, got %+v", parsed.Answers)
	}
}

func TestParsePacketToleratesGarbageSRVTarget(t *testing.T) {
	parsed, err := ParsePacket(buildSRVAnswerTestPacket(t, []byte{0, 0, 0, 0, 0, 0, 63, 'a', 'b'}))
	if err != nil {
		t.Fatalf("parse must not fail on malformed SRV target: %v", err)
	}
	if len(parsed.Answers) != 1 || parsed.Answers[0].RDataName != "" {
		t.Fatalf("want 1 answer with empty RDataName, got %+v", parsed.Answers)
	}
}

// ---- Task 2: SRV transport builders and extraction ----

func TestSRVUnitCapacityIsNSCapacityPlusSix(t *testing.T) {
	if got := maxNSUnitBytes(); got != 159 {
		t.Fatalf("maxNSUnitBytes() = %d, want 159 (base36 250-char budget)", got)
	}
	if got, want := maxSRVUnitBytes(), maxNSUnitBytes()+6; got != want {
		t.Fatalf("maxSRVUnitBytes() = %d, want %d", got, want)
	}
}

func TestBuildSRVAnswerRDataCarriesFixedBytesAndTarget(t *testing.T) {
	unit := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x10, 0x20, 0x30}
	rdata, err := buildSRVAnswerRData(unit)
	if err != nil {
		t.Fatal(err)
	}
	if len(rdata) < 6 || !bytes.Equal(rdata[:6], unit[:6]) {
		t.Fatalf("fixed fields = %v, want %v", rdata[:6], unit[:6])
	}
	nameText, _, err := parseName(rdata, 6)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeNSAnswerName(nameText)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, unit[6:]) {
		t.Fatalf("target carries %v, want %v", decoded, unit[6:])
	}
}

func TestBuildSRVAnswerRDataEncodesSixByteUnitAsRootTarget(t *testing.T) {
	unit := []byte{1, 2, 3, 4, 5, 6}
	rdata, err := buildSRVAnswerRData(unit)
	if err != nil {
		t.Fatal(err)
	}
	if len(rdata) != 7 || rdata[6] != 0 {
		t.Fatalf("six-byte unit must encode as fixed bytes + root target, got %v", rdata)
	}
	if !bytes.Equal(rdata[:6], unit) {
		t.Fatalf("fixed fields = %v, want %v", rdata[:6], unit)
	}
}

func TestDecodeNameRDataUnitReadsSRVFixedBytes(t *testing.T) {
	targetWire, err := buildNSAnswerName([]byte{0xAA, 0xBB})
	if err != nil {
		t.Fatal(err)
	}
	rdata := append([]byte{1, 2, 3, 4, 5, 6}, targetWire...)
	parsed, err := ParsePacket(buildSRVAnswerTestPacket(t, rdata))
	if err != nil {
		t.Fatal(err)
	}
	unit, ok := decodeNameRDataUnit(parsed.Answers[0])
	if !ok {
		t.Fatal("SRV answer did not decode to a unit")
	}
	if !bytes.Equal(unit, []byte{1, 2, 3, 4, 5, 6, 0xAA, 0xBB}) {
		t.Fatalf("unit = %v, want fixed bytes + decoded target", unit)
	}
}

func TestDecodeNameRDataUnitReadsSRVRootTarget(t *testing.T) {
	parsed, err := ParsePacket(buildSRVAnswerTestPacket(t, []byte{1, 2, 3, 4, 5, 6, 0}))
	if err != nil {
		t.Fatal(err)
	}
	unit, ok := decodeNameRDataUnit(parsed.Answers[0])
	if !ok {
		t.Fatal("root-target SRV answer did not decode to a unit")
	}
	if !bytes.Equal(unit, []byte{1, 2, 3, 4, 5, 6}) {
		t.Fatalf("unit = %v, want the six fixed bytes", unit)
	}
}

func TestDecodeNameRDataUnitSkipsMalformedSRV(t *testing.T) {
	parsed, err := ParsePacket(buildSRVAnswerTestPacket(t, []byte{1, 2, 3, 4, 5, 6, 63, 'a', 'b'}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decodeNameRDataUnit(parsed.Answers[0]); ok {
		t.Fatal("malformed SRV rdata must not decode to a unit")
	}
}

func TestBuildVPNResponsePacketMirrorsSRVQuestionSingle(t *testing.T) {
	question, err := BuildTXTQuestionPacket("a1b2.v.example.com", Enums.DNS_RECORD_TYPE_SRV, 0)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("hello srv world")
	resp, err := BuildVPNResponsePacket(question, "a1b2.v.example.com", vpnPacketForTest(payload), false)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := ParsePacket(resp)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Questions) != 1 || parsed.Questions[0].Type != Enums.DNS_RECORD_TYPE_SRV {
		t.Fatalf("question type not echoed: %+v", parsed.Questions)
	}
	if len(parsed.Answers) != 1 {
		t.Fatalf("want 1 SRV answer, got %d", len(parsed.Answers))
	}
	if parsed.Answers[0].Type != Enums.DNS_RECORD_TYPE_SRV {
		t.Fatalf("answer type = %d, want SRV(%d)", parsed.Answers[0].Type, Enums.DNS_RECORD_TYPE_SRV)
	}
	if len(parsed.Answers[0].RData) < 7 {
		t.Fatalf("SRV rdata too short: %v", parsed.Answers[0].RData)
	}
	for _, label := range splitLabels(parsed.Answers[0].RDataName) {
		if len(label) > 63 {
			t.Fatalf("label %q exceeds 63 chars", label)
		}
	}

	got, err := ExtractVPNResponse(resp, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.PacketType != Enums.PACKET_PONG || !bytes.Equal(got.Payload, payload) {
		t.Fatalf("roundtrip mismatch: type=%d payload=%q", got.PacketType, got.Payload)
	}
}

func TestBuildVPNResponsePacketMirrorsSRVQuestionMulti(t *testing.T) {
	question, err := BuildTXTQuestionPacket("aa.bb.v.example.com", Enums.DNS_RECORD_TYPE_SRV, 0)
	if err != nil {
		t.Fatal(err)
	}
	payload := patternedPayload(800) // forces multiple SRV answer records
	resp, err := BuildVPNResponsePacket(question, "aa.bb.v.example.com", VpnProto.Packet{
		SessionID:      7,
		PacketType:     Enums.PACKET_STREAM_DATA, // carries stream/seq in the header
		StreamID:       3,
		SequenceNum:    99,
		TotalFragments: 1,
		Payload:        payload,
	}, true /* base64 flag must be irrelevant for SRV */)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := ParsePacket(resp)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Answers) < 2 {
		t.Fatalf("want multiple SRV answers, got %d", len(parsed.Answers))
	}
	for i, ans := range parsed.Answers {
		if ans.Type != Enums.DNS_RECORD_TYPE_SRV {
			t.Fatalf("answer %d type = %d, want SRV", i, ans.Type)
		}
		if ans.Name != "aa.bb.v.example.com" {
			t.Fatalf("answer %d owner = %q, want qname", i, ans.Name)
		}
		unit, ok := decodeNameRDataUnit(ans)
		if !ok || len(unit) < 6 {
			t.Fatalf("answer %d did not decode to a >= 6-byte unit (ok=%v len=%d)", i, ok, len(unit))
		}
	}

	got, err := ExtractVPNResponse(resp, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.PacketType != Enums.PACKET_STREAM_DATA || got.StreamID != 3 || got.SequenceNum != 99 ||
		!bytes.Equal(got.Payload, payload) {
		t.Fatalf("roundtrip mismatch: type=%d stream=%d seq=%d payloadlen=%d",
			got.PacketType, got.StreamID, got.SequenceNum, len(got.Payload))
	}
}

func TestSRVAnswerRoundTripAcrossChunkBoundaries(t *testing.T) {
	question, err := BuildTXTQuestionPacket("barrage.v.example.com", Enums.DNS_RECORD_TYPE_SRV, 0)
	if err != nil {
		t.Fatal(err)
	}
	for size := 0; size <= 600; size++ {
		payload := patternedPayload(size)
		resp, err := BuildVPNResponsePacket(question, "barrage.v.example.com", VpnProto.Packet{
			SessionID:      7,
			PacketType:     Enums.PACKET_STREAM_DATA,
			StreamID:       3,
			SequenceNum:    1,
			TotalFragments: 1,
			Payload:        payload,
		}, false)
		if err != nil {
			t.Fatalf("size %d: build failed: %v", size, err)
		}
		parsed, err := ParsePacket(resp)
		if err != nil {
			t.Fatalf("size %d: parse failed: %v", size, err)
		}
		for i, ans := range parsed.Answers {
			unit, ok := decodeNameRDataUnit(ans)
			if !ok {
				t.Fatalf("size %d: answer %d undecodable", size, i)
			}
			if len(unit) < 6 {
				t.Fatalf("size %d: answer %d unit len %d < 6", size, i, len(unit))
			}
		}
		got, err := ExtractVPNResponse(resp, false)
		if err != nil {
			t.Fatalf("size %d: extract failed: %v", size, err)
		}
		if !bytes.Equal(got.Payload, payload) {
			t.Fatalf("size %d: payload mismatch (got %d bytes)", size, len(got.Payload))
		}
	}
}

func TestExtractVPNResponseReadsSRVWithBase64Flag(t *testing.T) {
	question, err := BuildTXTQuestionPacket("k9.v.example.com", Enums.DNS_RECORD_TYPE_SRV, 0)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := VpnProto.BuildRaw(VpnProto.BuildOptions{
		SessionID:  7,
		PacketType: Enums.PACKET_PONG,
		Payload:    []byte{1, 2, 3, 4, 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	rdata, err := buildSRVAnswerRData(frame)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := buildSingleSRVResponsePacket(question, "k9.v.example.com", rdata)
	if err != nil {
		t.Fatal(err)
	}

	// baseEncoded=true must not corrupt SRV-unit decoding (the fixed bytes are
	// raw and the name transport is charset-safe by itself).
	got, err := ExtractVPNResponse(resp, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.PacketType != Enums.PACKET_PONG || !bytes.Equal(got.Payload, []byte{1, 2, 3, 4, 5}) {
		t.Fatalf("roundtrip mismatch: type=%d payload=%v", got.PacketType, got.Payload)
	}
}

func TestBuildSRVVPNResponseShortFrameFallsBackToNS(t *testing.T) {
	question, err := BuildTXTQuestionPacket("k9.v.example.com", Enums.DNS_RECORD_TYPE_SRV, 0)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := buildSRVVPNResponse(question, "k9.v.example.com", []byte{1, 2, 3, 4})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePacket(resp)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Answers) != 1 || parsed.Answers[0].Type != Enums.DNS_RECORD_TYPE_NS {
		t.Fatalf("short SRV frame must fall back to an NS answer, got %+v", parsed.Answers)
	}
}

func TestExtractVPNResponseNoPayloadFromUnreadableSRV(t *testing.T) {
	// An SRV answer whose rdata cannot be decoded must behave like a response
	// without tunnel payload: ErrTXTAnswerMissing, not a panic.
	resp := buildSRVAnswerTestPacket(t, []byte{0, 0, 0, 0, 0, 0, 63, 'a', 'b'})
	if _, err := ExtractVPNResponse(resp, false); !errors.Is(err, ErrTXTAnswerMissing) {
		t.Fatalf("want ErrTXTAnswerMissing, got %v", err)
	}
}

func TestExtractVPNResponseIgnoresRecordsAppendedToSRV(t *testing.T) {
	// Recursors may append address records after the SRV RRset; units must come
	// only from the SRV rdata, in order.
	payload := patternedPayload(400)
	question, err := BuildTXTQuestionPacket("aa.bb.v.example.com", Enums.DNS_RECORD_TYPE_SRV, 0)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := BuildVPNResponsePacket(question, "aa.bb.v.example.com", vpnPacketForTest(payload), false)
	if err != nil {
		t.Fatal(err)
	}

	owner, err := encodeDNSNameStrict("extra.example.net")
	if err != nil {
		t.Fatal(err)
	}
	var fixed [10]byte
	binary.BigEndian.PutUint16(fixed[0:2], Enums.DNS_RECORD_TYPE_A)
	binary.BigEndian.PutUint16(fixed[2:4], Enums.DNSQ_CLASS_IN)
	binary.BigEndian.PutUint16(fixed[8:10], 4)
	resp = append(resp, owner...)
	resp = append(resp, fixed[:]...)
	resp = append(resp, 1, 2, 3, 4)
	anCount := binary.BigEndian.Uint16(resp[6:8])
	binary.BigEndian.PutUint16(resp[6:8], anCount+1)

	got, err := ExtractVPNResponse(resp, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.PacketType != Enums.PACKET_PONG || !bytes.Equal(got.Payload, payload) {
		t.Fatalf("roundtrip mismatch: type=%d payloadlen=%d", got.PacketType, len(got.Payload))
	}
}
