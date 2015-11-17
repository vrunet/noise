package noise

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	. "gopkg.in/check.v1"
)

func mustHex(s []byte) []byte {
	res, err := hex.DecodeString(string(s))
	if err != nil {
		panic(err)
	}
	return res
}

func hexReader(s []byte) io.Reader {
	return bytes.NewBuffer(mustHex(s))
}

var patterns = make(map[string]HandshakePattern)
var ciphers = map[string]CipherFunc{
	"AESGCM":     CipherAESGCM,
	"ChaChaPoly": CipherChaChaPoly,
}
var hashes = map[string]HashFunc{
	"SHA256":  HashSHA256,
	"SHA512":  HashSHA512,
	"BLAKE2b": HashBLAKE2b,
	"BLAKE2s": HashBLAKE2s,
}

var patternKeys = make(map[string]patternKeyInfo)

type patternKeyInfo struct {
	is, rs, isr, rsi, e bool
}

func init() {
	for _, h := range []HandshakePattern{HandshakeNN, HandshakeKN, HandshakeNK, HandshakeKK, HandshakeNE, HandshakeKE, HandshakeNX, HandshakeKX, HandshakeXN, HandshakeIN, HandshakeXK, HandshakeIK, HandshakeXE, HandshakeIE, HandshakeXX, HandshakeIX, HandshakeN, HandshakeK, HandshakeX} {
		patterns[h.Name] = h
		var k patternKeyInfo
		if len(h.Name) == 1 {
			switch h.Name {
			case "N":
				k.rs = true
				k.rsi = true
			case "K":
				k.is = true
				k.isr = true
				k.rs = true
				k.rsi = true
			case "X":
				k.is = true
				k.rs = true
				k.rsi = true
			}
		} else {
			switch h.Name[0] {
			case 'X', 'I':
				k.is = true
			case 'K':
				k.is = true
				k.isr = true
			}
			switch h.Name[1] {
			case 'K':
				k.rs = true
				k.rsi = true
			case 'E':
				k.rs = true
				k.rsi = true
				k.e = true
			case 'X':
				k.rs = true
			}
		}
		patternKeys[h.Name] = k
	}
}

func (NoiseSuite) TestVectors(c *C) {
	f, err := os.Open("vectors.txt")
	c.Assert(err, IsNil)
	r := bufio.NewReader(f)

	var hsI, hsR *HandshakeState
	var staticR, staticI, ephR DHKey
	var configI, configR Config
	var keyInfo patternKeyInfo
	var payload []byte

	for {
		line, _, err := r.ReadLine()
		if err == io.EOF {
			break
		}
		c.Assert(err, IsNil)

		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		splitLine := bytes.SplitN(line, []byte("="), 2)
		c.Assert(splitLine, HasLen, 2)

		switch string(splitLine[0]) {
		case "init_static":
			staticI = DH25519.GenerateKeypair(hexReader(splitLine[1]))
		case "resp_static":
			staticR = DH25519.GenerateKeypair(hexReader(splitLine[1]))
		case "resp_ephemeral":
			ephR = DH25519.GenerateKeypair(hexReader(splitLine[1]))
		case "handshake":
			c.Log(string(splitLine[1]))
			configI, configR = Config{Initiator: true}, Config{}
			hsI, hsR = nil, nil
			components := strings.SplitN(string(splitLine[1]), "_", 5)
			keyInfo = patternKeys[components[1]]
			configI.Pattern = patterns[components[1]]
			configI.CipherSuite = NewCipherSuite(DH25519, ciphers[components[3]], hashes[components[4]])
			configR.Pattern = configI.Pattern
			configR.CipherSuite = configI.CipherSuite
		case "gen_init_ephemeral":
			configI.Random = hexReader(splitLine[1])
		case "gen_resp_ephemeral":
			configR.Random = hexReader(splitLine[1])
		case "prologue":
			configI.Prologue = mustHex(splitLine[1])
			configR.Prologue = configI.Prologue
		case "preshared_key":
			configI.PresharedKey = mustHex(splitLine[1])
			configR.PresharedKey = configI.PresharedKey
		}

		if !bytes.HasPrefix(splitLine[0], []byte("msg_")) {
			continue
		}
		if bytes.HasSuffix(splitLine[0], []byte("_payload")) {
			payload = mustHex(splitLine[1])
			continue
		}

		if hsI == nil {
			if keyInfo.is {
				configI.StaticKeypair = staticI
			}
			if keyInfo.rs {
				configR.StaticKeypair = staticR
			}
			if keyInfo.isr {
				configR.PeerStatic = staticI.Public
			}
			if keyInfo.rsi {
				configI.PeerStatic = staticR.Public
			}
			if keyInfo.e {
				configR.EphemeralKeypair = ephR
				configI.PeerEphemeral = ephR.Public
			}
			hsI, hsR = NewHandshakeState(configI), NewHandshakeState(configR)
		}

		i, _ := strconv.Atoi(string(splitLine[0][4:5]))

		writer, reader := hsI, hsR
		if i%2 != 0 {
			writer, reader = hsR, hsI
		}

		msg, csW0, csW1 := writer.WriteMessage(nil, payload)
		c.Assert(fmt.Sprintf("%x", msg), Equals, string(splitLine[1]))
		res, csR0, csR1, err := reader.ReadMessage(nil, msg)
		c.Assert(err, IsNil)
		c.Assert(string(res), Equals, string(payload))

		if i == len(configI.Pattern.Messages) {
			plain := []byte("yellowsubmarine")
			encrypted := csW0.Encrypt(nil, nil, plain)
			decrypted, err := csR0.Decrypt(nil, nil, encrypted)
			c.Assert(err, IsNil)
			c.Assert(string(decrypted), Equals, string(plain))

			encrypted = csW1.Encrypt(nil, nil, plain)
			decrypted, err = csR1.Decrypt(nil, nil, encrypted)
			c.Assert(err, IsNil)
			c.Assert(string(decrypted), Equals, string(plain))
		}
		payload = nil
	}
}