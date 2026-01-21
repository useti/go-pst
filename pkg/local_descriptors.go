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
