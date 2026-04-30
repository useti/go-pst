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
	"encoding/binary"

	"github.com/rotisserie/eris"
)

// LocalDescriptor represents an item in the local descriptors.
// A local descriptor is basically a reference to a node which contains the data.
type LocalDescriptor struct {
	Identifier                 Identifier
	DataIdentifier             Identifier
	LocalDescriptorsIdentifier Identifier
}

// NewLocalDescriptor creates a new local descriptor.
func NewLocalDescriptor(data []byte, formatType FormatType) LocalDescriptor {
	switch formatType {
	case FormatTypeANSI:
		return LocalDescriptor{
			Identifier:                 Identifier(binary.LittleEndian.Uint32(data[:4])),
			DataIdentifier:             Identifier(binary.LittleEndian.Uint32(data[4 : 4+4])),
			LocalDescriptorsIdentifier: Identifier(binary.LittleEndian.Uint32(data[8 : 8+4])),
		}
	default:
		// TODO - Reference [MS-PDF] that this is actually 32-bit.
		return LocalDescriptor{
			Identifier:                 Identifier(binary.LittleEndian.Uint32(data[:8])),
			DataIdentifier:             Identifier(binary.LittleEndian.Uint32(data[8 : 8+8])),
			LocalDescriptorsIdentifier: Identifier(binary.LittleEndian.Uint32(data[16 : 16+8])),
		}
	}
}

// GetLocalDescriptors returns the local descriptors of the b-tree node.
func (file *File) GetLocalDescriptors(btreeNodeEntry BTreeNode) ([]LocalDescriptor, error) {
	return file.GetLocalDescriptorsFromIdentifier(btreeNodeEntry.LocalDescriptorsIdentifier)
}

// GetLocalDescriptorsFromIdentifier returns the local descriptors of the local descriptors identifier.
// References "Local Descriptors".
func (file *File) GetLocalDescriptorsFromIdentifier(localDescriptorsIdentifier Identifier) ([]LocalDescriptor, error) {
	if localDescriptorsIdentifier == 0 {
		// There are no local descriptors.
		return nil, nil
	}

	localDescriptorsNode, err := file.GetBlockBTreeNode(localDescriptorsIdentifier)

	if err != nil {
		return nil, eris.Wrap(err, "failed to get local descriptors node")
	}

	// TODO - Merge signature, level, entry count etc into one ReadAt
	signature := make([]byte, 1)

	if _, err = file.Reader.ReadAt(signature, localDescriptorsNode.FileOffset); err != nil {
		return nil, eris.Wrap(err, "failed to read local descriptors signature")
	} else if signature[0] != 2 {
		return nil, ErrLocalDescriptorsSignatureInvalid
	}

	localDescriptorsLevel := make([]byte, 1)

	if _, err := file.Reader.ReadAt(localDescriptorsLevel, localDescriptorsNode.FileOffset+1); err != nil {
		return nil, eris.Wrap(err, "failed to read local descriptors level")
	}

	localDescriptorsEntryCount := make([]byte, 2)

	if _, err := file.Reader.ReadAt(localDescriptorsEntryCount, localDescriptorsNode.FileOffset+2); err != nil {
		return nil, eris.Wrap(err, "failed to get local descriptors entry count")
	}

	entryCount := binary.LittleEndian.Uint16(localDescriptorsEntryCount)

	var localDescriptorsEntriesOffset int64

	switch file.FormatType {
	case FormatTypeANSI:
		localDescriptorsEntriesOffset = localDescriptorsNode.FileOffset + 4
	default:
		localDescriptorsEntriesOffset = localDescriptorsNode.FileOffset + 8
	}

	// Branch node (SIBLOCK) - contains SIENTRY entries pointing to SLBLOCKs.
	// References [MS-PST] 2.2.2.8.3.3.2 SIBLOCKs.
	if localDescriptorsLevel[0] > 0 {
		var siEntrySize uint8

		switch file.FormatType {
		case FormatTypeANSI:
			siEntrySize = 8 // nid (4 bytes) + bid (4 bytes)
		default:
			siEntrySize = 16 // nid (8 bytes) + bid (8 bytes)
		}

		siEntries := make([]byte, entryCount*uint16(siEntrySize))

		if _, err := file.Reader.ReadAt(siEntries, localDescriptorsEntriesOffset); err != nil {
			return nil, eris.Wrap(err, "failed to read SIBLOCK entries")
		}

		var allLocalDescriptors []LocalDescriptor

		for i := 0; i < int(entryCount); i++ {
			siEntry := siEntries[i*int(siEntrySize) : (i+1)*int(siEntrySize)]

			// Get the BID of the child SLBLOCK.
			var childBID Identifier

			switch file.FormatType {
			case FormatTypeANSI:
				childBID = Identifier(binary.LittleEndian.Uint32(siEntry[4:8]))
			default:
				childBID = Identifier(binary.LittleEndian.Uint64(siEntry[8:16]))
			}

			// Recursively get local descriptors from the child block.
			childLocalDescriptors, err := file.GetLocalDescriptorsFromIdentifier(childBID)

			if err != nil {
				return nil, eris.Wrapf(err, "failed to get local descriptors from child block (BID: %d)", childBID)
			}

			allLocalDescriptors = append(allLocalDescriptors, childLocalDescriptors...)
		}

		return allLocalDescriptors, nil
	}

	// Leaf node (SLBLOCK) - contains SLENTRY entries.
	// References [MS-PST] 2.2.2.8.3.3.1 SLBLOCKs.
	var localDescriptorEntrySize uint8

	switch file.FormatType {
	case FormatTypeANSI:
		localDescriptorEntrySize = 12
	default:
		localDescriptorEntrySize = 24
	}

	localDescriptorsEntries := make([]byte, entryCount*uint16(localDescriptorEntrySize))

	if _, err := file.Reader.ReadAt(localDescriptorsEntries, localDescriptorsEntriesOffset); err != nil {
		return nil, eris.Wrap(err, "failed to read local descriptors entries")
	}

	localDescriptors := make([]LocalDescriptor, entryCount)

	for i := 0; i < int(entryCount); i++ {
		localDescriptorEntry := localDescriptorsEntries[i*int(localDescriptorEntrySize) : (i+1)*int(localDescriptorEntrySize)]

		localDescriptors[i] = NewLocalDescriptor(localDescriptorEntry, file.FormatType)
	}

	return localDescriptors, nil
}

// FindLocalDescriptor returns the local descriptor with the specified identifier or an error if not found.
func FindLocalDescriptor(identifier Identifier, localDescriptors []LocalDescriptor) (LocalDescriptor, error) {
	for _, localDescriptor := range localDescriptors {
		if localDescriptor.Identifier == identifier {
			return localDescriptor, nil
		}
	}

	return LocalDescriptor{}, ErrLocalDescriptorNotFound
}

// FindLocalDescriptorGlobally searches all node b-tree leaf nodes for a local descriptor with the given identifier.
// This is an expensive operation and should only be used in RecoveryMode as a fallback.
func (file *File) FindLocalDescriptorGlobally(identifier Identifier) (LocalDescriptor, error) {
	// Initialize diagnostics if needed
	if file.Diagnostics == nil {
		file.Diagnostics = &RecoveryDiagnostics{
			NeighborSearchRadiusTried:  make(map[int]int),
			NeighborSearchHitDistances: make(map[int]int),
		}
	}
	file.Diagnostics.GlobalSearchAttempts++

	var found LocalDescriptor
	foundAny := false

	file.NodeBTree.Scan(func(node BTreeNode) bool {
		if node.LocalDescriptorsIdentifier == 0 {
			return true
		}

		localDescriptors, err := file.GetLocalDescriptors(node)
		if err != nil {
			// Ignore errors while scanning — continue searching
			return true
		}

		for _, ld := range localDescriptors {
			if ld.Identifier == identifier {
				found = ld
				foundAny = true
				return false // stop scanning
			}
		}

		return true
	})

	if !foundAny {
		return LocalDescriptor{}, ErrLocalDescriptorNotFound
	}

	file.Diagnostics.GlobalSearchHits++
	return found, nil
}

// FindLocalDescriptorNearby searches for a local descriptor with the given identifier
// in nodes near the supplied origin node. It scans the Node B-tree in ascending order
// and inspects a radius of nodes before and after the origin. This is an expensive
// recovery-only search and should be used sparingly.
func (file *File) FindLocalDescriptorNearby(identifier Identifier, origin Identifier, radius int) (LocalDescriptor, error) {
	if radius <= 0 {
		radius = 3
	}

	// Initialize diagnostics maps if needed
	if file.Diagnostics == nil {
		file.Diagnostics = &RecoveryDiagnostics{
			NeighborSearchRadiusTried:  make(map[int]int),
			NeighborSearchHitDistances: make(map[int]int),
		}
	}

	file.Diagnostics.NeighborSearchAttempts++
	file.Diagnostics.NeighborSearchRadiusTried[radius]++

	var prevBuffer []BTreeNode
	nextToCollect := -1
	nextDistance := 0
	found := false
	var foundLD LocalDescriptor

	file.NodeBTree.Scan(func(node BTreeNode) bool {
		// Maintain sliding buffer of previous nodes
		if len(prevBuffer) >= radius {
			prevBuffer = prevBuffer[1:]
		}
		prevBuffer = append(prevBuffer, node)

		if nextToCollect >= 0 {
			// We are collecting nodes after origin
			if node.LocalDescriptorsIdentifier != 0 {
				lds, err := file.GetLocalDescriptors(node)
				if err == nil {
					for _, ld := range lds {
						if ld.Identifier == identifier {
							foundLD = ld
							found = true
							// record successor hit
							distance := nextDistance
							file.Diagnostics.NeighborSearchHits++
							file.Diagnostics.NeighborSearchHitDistances[distance]++
							file.Diagnostics.NeighborSuccessorHits++
							return false
						}
					}
				}
			}

			nextToCollect--
			nextDistance++
			if nextToCollect < 0 {
				return false
			}

			return true
		}

		// Haven't found origin yet; check if this node is origin
		if node.Identifier == origin {
			// Check previous nodes (closest first)
			for i := len(prevBuffer) - 1; i >= 0; i-- {
				n := prevBuffer[i]
				if n.LocalDescriptorsIdentifier == 0 {
					continue
				}

				lds, err := file.GetLocalDescriptors(n)
				if err != nil {
					continue
				}

				for _, ld := range lds {
					if ld.Identifier == identifier {
						foundLD = ld
						found = true
						// distance from origin: last element of prevBuffer is origin
						distance := len(prevBuffer) - 1 - i
						file.Diagnostics.NeighborSearchHits++
						file.Diagnostics.NeighborSearchHitDistances[distance]++
						file.Diagnostics.NeighborPredecessorHits++
						return false
					}
				}
			}

			// Collect next `radius` nodes
			nextToCollect = radius
			nextDistance = 1
			return true
		}

		return true
	})

	if !found {
		return LocalDescriptor{}, ErrLocalDescriptorNotFound
	}

	return foundLD, nil
}

// FindLocalDescriptorInBlocks scans all Block B-tree leaf node payloads looking for LocalDescriptor
// entries whose identifier matches the supplied identifier. If found, it returns the LocalDescriptor
// constructed from the payload and validates via the DataIdentifier that it points to a block node.
// This is an aggressive recovery heuristic and is only used in RecoveryMode.
func (file *File) FindLocalDescriptorInBlocks(identifier Identifier) (LocalDescriptor, error) {
	// Initialize diagnostics if needed
	if file.Diagnostics == nil {
		file.Diagnostics = &RecoveryDiagnostics{
			NeighborSearchRadiusTried:  make(map[int]int),
			NeighborSearchHitDistances: make(map[int]int),
		}
	}
	file.Diagnostics.BlockScanAttempts++

	var found LocalDescriptor
	foundAny := false

	maxUnalignedChecksPerBlock := 16
	if file.RecoveryOptions != nil && file.RecoveryOptions.MaxUnalignedChecksPerBlock > 0 {
		maxUnalignedChecksPerBlock = file.RecoveryOptions.MaxUnalignedChecksPerBlock
	}

	file.BlockBTree.Scan(func(node BTreeNode) bool {
		// Only leaf nodes contain payloads
		if node.NodeLevel != 0 || node.Size == 0 {
			return true
		}

		file.Diagnostics.BlockNodesScanned++

		buf := make([]byte, node.Size)
		if _, err := file.Reader.ReadAt(buf, node.FileOffset); err != nil {
			// Can't read this block — continue
			return true
		}

		// Two-pass strategy:
		// 1) fast aligned scan based on format (4- or 8-byte alignments)
		// 2) limited unaligned fallback (byte-wise) with a cap to avoid CPU blowup

		// Helper to validate and potentially return a candidate LocalDescriptor
		validateCandidate := func(ld LocalDescriptor) bool {
			// First ensure the DataIdentifier exists in the block b-tree
			if _, err := file.GetBlockBTreeNode(ld.DataIdentifier); err != nil {
				return false
			}

			// Try to fully validate by building a Heap-on-Node from the candidate
			if _, err := file.GetHeapOnNodeFromLocalDescriptor(ld); err == nil {
				file.Diagnostics.BlockScanHits++
				file.Diagnostics.BlockScanValidatedHits++
				found = ld
				foundAny = true
				return true
			}

			// If full validation failed but the DataIdentifier exists, treat as a weak hit
			file.Diagnostics.BlockScanHits++
			found = ld
			foundAny = true
			return true
		}

		// ANSI-format scan (12-byte SLENTRY)
		if file.FormatType == FormatTypeANSI {
			entrySize := 12
			// Aligned pass: step by 4 bytes
			for i := 0; i+entrySize <= len(buf); i += 4 {
				candID := Identifier(binary.LittleEndian.Uint32(buf[i : i+4]))
				if candID != identifier {
					continue
				}

				ld := LocalDescriptor{
					Identifier:                 candID,
					DataIdentifier:             Identifier(binary.LittleEndian.Uint32(buf[i+4 : i+8])),
					LocalDescriptorsIdentifier: Identifier(binary.LittleEndian.Uint32(buf[i+8 : i+12])),
				}

				if validateCandidate(ld) {
					return false
				}
			}

			// Limited unaligned fallback — avoid scanning every byte for large blocks
			if file.RecoveryOptions == nil || file.RecoveryOptions.EnableUnalignedBlockScan {
				checked := 0
				for i := 0; i+entrySize <= len(buf) && checked < maxUnalignedChecksPerBlock; i++ {
					candID := Identifier(binary.LittleEndian.Uint32(buf[i : i+4]))
					if candID != identifier {
						continue
					}

					checked++
					file.Diagnostics.BlockScanCandidatesChecked++
					ld := LocalDescriptor{
						Identifier:                 candID,
						DataIdentifier:             Identifier(binary.LittleEndian.Uint32(buf[i+4 : i+8])),
						LocalDescriptorsIdentifier: Identifier(binary.LittleEndian.Uint32(buf[i+8 : i+12])),
					}

					if validateCandidate(ld) {
						return false
					}
				}
			}
		} else {
			// Unicode / non-ANSI scan (24-byte SLENTRY), align to 8 bytes for speed
			entrySize := 24
			sz := int(GetIdentifierSize(file.FormatType))
			for i := 0; i+entrySize <= len(buf); i += 8 {
				candID := GetIdentifierFromBytes(buf[i:i+sz], file.FormatType)
				if candID != identifier {
					continue
				}

				dataID := GetIdentifierFromBytes(buf[i+sz:i+sz+sz], file.FormatType)
				localDescID := GetIdentifierFromBytes(buf[i+sz+sz:i+sz+sz+sz], file.FormatType)

				ld := LocalDescriptor{
					Identifier:                 candID,
					DataIdentifier:             dataID,
					LocalDescriptorsIdentifier: localDescID,
				}

				if validateCandidate(ld) {
					return false
				}
			}

			// Limited unaligned fallback for Unicode entries
			if file.RecoveryOptions == nil || file.RecoveryOptions.EnableUnalignedBlockScan {
				checked := 0
				for i := 0; i+entrySize <= len(buf) && checked < maxUnalignedChecksPerBlock; i++ {
					candID := GetIdentifierFromBytes(buf[i:i+sz], file.FormatType)
					if candID != identifier {
						continue
					}

					checked++
					file.Diagnostics.BlockScanCandidatesChecked++
					dataID := GetIdentifierFromBytes(buf[i+sz:i+sz+sz], file.FormatType)
					localDescID := GetIdentifierFromBytes(buf[i+sz+sz:i+sz+sz+sz], file.FormatType)

					ld := LocalDescriptor{
						Identifier:                 candID,
						DataIdentifier:             dataID,
						LocalDescriptorsIdentifier: localDescID,
					}

					if validateCandidate(ld) {
						return false
					}
				}
			}
		}

		return true
	})

	if !foundAny {
		return LocalDescriptor{}, ErrLocalDescriptorNotFound
	}

	return found, nil
}
