// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ecdsa

import (
	"bufio"
	"compress/bzip2"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"
	"io"
	"math/big"
	"os"
	"strings"
	"testing"
)

func testAllCurves(t *testing.T, f func(*testing.T, elliptic.Curve)) {
	tests := []struct {
		name  string
		curve elliptic.Curve
	}{
		{"P256", elliptic.P256()},
		{"P224", elliptic.P224()},
		{"P384", elliptic.P384()},
		{"P521", elliptic.P521()},
	}
	if testing.Short() {
		tests = tests[:1]
	}
	for _, test := range tests {
		curve := test.curve
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			f(t, curve)
		})
	}
}

func TestKeyGeneration(t *testing.T) {
	testAllCurves(t, testKeyGeneration)
}

func testKeyGeneration(t *testing.T, c elliptic.Curve) {
	priv, err := GenerateKey(c, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if !c.IsOnCurve(priv.PublicKey.X, priv.PublicKey.Y) {
		t.Errorf("public key invalid: %s", err)
	}
}

// https://eprint.iacr.org/2015/1135.pdf
// Section 4.2: Related-Key Attack against DSA
func testRelatedKeySignOracleAttack(t *testing.T, c elliptic.Curve) {
	skS, _ := GenerateKey(c, rand.Reader)

	m0 := []byte("m0")
	m1 := []byte("m1")
	z0 := hashToInt(m0, c)
	z1 := hashToInt(m1, c)
	z0Inv := new(big.Int).ModInverse(z0, c.Params().N)
	a := new(big.Int).Mul(z1, z0Inv)
	a.Mod(a, c.Params().N)
	aKey := &PrivateKey{
		D: a,
	}

	// Signature: (r, s = t^(-1)(x1 + axr))
	r, s, err := BlindKeySign(rand.Reader, skS, aKey, m1)
	if err != nil {
		t.Errorf("BlindKeySign error: %s", err)
		return
	}

	// Forgery: m* = m0, (r*, s*) = (r, (s/a) mod q)
	rForge := r
	aInv := new(big.Int).ModInverse(a, c.Params().N)
	sForge := new(big.Int).Mul(s, aInv)
	sForge.Mod(sForge, c.Params().N)

	if Verify(&skS.PublicKey, m0, rForge, sForge) {
		t.Errorf("Verify succeeded when it should have failed")
	}
}

func TestRelatedKeySignOracleAttack(t *testing.T) {
	testAllCurves(t, testRelatedKeySignOracleAttack)
}

func TestBlindKeySign(t *testing.T) {
	testAllCurves(t, testBlindKeySign)
}

func testBlindKeySign(t *testing.T, c elliptic.Curve) {
	skS, _ := GenerateKey(c, rand.Reader)
	skB, _ := GenerateKey(c, rand.Reader)

	hashed := []byte("testing")
	r, s, err := BlindKeySign(rand.Reader, skS, skB, hashed)
	if err != nil {
		t.Errorf("BlindKeySign error: %s", err)
		return
	}

	pkR, err := BlindPublicKey(c, &skS.PublicKey, skB)
	if err != nil {
		t.Errorf("BlindPublicKey error: %s", err)
		return
	}

	if !Verify(pkR, hashed, r, s) {
		t.Errorf("Verify failed")
	}
}

func TestBlindPublicKey(t *testing.T) {
	testAllCurves(t, testBlindPublicKey)
}

func testBlindPublicKey(t *testing.T, c elliptic.Curve) {
	skS, _ := GenerateKey(c, rand.Reader)
	skB, _ := GenerateKey(c, rand.Reader)

	pkR, err := BlindPublicKey(c, &skS.PublicKey, skB)
	if err != nil {
		t.Errorf("BlindPublicKey error: %s", err)
		return
	}
	pkO, err := UnblindPublicKey(c, pkR, skB)
	if err != nil {
		t.Errorf("UnblindPublicKey error: %s", err)
		return
	}

	if !pkO.Equal(&skS.PublicKey) {
		t.Errorf("Unblinded key does not match original key")
		return
	}
}

func TestSignAndVerify(t *testing.T) {
	testAllCurves(t, testSignAndVerify)
}

func testSignAndVerify(t *testing.T, c elliptic.Curve) {
	priv, _ := GenerateKey(c, rand.Reader)

	hashed := []byte("testing")
	r, s, err := Sign(rand.Reader, priv, hashed)
	if err != nil {
		t.Errorf("error signing: %s", err)
		return
	}

	if !Verify(&priv.PublicKey, hashed, r, s) {
		t.Errorf("Verify failed")
	}

	hashed[0] ^= 0xff
	if Verify(&priv.PublicKey, hashed, r, s) {
		t.Errorf("Verify always works!")
	}
}

func TestSignAndVerifyASN1(t *testing.T) {
	testAllCurves(t, testSignAndVerifyASN1)
}

func testSignAndVerifyASN1(t *testing.T, c elliptic.Curve) {
	priv, _ := GenerateKey(c, rand.Reader)

	hashed := []byte("testing")
	sig, err := SignASN1(rand.Reader, priv, hashed)
	if err != nil {
		t.Errorf("error signing: %s", err)
		return
	}

	if !VerifyASN1(&priv.PublicKey, hashed, sig) {
		t.Errorf("VerifyASN1 failed")
	}

	hashed[0] ^= 0xff
	if VerifyASN1(&priv.PublicKey, hashed, sig) {
		t.Errorf("VerifyASN1 always works!")
	}
}

func TestNonceSafety(t *testing.T) {
	testAllCurves(t, testNonceSafety)
}

func testNonceSafety(t *testing.T, c elliptic.Curve) {
	priv, _ := GenerateKey(c, rand.Reader)

	hashed := []byte("testing")
	r0, s0, err := Sign(zeroReader, priv, hashed)
	if err != nil {
		t.Errorf("error signing: %s", err)
		return
	}

	hashed = []byte("testing...")
	r1, s1, err := Sign(zeroReader, priv, hashed)
	if err != nil {
		t.Errorf("error signing: %s", err)
		return
	}

	if s0.Cmp(s1) == 0 {
		// This should never happen.
		t.Errorf("the signatures on two different messages were the same")
	}

	if r0.Cmp(r1) == 0 {
		t.Errorf("the nonce used for two different messages was the same")
	}
}

func TestINDCCA(t *testing.T) {
	testAllCurves(t, testINDCCA)
}

func testINDCCA(t *testing.T, c elliptic.Curve) {
	priv, _ := GenerateKey(c, rand.Reader)

	hashed := []byte("testing")
	r0, s0, err := Sign(rand.Reader, priv, hashed)
	if err != nil {
		t.Errorf("error signing: %s", err)
		return
	}

	r1, s1, err := Sign(rand.Reader, priv, hashed)
	if err != nil {
		t.Errorf("error signing: %s", err)
		return
	}

	if s0.Cmp(s1) == 0 {
		t.Errorf("two signatures of the same message produced the same result")
	}

	if r0.Cmp(r1) == 0 {
		t.Errorf("two signatures of the same message produced the same nonce")
	}
}

func fromHex(s string) *big.Int {
	r, ok := new(big.Int).SetString(s, 16)
	if !ok {
		panic("bad hex")
	}
	return r
}

// cut slices s around the first instance of sep,
// returning the text before and after sep.
// The found result reports whether sep appears in s.
// If sep does not appear in s, cut returns s, "", false.
func cut(s, sep string) (before, after string, found bool) {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}

func TestVectors(t *testing.T) {
	// This test runs the full set of NIST test vectors from
	// https://csrc.nist.gov/groups/STM/cavp/documents/dss/186-3ecdsatestvectors.zip
	//
	// The SigVer.rsp file has been edited to remove test vectors for
	// unsupported algorithms and has been compressed.

	if testing.Short() {
		return
	}

	f, err := os.Open("testdata/SigVer.rsp.bz2")
	if err != nil {
		t.Fatal(err)
	}

	buf := bufio.NewReader(bzip2.NewReader(f))

	lineNo := 1
	var h hash.Hash
	var msg []byte
	var hashed []byte
	var r, s *big.Int
	pub := new(PublicKey)

	for {
		line, err := buf.ReadString('\n')
		if len(line) == 0 {
			if err == io.EOF {
				break
			}
			t.Fatalf("error reading from input: %s", err)
		}
		lineNo++
		// Need to remove \r\n from the end of the line.
		if !strings.HasSuffix(line, "\r\n") {
			t.Fatalf("bad line ending (expected \\r\\n) on line %d", lineNo)
		}
		line = line[:len(line)-2]

		if len(line) == 0 || line[0] == '#' {
			continue
		}

		if line[0] == '[' {
			line = line[1 : len(line)-1]
			curve, hash, _ := cut(line, ",")

			switch curve {
			case "P-224":
				pub.Curve = elliptic.P224()
			case "P-256":
				pub.Curve = elliptic.P256()
			case "P-384":
				pub.Curve = elliptic.P384()
			case "P-521":
				pub.Curve = elliptic.P521()
			default:
				pub.Curve = nil
			}

			switch hash {
			case "SHA-1":
				h = sha1.New()
			case "SHA-224":
				h = sha256.New224()
			case "SHA-256":
				h = sha256.New()
			case "SHA-384":
				h = sha512.New384()
			case "SHA-512":
				h = sha512.New()
			default:
				h = nil
			}

			continue
		}

		if h == nil || pub.Curve == nil {
			continue
		}

		switch {
		case strings.HasPrefix(line, "Msg = "):
			if msg, err = hex.DecodeString(line[6:]); err != nil {
				t.Fatalf("failed to decode message on line %d: %s", lineNo, err)
			}
		case strings.HasPrefix(line, "Qx = "):
			pub.X = fromHex(line[5:])
		case strings.HasPrefix(line, "Qy = "):
			pub.Y = fromHex(line[5:])
		case strings.HasPrefix(line, "R = "):
			r = fromHex(line[4:])
		case strings.HasPrefix(line, "S = "):
			s = fromHex(line[4:])
		case strings.HasPrefix(line, "Result = "):
			expected := line[9] == 'P'
			h.Reset()
			h.Write(msg)
			hashed := h.Sum(hashed[:0])
			if Verify(pub, hashed, r, s) != expected {
				t.Fatalf("incorrect result on line %d", lineNo)
			}
		default:
			t.Fatalf("unknown variable on line %d: %s", lineNo, line)
		}
	}
}

func TestNegativeInputs(t *testing.T) {
	testAllCurves(t, testNegativeInputs)
}

func testNegativeInputs(t *testing.T, curve elliptic.Curve) {
	key, err := GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Errorf("failed to generate key")
	}

	var hash [32]byte
	r := new(big.Int).SetInt64(1)
	r.Lsh(r, 550 /* larger than any supported curve */)
	r.Neg(r)

	if Verify(&key.PublicKey, hash[:], r, r) {
		t.Errorf("bogus signature accepted")
	}
}

func TestZeroHashSignature(t *testing.T) {
	testAllCurves(t, testZeroHashSignature)
}

func testZeroHashSignature(t *testing.T, curve elliptic.Curve) {
	zeroHash := make([]byte, 64)

	privKey, err := GenerateKey(curve, rand.Reader)
	if err != nil {
		panic(err)
	}

	// Sign a hash consisting of all zeros.
	r, s, err := Sign(rand.Reader, privKey, zeroHash)
	if err != nil {
		panic(err)
	}

	// Confirm that it can be verified.
	if !Verify(&privKey.PublicKey, zeroHash, r, s) {
		t.Errorf("zero hash signature verify failed for %T", curve)
	}
}

// func benchmarkAllCurves(t *testing.B, f func(*testing.B, elliptic.Curve)) {
// 	tests := []struct {
// 		name  string
// 		curve elliptic.Curve
// 	}{
// 		{"P256", elliptic.P256()},
// 		{"P224", elliptic.P224()},
// 		{"P384", elliptic.P384()},
// 		{"P521", elliptic.P521()},
// 	}
// 	for _, test := range tests {
// 		curve := test.curve
// 		t.Run(test.name, func(t *testing.B) {
// 			f(t, curve)
// 		})
// 	}
// }

func BenchmarkSign(b *testing.B) {
	benchmarkAllCurves(b, func(b *testing.B, curve elliptic.Curve) {
		priv, err := GenerateKey(curve, rand.Reader)
		if err != nil {
			b.Fatal(err)
		}
		hashed := []byte("testing")

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sig, err := SignASN1(rand.Reader, priv, hashed)
			if err != nil {
				b.Fatal(err)
			}
			// Prevent the compiler from optimizing out the operation.
			hashed[0] = sig[0]
		}
	})
}

func BenchmarkVerify(b *testing.B) {
	benchmarkAllCurves(b, func(b *testing.B, curve elliptic.Curve) {
		priv, err := GenerateKey(curve, rand.Reader)
		if err != nil {
			b.Fatal(err)
		}
		hashed := []byte("testing")
		r, s, err := Sign(rand.Reader, priv, hashed)
		if err != nil {
			b.Fatal(err)
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if !Verify(&priv.PublicKey, hashed, r, s) {
				b.Fatal("verify failed")
			}
		}
	})
}

func BenchmarkGenerateKey(b *testing.B) {
	benchmarkAllCurves(b, func(b *testing.B, curve elliptic.Curve) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := GenerateKey(curve, rand.Reader); err != nil {
				b.Fatal(err)
			}
		}
	})
}


func BenchmarkBlindKeyGeneration(b *testing.B) {
	benchmarkAllCurves(b, func(b *testing.B, curve elliptic.Curve) {
		_, err := GenerateKey(curve, rand.Reader)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = GenerateKey(curve, rand.Reader) // Use the _ identifier to indicate unused variables.
		}
	})
}

func BenchmarkBlindPublicKey(b *testing.B) {
	benchmarkAllCurves(b, func(b *testing.B, curve elliptic.Curve) {
		skS, _ := GenerateKey(curve, rand.Reader)
		skB, _ := GenerateKey(curve, rand.Reader)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pkR, _ := BlindPublicKey(curve, &skS.PublicKey, skB) // Use the _ identifier to indicate unused variables.
			// Prevent the compiler from optimizing out the operation.
			_ = pkR
		}
	})
}

func BenchmarkUnblindPublicKey(b *testing.B) {
	benchmarkAllCurves(b, func(b *testing.B, curve elliptic.Curve) {
		skS, _ := GenerateKey(curve, rand.Reader)
		skB, _ := GenerateKey(curve, rand.Reader)
		pkR, _ := BlindPublicKey(curve, &skS.PublicKey, skB)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pkO, _ := UnblindPublicKey(curve, pkR, skB) // Use the _ identifier to indicate unused variables.
			// Prevent the compiler from optimizing out the operation.
			_ = pkO
		}
	})
}

func BenchmarkBlindKeySign(b *testing.B) {
	benchmarkAllCurves(b, func(b *testing.B, curve elliptic.Curve) {
		skS, _ := GenerateKey(curve, rand.Reader)
		skB, _ := GenerateKey(curve, rand.Reader)
		hashed := []byte("testing")
		r, s, err := BlindKeySign(rand.Reader, skS, skB, hashed)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _, _ = BlindKeySign(rand.Reader, skS, skB, hashed) // Use the _ identifier to indicate unused variables.
		}
		// Prevent the compiler from optimizing out the operation.
		_, _ = r, s
	})
}


func benchmarkAllCurves(t *testing.B, f func(*testing.B, elliptic.Curve)) {
	tests := []struct {
		name  string
		curve elliptic.Curve
	}{
		{"P256", elliptic.P256()},
		{"P224", elliptic.P224()},
		{"P384", elliptic.P384()},
		{"P521", elliptic.P521()},
	}
	for _, test := range tests {
		curve := test.curve
		t.Run(test.name, func(t *testing.B) {
			f(t, curve)
		})
	}
}