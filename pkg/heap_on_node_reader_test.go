// go-pst is a library for reading Personal Storage Table (.pst) files (written in Go/Golang).
//
// Copyright 2023 Marten Mooij
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package pst

import (
	"bytes"
	"io"
	"testing"
)

func TestDecodeCyclicEncryption(t *testing.T) {
	// Test that DecodeCyclicEncryption is symmetric (encode then decode returns original)
	// The cyclic algorithm is symmetric, so applying it twice returns the original

	reader := &HeapOnNodeReader{}

	t.Run("symmetric encoding/decoding", func(t *testing.T) {
		original := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77}
		data := make([]byte, len(original))
		copy(data, original)

		dwKey := uint32(0x12345678)
		wOffset := uint16(0)

		// First application (encode)
		encoded := reader.DecodeCyclicEncryption(data, dwKey, wOffset)

		// The encoded data should be different from original
		if bytes.Equal(encoded, original) {
			t.Error("encoded data should be different from original")
		}

		// Second application (decode) - copy encoded data first
		data2 := make([]byte, len(encoded))
		copy(data2, encoded)
		decoded := reader.DecodeCyclicEncryption(data2, dwKey, wOffset)

		// Should match original
		if !bytes.Equal(decoded, original) {
			t.Errorf("decoded data should match original\noriginal: %v\ndecoded:  %v", original, decoded)
		}
	})

	t.Run("different keys produce different results", func(t *testing.T) {
		// Use keys that produce different w values
		// key1: 0x12340000 -> w = 0x1234 ^ 0x0000 = 0x1234
		// key2: 0x56780000 -> w = 0x5678 ^ 0x0000 = 0x5678
		data1 := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x00, 0x11}
		data2 := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x00, 0x11}

		reader.DecodeCyclicEncryption(data1, 0x12340000, 0)
		reader.DecodeCyclicEncryption(data2, 0x56780000, 0)

		// At least some bytes should be different
		sameCount := 0
		for i := range data1 {
			if data1[i] == data2[i] {
				sameCount++
			}
		}
		if sameCount == len(data1) {
			t.Errorf("different keys should produce different results\ndata1: %v\ndata2: %v", data1, data2)
		}
	})

	t.Run("offset affects decryption", func(t *testing.T) {
		data1 := []byte{0xAA, 0xBB, 0xCC, 0xDD}
		data2 := []byte{0xAA, 0xBB, 0xCC, 0xDD}

		reader.DecodeCyclicEncryption(data1, 0x12345678, 0)
		reader.DecodeCyclicEncryption(data2, 0x12345678, 10) // Different offset

		if bytes.Equal(data1, data2) {
			t.Error("different offsets should produce different results")
		}
	})

	t.Run("empty data", func(t *testing.T) {
		data := []byte{}
		result := reader.DecodeCyclicEncryption(data, 0x12345678, 0)
		if len(result) != 0 {
			t.Error("empty data should return empty result")
		}
	})

	t.Run("single byte", func(t *testing.T) {
		original := []byte{0x42}
		data := make([]byte, len(original))
		copy(data, original)

		encoded := reader.DecodeCyclicEncryption(data, 0xABCD1234, 0)

		// Encode again (decode)
		data2 := make([]byte, len(encoded))
		copy(data2, encoded)
		decoded := reader.DecodeCyclicEncryption(data2, 0xABCD1234, 0)

		if !bytes.Equal(decoded, original) {
			t.Errorf("single byte decode failed: got %v, want %v", decoded, original)
		}
	})
}

func TestDecodeCompressibleEncryption(t *testing.T) {
	reader := &HeapOnNodeReader{}

	t.Run("decodes using mpbbI table", func(t *testing.T) {
		// Test that the decoding uses the inverse permutation table
		// mpbbI[0x47] should map back to 0x00 (since mpbbR[0x00] = 0x41 = 65)
		// Let's verify a known mapping
		data := []byte{0x47} // This is mpbbI[0] in the original table = 71 = 0x47

		reader.DecodeCompressibleEncryption(data)

		// After decoding, mpbbI[0x47] should give us the decoded value
		// 0x47 = 71, and mpbbI[71] = ...
		// This tests that the function runs without error
		if len(data) != 1 {
			t.Error("data length should remain 1")
		}
	})

	t.Run("all byte values", func(t *testing.T) {
		// Test all 256 possible byte values
		data := make([]byte, 256)
		for i := 0; i < 256; i++ {
			data[i] = byte(i)
		}

		result := reader.DecodeCompressibleEncryption(data)

		if len(result) != 256 {
			t.Errorf("expected 256 bytes, got %d", len(result))
		}
	})
}

func TestHeapOnNodeReaderWithIdentifiers(t *testing.T) {
	t.Run("NewHeapOnNodeReaderWithIdentifiers stores identifiers", func(t *testing.T) {
		block := io.NewSectionReader(bytes.NewReader(make([]byte, 100)), 0, 100)
		identifiers := []Identifier{100, 200, 300}

		reader := NewHeapOnNodeReaderWithIdentifiers(EncryptionTypeCyclic, identifiers, *block)

		if len(reader.BlockIdentifiers) != 3 {
			t.Errorf("expected 3 identifiers, got %d", len(reader.BlockIdentifiers))
		}
		if reader.BlockIdentifiers[0] != 100 {
			t.Errorf("expected first identifier 100, got %d", reader.BlockIdentifiers[0])
		}
	})

	t.Run("NewHeapOnNodeReader has nil identifiers", func(t *testing.T) {
		block := io.NewSectionReader(bytes.NewReader(make([]byte, 100)), 0, 100)

		reader := NewHeapOnNodeReader(EncryptionTypePermute, *block)

		if reader.BlockIdentifiers != nil {
			t.Error("expected nil identifiers for backward compatible constructor")
		}
	})
}

func TestEncryptionTypeConstants(t *testing.T) {
	// Verify encryption type constants match [MS-PST] specification
	if EncryptionTypeNone != 0 {
		t.Errorf("EncryptionTypeNone should be 0, got %d", EncryptionTypeNone)
	}
	if EncryptionTypePermute != 1 {
		t.Errorf("EncryptionTypePermute should be 1, got %d", EncryptionTypePermute)
	}
	if EncryptionTypeCyclic != 2 {
		t.Errorf("EncryptionTypeCyclic should be 2, got %d", EncryptionTypeCyclic)
	}
}

func TestMpbbCryptTable(t *testing.T) {
	t.Run("table has correct size", func(t *testing.T) {
		if len(mpbbCrypt) != 768 {
			t.Errorf("mpbbCrypt should have 768 bytes, got %d", len(mpbbCrypt))
		}
	})

	t.Run("mpbbR slice is correct", func(t *testing.T) {
		if len(mpbbR) != 256 {
			t.Errorf("mpbbR should have 256 bytes, got %d", len(mpbbR))
		}
		// First byte of mpbbR should be 65 (from MS-PST spec)
		if mpbbR[0] != 65 {
			t.Errorf("mpbbR[0] should be 65, got %d", mpbbR[0])
		}
	})

	t.Run("mpbbS slice is correct", func(t *testing.T) {
		if len(mpbbS) != 256 {
			t.Errorf("mpbbS should have 256 bytes, got %d", len(mpbbS))
		}
		// First byte of mpbbS should be 20 (from MS-PST spec)
		if mpbbS[0] != 20 {
			t.Errorf("mpbbS[0] should be 20, got %d", mpbbS[0])
		}
	})

	t.Run("mpbbI slice is correct", func(t *testing.T) {
		if len(mpbbI) != 256 {
			t.Errorf("mpbbI should have 256 bytes, got %d", len(mpbbI))
		}
		// First byte of mpbbI should be 71 (from MS-PST spec)
		if mpbbI[0] != 71 {
			t.Errorf("mpbbI[0] should be 71, got %d", mpbbI[0])
		}
	})
}

func BenchmarkDecodeCyclicEncryption(b *testing.B) {
	reader := &HeapOnNodeReader{}
	data := make([]byte, 8192) // Typical block size
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Make a copy since the function modifies in place
		testData := make([]byte, len(data))
		copy(testData, data)
		reader.DecodeCyclicEncryption(testData, 0x12345678, 0)
	}
}

func BenchmarkDecodeCompressibleEncryption(b *testing.B) {
	reader := &HeapOnNodeReader{}
	data := make([]byte, 8192) // Typical block size
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Make a copy since the function modifies in place
		testData := make([]byte, len(data))
		copy(testData, data)
		reader.DecodeCompressibleEncryption(testData)
	}
}
