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
	"io"

	_ "github.com/emersion/go-message/charset"
	"github.com/rotisserie/eris"
)

// RecoveryOptions holds opt-in controls for recovery heuristics
// to manage cost and false-positive risk.
type RecoveryOptions struct {
	EnableUnalignedBlockScan    bool     // whether to run the unaligned fallback pass
	MaxUnalignedChecksPerBlock  int      // cap on unaligned candidate checks per block
	EnableAggressiveCarver      bool     // whether to run the signature-based carver
	MaxCarverBlocksToScan       int      // cap on number of blocks to scan (0 = unlimited)
	MaxCarverCandidatesPerBlock int      // cap of candidates per block to consider
	CarverSignatures            [][]byte // list of byte sequences to search for
	// EnableSLENTRYCarver enables scanning for raw SLENTRY-like structures (opt-in)
	EnableSLENTRYCarver          bool // whether to run the SLENTRY-pattern carver
	MaxSLENTRYCandidatesPerBlock int  // cap of SLENTRY candidates per block to consider
	// EnableAggressiveCarverRelaxedValidation, when true, allows the carver to accept
	// candidates without successfully building a Heap-on-Node. This is useful for
	// experimental runs against heavily damaged PSTs and should remain opt-in.
	EnableAggressiveCarverRelaxedValidation bool
	// MaxSLENTRYCandidatesToSamplePerRun caps the number of SLENTRY candidates to return
	// from a single run of the SLENTRY carver. If 0, no sampling is applied (return all).
	MaxSLENTRYCandidatesToSamplePerRun int
	// MaxSLENTRYValidationPerRun caps how many sampled SLENTRY candidates are strictly validated
	// (using GetBlockBTreeNode + GetHeapOnNodeFromLocalDescriptor). Default 0 (disabled).
	MaxSLENTRYValidationPerRun int
}

// File represents a PST file.
type File struct {
	Reader         Reader
	FormatType     FormatType
	EncryptionType EncryptionType
	NodeBTree      BTreeStore
	BlockBTree     BTreeStore
	NameToIDMap    *NameToIDMap
	// RecoveryMode enables lenient parsing to recover data from corrupted PST files.
	// When enabled, certain validation errors (like invalid table types) are skipped.
	RecoveryMode bool
	// RecoveryOptions contains opt-in controls for expensive or risky heuristics.
	RecoveryOptions *RecoveryOptions
	// Diagnostics holds runtime recovery metrics to help tune heuristics.
	Diagnostics *RecoveryDiagnostics
}

// Reader defines the file reader used by go-pst to support asynchronous I/O.
// Non-linux systems will fall back to DefaultReader.
// See AsyncReader.
type Reader interface {
	ReadAtAsync(outputBuffer []byte, offset uint64, callback func(err error)) (uint64, error)
	io.ReaderAt // Blocking call.
}

// DefaultReader implements Reader using io.ReaderAt.
type DefaultReader struct {
	reader io.ReaderAt
}

func NewDefaultReader(reader io.ReaderAt) *DefaultReader {
	return &DefaultReader{
		reader: reader,
	}
}

// New is a constructor for creating PST files.
// See also NewAsync.
func New(reader io.ReaderAt) (*File, error) {
	return newFromReaderWithOptions(NewDefaultReader(reader), NewBTreeStoreInMemory(), NewBTreeStoreInMemory(), false)
}

// NewWithRecoveryMode is a constructor for creating PST files with recovery mode enabled.
// Recovery mode allows parsing corrupted PST files by skipping certain validation errors.
// This is useful for recovering data from damaged files.
func NewWithRecoveryMode(reader io.ReaderAt) (*File, error) {
	return newFromReaderWithOptions(NewDefaultReader(reader), NewBTreeStoreInMemory(), NewBTreeStoreInMemory(), true)
}

// NewFromReaderWithBTrees is a constructor for creating PST files from a reader using the specified b-tree stores.
// Initialization of the b-tree stores will be skipped respectively if not empty.
func NewFromReaderWithBTrees(reader Reader, nodeBTree BTreeStore, blockBTree BTreeStore) (*File, error) {
	return newFromReaderWithOptions(reader, nodeBTree, blockBTree, false)
}

// NewFromReaderWithBTreesRecoveryMode is a constructor for creating PST files from a reader using the specified b-tree stores with recovery mode enabled.
func NewFromReaderWithBTreesRecoveryMode(reader Reader, nodeBTree BTreeStore, blockBTree BTreeStore) (*File, error) {
	return newFromReaderWithOptions(reader, nodeBTree, blockBTree, true)
}

// newFromReaderWithOptions is the internal constructor that handles all initialization options.
func newFromReaderWithOptions(reader Reader, nodeBTree BTreeStore, blockBTree BTreeStore, recoveryMode bool) (*File, error) {
	pstFile := &File{
		Reader: &DefaultReader{
			reader: reader,
		},
		NodeBTree:    nodeBTree,
		BlockBTree:   blockBTree,
		RecoveryMode: recoveryMode,
		// Default recovery options; aggressive carver is opt-in
		RecoveryOptions: &RecoveryOptions{
			EnableUnalignedBlockScan:                true,
			MaxUnalignedChecksPerBlock:              16,
			EnableAggressiveCarver:                  false,
			MaxCarverBlocksToScan:                   512,
			MaxCarverCandidatesPerBlock:             8,
			CarverSignatures:                        [][]byte{[]byte("IPM.Note")},
			EnableAggressiveCarverRelaxedValidation: false, EnableSLENTRYCarver: false,
			MaxSLENTRYCandidatesPerBlock:       8,
			MaxSLENTRYCandidatesToSamplePerRun: 256,
			MaxSLENTRYValidationPerRun:         0},
	}

	isValidSignature, err := pstFile.IsValidSignature()

	if err != nil {
		return nil, err
	} else if !isValidSignature {
		return nil, ErrFileSignatureInvalid
	}

	formatType, err := pstFile.GetFormatType()

	if err != nil {
		return nil, err
	}

	pstFile.FormatType = formatType

	if _, err := pstFile.GetContentType(); err != nil {
		return nil, err
	}

	encryptionType, err := pstFile.GetEncryptionType()

	if err != nil {
		return nil, err
	}

	pstFile.EncryptionType = encryptionType

	if pstFile.NodeBTree.Len() == 0 {
		nodeBTreeOffset, err := pstFile.GetNodeBTreeOffset()

		if err != nil {
			return nil, err
		}

		pstFile.WalkAndCreateBTree(nodeBTreeOffset, BTreeTypeNode, pstFile.NodeBTree)

		if err != nil {
			return nil, err
		}
	}

	if pstFile.BlockBTree.Len() == 0 {
		blockBTreeOffset, err := pstFile.GetBlockBTreeOffset()

		if err != nil {
			return nil, err
		}

		pstFile.WalkAndCreateBTree(blockBTreeOffset, BTreeTypeBlock, pstFile.BlockBTree)

		if err != nil {
			return nil, err
		}
	}

	nameToIDMap, err := pstFile.GetNameToIDMap()

	if err != nil {
		if pstFile.RecoveryMode {
			// In recovery mode, continue with an empty Name-To-ID Map
			pstFile.NameToIDMap = &NameToIDMap{
				PropertySets: []string{},
				NameToID:     make(map[int]int),
				IDToName:     make(map[int]int),
				StringToID:   make(map[string]int),
				IDToString:   make(map[int]string),
			}
		} else {
			return nil, err
		}
	} else {
		pstFile.NameToIDMap = nameToIDMap
	}

	// Initialize diagnostics for recovery heuristics tuning
	pstFile.Diagnostics = &RecoveryDiagnostics{
		NeighborSearchRadiusTried:     make(map[int]int),
		NeighborSearchHitDistances:    make(map[int]int),
		CarverSLEntryUniqueCandidates: 0,
		CarverSLEntrySampled:          0,
		CarverSLEntryValidated:        0,
	}
	return pstFile, nil
}

// IsValidSignature returns true is the file matches the PFF format signature.
// References "File Header".
func (file *File) IsValidSignature() (bool, error) {
	signature := make([]byte, 4)

	if _, err := file.Reader.ReadAt(signature, 0); err != nil {
		return false, eris.Wrap(err, "failed to read signature")
	}

	return bytes.Equal(signature, []byte("!BDN")), nil
}

// ContentType represents a PST, OST or PAB file.
type ContentType uint8

// Constants defining the content types.
// References "Content Types".
const (
	ContentTypePST ContentType = iota
	ContentTypeOST
	ContentTypePAB
)

// GetContentType returns if the file is a PST, OST or PAB file.
// References "File Header", "Content Types".
func (file *File) GetContentType() (ContentType, error) {
	contentType := make([]byte, 2)

	if _, err := file.Reader.ReadAt(contentType, 8); err != nil {
		return 0, eris.Wrap(err, "failed to get content type")
	}

	if bytes.Equal(contentType, []byte("SM")) {
		return ContentTypePST, nil
	} else if bytes.Equal(contentType, []byte("SO")) {
		return ContentTypeOST, nil
	} else if bytes.Equal(contentType, []byte("AB")) {
		return ContentTypePAB, nil
	} else {
		return 0, ErrContentTypeUnsupported
	}
}

// FormatType represents a Unicode or ANSI format type.
type FormatType uint8

// Constants defining the format types.
// References "Format Types".
const (
	FormatTypeANSI FormatType = iota
	FormatTypeUnicode
	FormatTypeUnicode4k
)

// GetFormatType returns the format type.
// References "File Header", "Format Types".
func (file *File) GetFormatType() (FormatType, error) {
	formatType := make([]byte, 2)

	if _, err := file.Reader.ReadAt(formatType, 10); err != nil {
		return 0, eris.Wrap(err, "failed to read format type")
	}

	switch binary.LittleEndian.Uint16(formatType) {
	case 14:
		return FormatTypeANSI, nil
	case 15:
		return FormatTypeANSI, nil
	case 21:
		return FormatTypeUnicode, nil
	case 23:
		return FormatTypeUnicode, nil
	case 36:
		return FormatTypeUnicode4k, nil
	default:
		return 0, ErrFormatTypeUnsupported
	}
}

// RecoveryDiagnostics contains counters and histograms used to tune recovery heuristics
// such as neighbor-local-descriptor inference and global descriptor scans.
type RecoveryDiagnostics struct {
	NeighborSearchAttempts     int         // total neighbor searches attempted
	NeighborSearchHits         int         // total neighbor searches that found a descriptor
	NeighborSearchRadiusTried  map[int]int // radius => attempts
	NeighborSearchHitDistances map[int]int // distance => hits
	NeighborPredecessorHits    int         // hits found in predecessor nodes
	NeighborSuccessorHits      int         // hits found in successor nodes
	GlobalSearchAttempts       int         // global descriptor scan attempts
	GlobalSearchHits           int         // successful global descriptor finds
	// Block scanning diagnostics
	BlockScanAttempts          int // total block scan attempts
	BlockNodesScanned          int // total block nodes scanned
	BlockScanCandidatesChecked int // unaligned/extra candidates checked per block
	BlockScanHits              int // successful finds in block payloads
	BlockScanValidatedHits     int // validated (Heap-on-Node) hits
	// Carver diagnostics
	CarverAttempts         int // number of times carver started
	CarverCandidatesFound  int // number of signature occurrences found
	CarverRecovered        int // number of recovered descriptors from carver
	CarverRecoveredRelaxed int // number of recovered descriptors found via relaxed validation
	// SLENTRY-specific diagnostics
	CarverSLEntryCandidatesFound  int // number of SLENTRY candidate occurrences found
	CarverSLEntryRecovered        int // number of SLENTRY-based recovered descriptors
	CarverSLEntryRecoveredRelaxed int // number recovered by SLENTRY carver under relaxed validation
	CarverSLEntryUniqueCandidates int // number of unique SLENTRY candidates (deduped)
	CarverSLEntrySampled          int // number of SLENTRY candidates returned after sampling
	CarverSLEntryValidated        int // number of sampled SLENTRY candidates validated successfully (Heap-on-Node built)

}

// ResetRecoveryDiagnostics resets diagnostics counters and histograms.
func (file *File) ResetRecoveryDiagnostics() {
	if file.Diagnostics == nil {
		file.Diagnostics = &RecoveryDiagnostics{
			NeighborSearchRadiusTried:  make(map[int]int),
			NeighborSearchHitDistances: make(map[int]int),
		}
		return
	}

	file.Diagnostics.NeighborSearchAttempts = 0
	file.Diagnostics.NeighborSearchHits = 0
	file.Diagnostics.NeighborPredecessorHits = 0
	file.Diagnostics.NeighborSuccessorHits = 0
	file.Diagnostics.GlobalSearchAttempts = 0
	file.Diagnostics.GlobalSearchHits = 0
	file.Diagnostics.BlockScanAttempts = 0
	file.Diagnostics.BlockNodesScanned = 0
	file.Diagnostics.BlockScanCandidatesChecked = 0
	file.Diagnostics.BlockScanHits = 0
	file.Diagnostics.BlockScanValidatedHits = 0
	file.Diagnostics.CarverRecoveredRelaxed = 0
	file.Diagnostics.CarverSLEntryCandidatesFound = 0
	file.Diagnostics.CarverSLEntryRecovered = 0
	file.Diagnostics.CarverSLEntryRecoveredRelaxed = 0
	file.Diagnostics.CarverSLEntryUniqueCandidates = 0
	file.Diagnostics.CarverSLEntrySampled = 0
	file.Diagnostics.CarverSLEntryValidated = 0

	for k := range file.Diagnostics.NeighborSearchRadiusTried {
		delete(file.Diagnostics.NeighborSearchRadiusTried, k)
	}
	for k := range file.Diagnostics.NeighborSearchHitDistances {
		delete(file.Diagnostics.NeighborSearchHitDistances, k)
	}
}

// GetRecoveryDiagnostics returns a copy of the current diagnostics snapshot.
func (file *File) GetRecoveryDiagnostics() RecoveryDiagnostics {
	if file.Diagnostics == nil {
		return RecoveryDiagnostics{
			NeighborSearchRadiusTried:  make(map[int]int),
			NeighborSearchHitDistances: make(map[int]int),
		}
	}
	return *file.Diagnostics
}

type EncryptionType uint8

// Constants defining the encryption types.
// References "Encryption Types".
const (
	EncryptionTypeNone    EncryptionType = 0
	EncryptionTypePermute EncryptionType = 1
	EncryptionTypeCyclic  EncryptionType = 2
)

// GetEncryptionType returns the encryption type.
// References "The 64-bit header data", "The 32-bit header data", "Encryption Types".
func (file *File) GetEncryptionType() (EncryptionType, error) {
	outputBuffer := make([]byte, 1)
	var offset int64

	switch file.FormatType {
	case FormatTypeANSI:
		offset = 461
	default:
		offset = 513
	}

	if _, err := file.Reader.ReadAt(outputBuffer, offset); err != nil {
		return 0, eris.Wrap(err, "failed to read encryption type")
	}

	switch outputBuffer[0] {
	case 0:
		return EncryptionTypeNone, nil
	case 1:
		return EncryptionTypePermute, nil
	case 2:
		return EncryptionTypeCyclic, nil
	default:
		return 0, ErrEncryptionTypeUnsupported
	}
}

// Cleanup clears the node and block b-trees.
func (file *File) Cleanup() {
	file.NodeBTree.Clear()
	file.BlockBTree.Clear()
}

// ReadAt calls the underlying io.ReaderAt.
func (defaultReader *DefaultReader) ReadAt(outputBuffer []byte, offset int64) (int, error) {
	return defaultReader.reader.ReadAt(outputBuffer, offset)
}

// ReadAtAsync is a fall-back which calls io.ReaderAt.
// See AsyncReader for Linux io_uring support.
func (defaultReader *DefaultReader) ReadAtAsync(outputBuffer []byte, offset uint64, callback func(err error)) (uint64, error) {
	_, err := defaultReader.reader.ReadAt(outputBuffer, int64(offset))

	callback(err)

	return 0, err
}
