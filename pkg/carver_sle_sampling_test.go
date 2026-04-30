package pst

import (
	"encoding/binary"
	"testing"
)

func TestAggressiveSLEntryCarver_SamplingApplied(t *testing.T) {
	// Create a block containing many ANSI SLENTRY-like triples
	entries := make([]LocalDescriptor, 0)
	for i := 1; i <= 100; i++ {
		entries = append(entries, LocalDescriptor{Identifier: Identifier(1000 + i), DataIdentifier: Identifier(2000 + i), LocalDescriptorsIdentifier: 0})
	}
	// Build a synthetic block payload containing raw triples (no SLBLOCK header necessary for this test)
	buf := make([]byte, 12*len(entries))
	o := 0
	for _, e := range entries {
		binary.LittleEndian.PutUint32(buf[o:o+4], uint32(e.Identifier))
		binary.LittleEndian.PutUint32(buf[o+4:o+8], uint32(e.DataIdentifier))
		binary.LittleEndian.PutUint32(buf[o+8:o+12], uint32(e.LocalDescriptorsIdentifier))
		o += 12
	}

	blockStore := newMockBTreeStore()
	blockStore.Load(BTreeNode{Identifier: 42, FileOffset: int64(100), NodeLevel: 0, Size: uint16(len(buf))})

	fileData := make([]byte, 200)
	copy(fileData[100:], buf)

	file := &File{
		Reader:     newMockReader(fileData),
		FormatType: FormatTypeANSI,
		BlockBTree: blockStore,
		RecoveryOptions: &RecoveryOptions{
			EnableSLENTRYCarver:                     true,
			MaxSLENTRYCandidatesPerBlock:            0, // unlimited per block
			MaxSLENTRYCandidatesToSamplePerRun:      5, // sample to 5
			EnableAggressiveCarverRelaxedValidation: true,
			MaxSLENTRYValidationPerRun:              2, // validate up to 2 of the sampled candidates to estimate hit rate
		},

		Diagnostics: &RecoveryDiagnostics{},
	}

	sRecovered, err := file.AggressiveCarveForSLEntries()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sRecovered) != 5 {
		t.Fatalf("expected 5 sampled candidates, got %d", len(sRecovered))
	}

	diags := file.GetRecoveryDiagnostics()
	if diags.CarverSLEntryUniqueCandidates == 0 {
		t.Fatalf("expected unique candidates > 0")
	}
	if diags.CarverSLEntrySampled != 5 {
		t.Fatalf("expected sampled == 5, got %d", diags.CarverSLEntrySampled)
	}
	// If validation runs, CarverSLEntryValidated should be <= sampled
	if diags.CarverSLEntryValidated < 0 || diags.CarverSLEntryValidated > diags.CarverSLEntrySampled {
		t.Fatalf("invalid CarverSLEntryValidated: %d", diags.CarverSLEntryValidated)
	}
}
