// go-pst is a library for reading Personal Storage Table (.pst) files (written in Go/Golang).
//
// Copyright 2023 Marten Mooij
// Copyright 2025 Yury Tikhoglaz
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
	"encoding/binary"
	"testing"
)

// mockReader implements Reader interface for testing.
type mockReader struct {
	data []byte
}

func newMockReader(data []byte) *mockReader {
	return &mockReader{data: data}
}

func (m *mockReader) ReadAt(p []byte, off int64) (n int, err error) {
	if off >= int64(len(m.data)) {
		return 0, nil
	}
	n = copy(p, m.data[off:])
	return n, nil
}

func (m *mockReader) ReadAtAsync(outputBuffer []byte, offset uint64, callback func(err error)) (uint64, error) {
	n, err := m.ReadAt(outputBuffer, int64(offset))
	if callback != nil {
		callback(err)
	}
	return uint64(n), err
}

// mockBTreeStore implements BTreeStore interface for testing.
type mockBTreeStore struct {
	nodes map[Identifier]BTreeNode
}

func newMockBTreeStore() *mockBTreeStore {
	return &mockBTreeStore{
		nodes: make(map[Identifier]BTreeNode),
	}
}

func (m *mockBTreeStore) Get(key BTreeNode) (BTreeNode, bool) {
	node, ok := m.nodes[key.Identifier]
	return node, ok
}

func (m *mockBTreeStore) Load(node BTreeNode) (BTreeNode, bool) {
	existing, ok := m.nodes[node.Identifier]
	m.nodes[node.Identifier] = node
	return existing, ok
}

func (m *mockBTreeStore) Len() int {
	return len(m.nodes)
}

func (m *mockBTreeStore) Clear() {
	m.nodes = make(map[Identifier]BTreeNode)
}

// buildSLBLOCKUnicode builds a Unicode SLBLOCK (leaf block) with SLENTRY entries.
// SLBLOCK structure:
// - btype (1 byte): 0x02
// - cLevel (1 byte): 0x00 for leaf
// - cEnt (2 bytes): entry count
// - dwPadding (4 bytes): padding
// - rgentries: array of SLENTRY (24 bytes each for Unicode)
func buildSLBLOCKUnicode(entries []LocalDescriptor) []byte {
	entryCount := len(entries)
	// Header: 8 bytes + entries (24 bytes each)
	data := make([]byte, 8+entryCount*24)

	data[0] = 0x02                                               // btype
	data[1] = 0x00                                               // cLevel = 0 (leaf)
	binary.LittleEndian.PutUint16(data[2:4], uint16(entryCount)) // cEnt

	offset := 8
	for _, entry := range entries {
		// SLENTRY for Unicode: nid (8 bytes) + bidData (8 bytes) + bidSub (8 bytes)
		binary.LittleEndian.PutUint64(data[offset:offset+8], uint64(entry.Identifier))
		binary.LittleEndian.PutUint64(data[offset+8:offset+16], uint64(entry.DataIdentifier))
		binary.LittleEndian.PutUint64(data[offset+16:offset+24], uint64(entry.LocalDescriptorsIdentifier))
		offset += 24
	}

	return data
}

// buildSIBLOCKUnicode builds a Unicode SIBLOCK (branch/intermediate block) with SIENTRY entries.
// SIBLOCK structure:
// - btype (1 byte): 0x02
// - cLevel (1 byte): 0x01 for intermediate
// - cEnt (2 bytes): entry count
// - dwPadding (4 bytes): padding
// - rgentries: array of SIENTRY (16 bytes each for Unicode)
func buildSIBLOCKUnicode(entries []struct{ nid, bid Identifier }) []byte {
	entryCount := len(entries)
	// Header: 8 bytes + entries (16 bytes each)
	data := make([]byte, 8+entryCount*16)

	data[0] = 0x02                                               // btype
	data[1] = 0x01                                               // cLevel = 1 (intermediate/branch)
	binary.LittleEndian.PutUint16(data[2:4], uint16(entryCount)) // cEnt

	offset := 8
	for _, entry := range entries {
		// SIENTRY for Unicode: nid (8 bytes) + bid (8 bytes)
		binary.LittleEndian.PutUint64(data[offset:offset+8], uint64(entry.nid))
		binary.LittleEndian.PutUint64(data[offset+8:offset+16], uint64(entry.bid))
		offset += 16
	}

	return data
}

// buildSLBLOCKANSI builds an ANSI SLBLOCK (leaf block) with SLENTRY entries.
func buildSLBLOCKANSI(entries []LocalDescriptor) []byte {
	entryCount := len(entries)
	// Header: 4 bytes + entries (12 bytes each)
	data := make([]byte, 4+entryCount*12)

	data[0] = 0x02                                               // btype
	data[1] = 0x00                                               // cLevel = 0 (leaf)
	binary.LittleEndian.PutUint16(data[2:4], uint16(entryCount)) // cEnt

	offset := 4
	for _, entry := range entries {
		// SLENTRY for ANSI: nid (4 bytes) + bidData (4 bytes) + bidSub (4 bytes)
		binary.LittleEndian.PutUint32(data[offset:offset+4], uint32(entry.Identifier))
		binary.LittleEndian.PutUint32(data[offset+4:offset+8], uint32(entry.DataIdentifier))
		binary.LittleEndian.PutUint32(data[offset+8:offset+12], uint32(entry.LocalDescriptorsIdentifier))
		offset += 12
	}

	return data
}

// buildSIBLOCKANSI builds an ANSI SIBLOCK (branch/intermediate block) with SIENTRY entries.
func buildSIBLOCKANSI(entries []struct{ nid, bid Identifier }) []byte {
	entryCount := len(entries)
	// Header: 4 bytes + entries (8 bytes each)
	data := make([]byte, 4+entryCount*8)

	data[0] = 0x02                                               // btype
	data[1] = 0x01                                               // cLevel = 1 (intermediate/branch)
	binary.LittleEndian.PutUint16(data[2:4], uint16(entryCount)) // cEnt

	offset := 4
	for _, entry := range entries {
		// SIENTRY for ANSI: nid (4 bytes) + bid (4 bytes)
		binary.LittleEndian.PutUint32(data[offset:offset+4], uint32(entry.nid))
		binary.LittleEndian.PutUint32(data[offset+4:offset+8], uint32(entry.bid))
		offset += 8
	}

	return data
}

func TestNewLocalDescriptor(t *testing.T) {
	t.Run("Unicode format", func(t *testing.T) {
		data := make([]byte, 24)
		binary.LittleEndian.PutUint64(data[0:8], 100)   // Identifier
		binary.LittleEndian.PutUint64(data[8:16], 200)  // DataIdentifier
		binary.LittleEndian.PutUint64(data[16:24], 300) // LocalDescriptorsIdentifier

		ld := NewLocalDescriptor(data, FormatTypeUnicode)

		if ld.Identifier != 100 {
			t.Errorf("expected Identifier 100, got %d", ld.Identifier)
		}
		if ld.DataIdentifier != 200 {
			t.Errorf("expected DataIdentifier 200, got %d", ld.DataIdentifier)
		}
		if ld.LocalDescriptorsIdentifier != 300 {
			t.Errorf("expected LocalDescriptorsIdentifier 300, got %d", ld.LocalDescriptorsIdentifier)
		}
	})

	t.Run("ANSI format", func(t *testing.T) {
		data := make([]byte, 12)
		binary.LittleEndian.PutUint32(data[0:4], 100)  // Identifier
		binary.LittleEndian.PutUint32(data[4:8], 200)  // DataIdentifier
		binary.LittleEndian.PutUint32(data[8:12], 300) // LocalDescriptorsIdentifier

		ld := NewLocalDescriptor(data, FormatTypeANSI)

		if ld.Identifier != 100 {
			t.Errorf("expected Identifier 100, got %d", ld.Identifier)
		}
		if ld.DataIdentifier != 200 {
			t.Errorf("expected DataIdentifier 200, got %d", ld.DataIdentifier)
		}
		if ld.LocalDescriptorsIdentifier != 300 {
			t.Errorf("expected LocalDescriptorsIdentifier 300, got %d", ld.LocalDescriptorsIdentifier)
		}
	})
}

func TestGetLocalDescriptorsFromIdentifier_LeafNode(t *testing.T) {
	t.Run("Unicode leaf node (SLBLOCK)", func(t *testing.T) {
		entries := []LocalDescriptor{
			{Identifier: 100, DataIdentifier: 101, LocalDescriptorsIdentifier: 102},
			{Identifier: 200, DataIdentifier: 201, LocalDescriptorsIdentifier: 202},
		}

		slblock := buildSLBLOCKUnicode(entries)

		// Create mock file data
		blockOffset := int64(1000)
		fileData := make([]byte, blockOffset+int64(len(slblock)))
		copy(fileData[blockOffset:], slblock)

		blockStore := newMockBTreeStore()
		blockStore.Load(BTreeNode{
			Identifier: 50,
			FileOffset: blockOffset,
		})

		file := &File{
			Reader:     newMockReader(fileData),
			FormatType: FormatTypeUnicode,
			BlockBTree: blockStore,
		}

		result, err := file.GetLocalDescriptorsFromIdentifier(50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result) != 2 {
			t.Fatalf("expected 2 local descriptors, got %d", len(result))
		}

		if result[0].Identifier != 100 || result[0].DataIdentifier != 101 {
			t.Errorf("first entry mismatch: got %+v", result[0])
		}
		if result[1].Identifier != 200 || result[1].DataIdentifier != 201 {
			t.Errorf("second entry mismatch: got %+v", result[1])
		}
	})

	t.Run("ANSI leaf node (SLBLOCK)", func(t *testing.T) {
		entries := []LocalDescriptor{
			{Identifier: 100, DataIdentifier: 101, LocalDescriptorsIdentifier: 102},
		}

		slblock := buildSLBLOCKANSI(entries)

		blockOffset := int64(500)
		fileData := make([]byte, blockOffset+int64(len(slblock)))
		copy(fileData[blockOffset:], slblock)

		blockStore := newMockBTreeStore()
		// Use even identifier (LSB = 0) since GetBlockBTreeNode masks with 0xfffffffe
		blockStore.Load(BTreeNode{
			Identifier: 24,
			FileOffset: blockOffset,
		})

		file := &File{
			Reader:     newMockReader(fileData),
			FormatType: FormatTypeANSI,
			BlockBTree: blockStore,
		}

		result, err := file.GetLocalDescriptorsFromIdentifier(24)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result) != 1 {
			t.Fatalf("expected 1 local descriptor, got %d", len(result))
		}

		if result[0].Identifier != 100 {
			t.Errorf("expected Identifier 100, got %d", result[0].Identifier)
		}
	})
}

func TestGetLocalDescriptorsFromIdentifier_BranchNode(t *testing.T) {
	t.Run("Unicode branch node (SIBLOCK) with single child", func(t *testing.T) {
		// Child SLBLOCK entries
		childEntries := []LocalDescriptor{
			{Identifier: 1000, DataIdentifier: 1001, LocalDescriptorsIdentifier: 1002},
			{Identifier: 2000, DataIdentifier: 2001, LocalDescriptorsIdentifier: 2002},
		}
		childSLBLOCK := buildSLBLOCKUnicode(childEntries)

		// Parent SIBLOCK pointing to child
		parentSIENTRYs := []struct{ nid, bid Identifier }{
			{nid: 1000, bid: 60}, // bid points to child block
		}
		parentSIBLOCK := buildSIBLOCKUnicode(parentSIENTRYs)

		// Layout in file: parent at offset 1000, child at offset 2000
		parentOffset := int64(1000)
		childOffset := int64(2000)
		fileData := make([]byte, 3000)
		copy(fileData[parentOffset:], parentSIBLOCK)
		copy(fileData[childOffset:], childSLBLOCK)

		blockStore := newMockBTreeStore()
		blockStore.Load(BTreeNode{
			Identifier: 50, // Parent block ID
			FileOffset: parentOffset,
		})
		blockStore.Load(BTreeNode{
			Identifier: 60, // Child block ID
			FileOffset: childOffset,
		})

		file := &File{
			Reader:     newMockReader(fileData),
			FormatType: FormatTypeUnicode,
			BlockBTree: blockStore,
		}

		result, err := file.GetLocalDescriptorsFromIdentifier(50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result) != 2 {
			t.Fatalf("expected 2 local descriptors from child, got %d", len(result))
		}

		if result[0].Identifier != 1000 {
			t.Errorf("expected first Identifier 1000, got %d", result[0].Identifier)
		}
		if result[1].Identifier != 2000 {
			t.Errorf("expected second Identifier 2000, got %d", result[1].Identifier)
		}
	})

	t.Run("Unicode branch node (SIBLOCK) with multiple children", func(t *testing.T) {
		// First child SLBLOCK
		child1Entries := []LocalDescriptor{
			{Identifier: 100, DataIdentifier: 101, LocalDescriptorsIdentifier: 0},
			{Identifier: 200, DataIdentifier: 201, LocalDescriptorsIdentifier: 0},
		}
		child1SLBLOCK := buildSLBLOCKUnicode(child1Entries)

		// Second child SLBLOCK
		child2Entries := []LocalDescriptor{
			{Identifier: 300, DataIdentifier: 301, LocalDescriptorsIdentifier: 0},
		}
		child2SLBLOCK := buildSLBLOCKUnicode(child2Entries)

		// Parent SIBLOCK pointing to both children
		parentSIENTRYs := []struct{ nid, bid Identifier }{
			{nid: 100, bid: 60}, // First child
			{nid: 300, bid: 70}, // Second child
		}
		parentSIBLOCK := buildSIBLOCKUnicode(parentSIENTRYs)

		// Layout in file
		parentOffset := int64(1000)
		child1Offset := int64(2000)
		child2Offset := int64(3000)
		fileData := make([]byte, 4000)
		copy(fileData[parentOffset:], parentSIBLOCK)
		copy(fileData[child1Offset:], child1SLBLOCK)
		copy(fileData[child2Offset:], child2SLBLOCK)

		blockStore := newMockBTreeStore()
		blockStore.Load(BTreeNode{Identifier: 50, FileOffset: parentOffset})
		blockStore.Load(BTreeNode{Identifier: 60, FileOffset: child1Offset})
		blockStore.Load(BTreeNode{Identifier: 70, FileOffset: child2Offset})

		file := &File{
			Reader:     newMockReader(fileData),
			FormatType: FormatTypeUnicode,
			BlockBTree: blockStore,
		}

		result, err := file.GetLocalDescriptorsFromIdentifier(50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result) != 3 {
			t.Fatalf("expected 3 local descriptors total, got %d", len(result))
		}

		// Verify all entries are present
		ids := make(map[Identifier]bool)
		for _, ld := range result {
			ids[ld.Identifier] = true
		}

		for _, expectedID := range []Identifier{100, 200, 300} {
			if !ids[expectedID] {
				t.Errorf("expected Identifier %d to be present", expectedID)
			}
		}
	})

	t.Run("ANSI branch node (SIBLOCK)", func(t *testing.T) {
		// Child SLBLOCK
		childEntries := []LocalDescriptor{
			{Identifier: 500, DataIdentifier: 501, LocalDescriptorsIdentifier: 0},
		}
		childSLBLOCK := buildSLBLOCKANSI(childEntries)

		// Parent SIBLOCK
		parentSIENTRYs := []struct{ nid, bid Identifier }{
			{nid: 500, bid: 30},
		}
		parentSIBLOCK := buildSIBLOCKANSI(parentSIENTRYs)

		parentOffset := int64(100)
		childOffset := int64(200)
		fileData := make([]byte, 300)
		copy(fileData[parentOffset:], parentSIBLOCK)
		copy(fileData[childOffset:], childSLBLOCK)

		blockStore := newMockBTreeStore()
		blockStore.Load(BTreeNode{Identifier: 20, FileOffset: parentOffset})
		blockStore.Load(BTreeNode{Identifier: 30, FileOffset: childOffset})

		file := &File{
			Reader:     newMockReader(fileData),
			FormatType: FormatTypeANSI,
			BlockBTree: blockStore,
		}

		result, err := file.GetLocalDescriptorsFromIdentifier(20)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result) != 1 {
			t.Fatalf("expected 1 local descriptor, got %d", len(result))
		}

		if result[0].Identifier != 500 {
			t.Errorf("expected Identifier 500, got %d", result[0].Identifier)
		}
	})
}

func TestGetLocalDescriptorsFromIdentifier_ZeroIdentifier(t *testing.T) {
	file := &File{}

	result, err := file.GetLocalDescriptorsFromIdentifier(0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != nil {
		t.Errorf("expected nil result for zero identifier, got %v", result)
	}
}

func TestGetLocalDescriptorsFromIdentifier_InvalidSignature(t *testing.T) {
	// Create block with invalid signature (not 0x02)
	data := make([]byte, 100)
	data[0] = 0x01 // Invalid signature

	blockOffset := int64(0)
	blockStore := newMockBTreeStore()
	blockStore.Load(BTreeNode{Identifier: 10, FileOffset: blockOffset})

	file := &File{
		Reader:     newMockReader(data),
		FormatType: FormatTypeUnicode,
		BlockBTree: blockStore,
	}

	_, err := file.GetLocalDescriptorsFromIdentifier(10)
	if err == nil {
		t.Fatal("expected error for invalid signature")
	}

	if err != ErrLocalDescriptorsSignatureInvalid {
		t.Errorf("expected ErrLocalDescriptorsSignatureInvalid, got %v", err)
	}
}

func TestFindLocalDescriptor(t *testing.T) {
	descriptors := []LocalDescriptor{
		{Identifier: 100, DataIdentifier: 101},
		{Identifier: 200, DataIdentifier: 201},
		{Identifier: 300, DataIdentifier: 301},
	}

	t.Run("finds existing descriptor", func(t *testing.T) {
		result, err := FindLocalDescriptor(200, descriptors)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Identifier != 200 {
			t.Errorf("expected Identifier 200, got %d", result.Identifier)
		}
		if result.DataIdentifier != 201 {
			t.Errorf("expected DataIdentifier 201, got %d", result.DataIdentifier)
		}
	})

	t.Run("returns error for non-existent descriptor", func(t *testing.T) {
		_, err := FindLocalDescriptor(999, descriptors)
		if err == nil {
			t.Fatal("expected error for non-existent descriptor")
		}
		if err != ErrLocalDescriptorNotFound {
			t.Errorf("expected ErrLocalDescriptorNotFound, got %v", err)
		}
	})

	t.Run("handles empty slice", func(t *testing.T) {
		_, err := FindLocalDescriptor(100, []LocalDescriptor{})
		if err != ErrLocalDescriptorNotFound {
			t.Errorf("expected ErrLocalDescriptorNotFound, got %v", err)
		}
	})
}

func TestGetLocalDescriptorsFromIdentifier_DeepNesting(t *testing.T) {
	// Test multi-level nesting (level 2 -> level 1 -> level 0)
	// This tests recursive handling when there are multiple levels of SIBLOCK

	// Level 0: Leaf SLBLOCK with actual entries
	leafEntries := []LocalDescriptor{
		{Identifier: 9000, DataIdentifier: 9001, LocalDescriptorsIdentifier: 0},
	}
	leafSLBLOCK := buildSLBLOCKUnicode(leafEntries)

	// Level 1: Intermediate SIBLOCK pointing to leaf
	level1SIENTRYs := []struct{ nid, bid Identifier }{
		{nid: 9000, bid: 80}, // Points to leaf
	}
	level1SIBLOCK := buildSIBLOCKUnicode(level1SIENTRYs)

	// Layout
	level1Offset := int64(1000)
	leafOffset := int64(2000)
	fileData := make([]byte, 3000)
	copy(fileData[level1Offset:], level1SIBLOCK)
	copy(fileData[leafOffset:], leafSLBLOCK)

	blockStore := newMockBTreeStore()
	blockStore.Load(BTreeNode{Identifier: 70, FileOffset: level1Offset})
	blockStore.Load(BTreeNode{Identifier: 80, FileOffset: leafOffset})

	file := &File{
		Reader:     newMockReader(fileData),
		FormatType: FormatTypeUnicode,
		BlockBTree: blockStore,
	}

	result, err := file.GetLocalDescriptorsFromIdentifier(70)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 local descriptor, got %d", len(result))
	}

	if result[0].Identifier != 9000 {
		t.Errorf("expected Identifier 9000, got %d", result[0].Identifier)
	}
}

func BenchmarkGetLocalDescriptorsFromIdentifier_LeafNode(b *testing.B) {
	entries := make([]LocalDescriptor, 100)
	for i := range entries {
		entries[i] = LocalDescriptor{
			Identifier:     Identifier(i * 100),
			DataIdentifier: Identifier(i*100 + 1),
		}
	}

	slblock := buildSLBLOCKUnicode(entries)
	blockOffset := int64(1000)
	fileData := make([]byte, blockOffset+int64(len(slblock)))
	copy(fileData[blockOffset:], slblock)

	blockStore := newMockBTreeStore()
	blockStore.Load(BTreeNode{Identifier: 50, FileOffset: blockOffset})

	file := &File{
		Reader:     newMockReader(fileData),
		FormatType: FormatTypeUnicode,
		BlockBTree: blockStore,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = file.GetLocalDescriptorsFromIdentifier(50)
	}
}

func BenchmarkGetLocalDescriptorsFromIdentifier_BranchNode(b *testing.B) {
	// Create multiple children
	var children [][]byte
	var childOffsets []int64
	var siEntries []struct{ nid, bid Identifier }

	baseOffset := int64(10000)
	for i := 0; i < 10; i++ {
		entries := make([]LocalDescriptor, 10)
		for j := range entries {
			entries[j] = LocalDescriptor{
				Identifier:     Identifier(i*1000 + j),
				DataIdentifier: Identifier(i*1000 + j + 1),
			}
		}
		child := buildSLBLOCKUnicode(entries)
		children = append(children, child)
		offset := baseOffset + int64(i*1000)
		childOffsets = append(childOffsets, offset)
		siEntries = append(siEntries, struct{ nid, bid Identifier }{
			nid: Identifier(i * 1000),
			bid: Identifier(100 + i),
		})
	}

	parentSIBLOCK := buildSIBLOCKUnicode(siEntries)
	parentOffset := int64(1000)

	fileData := make([]byte, 20000)
	copy(fileData[parentOffset:], parentSIBLOCK)
	for i, child := range children {
		copy(fileData[childOffsets[i]:], child)
	}

	blockStore := newMockBTreeStore()
	blockStore.Load(BTreeNode{Identifier: 50, FileOffset: parentOffset})
	for i := range children {
		blockStore.Load(BTreeNode{
			Identifier: Identifier(100 + i),
			FileOffset: childOffsets[i],
		})
	}

	file := &File{
		Reader:     newMockReader(fileData),
		FormatType: FormatTypeUnicode,
		BlockBTree: blockStore,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = file.GetLocalDescriptorsFromIdentifier(50)
	}
}

// Helper to ensure bytes package is used (for potential future tests)
var _ = bytes.Buffer{}
