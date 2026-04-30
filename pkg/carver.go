package pst

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// CarverCandidate represents a signature occurrence in a block.
type CarverCandidate struct {
	BlockIdentifier Identifier
	BlockNode       BTreeNode
	Offset          int
	Signature       []byte
}

// AggressiveCarveForSignatures scans block payloads for configured signatures and returns candidates.
func (file *File) AggressiveCarveForSignatures() ([]CarverCandidate, error) {
	if file.RecoveryOptions == nil || !file.RecoveryOptions.EnableAggressiveCarver {
		return nil, nil
	}

	// Initialize diagnostics if needed
	if file.Diagnostics == nil {
		file.Diagnostics = &RecoveryDiagnostics{
			NeighborSearchRadiusTried:  make(map[int]int),
			NeighborSearchHitDistances: make(map[int]int),
		}
	}

	file.Diagnostics.CarverAttempts++

	var candidates []CarverCandidate
	var blocksScanned int

	file.BlockBTree.Scan(func(node BTreeNode) bool {
		if node.NodeLevel != 0 || node.Size == 0 {
			return true
		}

		buf := make([]byte, node.Size)
		if _, err := file.Reader.ReadAt(buf, node.FileOffset); err != nil {
			return true
		}

		perBlockFound := 0

		for _, sig := range file.RecoveryOptions.CarverSignatures {
			start := 0
			for {
				o := bytes.Index(buf[start:], sig)
				if o < 0 {
					break
				}
				off := start + o
				candidates = append(candidates, CarverCandidate{BlockIdentifier: node.Identifier, BlockNode: node, Offset: off, Signature: sig})
				perBlockFound++
				file.Diagnostics.CarverCandidatesFound++
				if file.RecoveryOptions.MaxCarverCandidatesPerBlock > 0 && perBlockFound >= file.RecoveryOptions.MaxCarverCandidatesPerBlock {
					break
				}
				start = off + 1
			}
			if file.RecoveryOptions.MaxCarverCandidatesPerBlock > 0 && perBlockFound >= file.RecoveryOptions.MaxCarverCandidatesPerBlock {
				break
			}
		}

		// Update blocks scanned and respect global limit per run.
		blocksScanned++
		if file.RecoveryOptions.MaxCarverBlocksToScan > 0 && blocksScanned >= file.RecoveryOptions.MaxCarverBlocksToScan {
			// stop scanning further blocks
			return false
		}

		return true
	})

	if len(candidates) == 0 {
		return nil, ErrLocalDescriptorNotFound
	}

	return candidates, nil
}

// AggressiveCarveAndRecover scans for signatures and attempts to recover LocalDescriptors near each occurrence.
// Returns recovered LocalDescriptors (may be empty).
func (file *File) AggressiveCarveAndRecover() ([]LocalDescriptor, error) {
	candidates, err := file.AggressiveCarveForSignatures()
	if err != nil && err != ErrLocalDescriptorNotFound {
		return nil, err
	}

	var recovered []LocalDescriptor

	for _, cand := range candidates {
		// Read block payload
		buf := make([]byte, cand.BlockNode.Size)
		if _, err := file.Reader.ReadAt(buf, cand.BlockNode.FileOffset); err != nil {
			continue
		}

		// Search around the signature occurrence for potential SLENTRYs (ANSI & Unicode)
		windowStart := cand.Offset - 64
		if windowStart < 0 {
			windowStart = 0
		}
		windowEnd := cand.Offset + len(cand.Signature) + 64
		if windowEnd > len(buf) {
			windowEnd = len(buf)
		}

		// Try ANSI candidates: 12-byte entries
		for i := windowStart; i+12 <= windowEnd; i++ {
			identifier := Identifier(binary.LittleEndian.Uint32(buf[i : i+4]))
			ld := LocalDescriptor{
				Identifier:                 identifier,
				DataIdentifier:             Identifier(binary.LittleEndian.Uint32(buf[i+4 : i+8])),
				LocalDescriptorsIdentifier: Identifier(binary.LittleEndian.Uint32(buf[i+8 : i+12])),
			}

			// Strict validation: DataIdentifier must exist and Heap-on-Node built
			if !file.RecoveryOptions.EnableAggressiveCarverRelaxedValidation {
				if _, err := file.GetBlockBTreeNode(ld.DataIdentifier); err == nil {
					if _, err := file.GetHeapOnNodeFromLocalDescriptor(ld); err == nil {
						recovered = append(recovered, ld)
						file.Diagnostics.CarverRecovered++
						break // don't add multiple descriptors for the same candidate
					}
				}
			} else {
				// Relaxed validation: accept any plausible looking descriptor (non-zero identifier)
				if ld.Identifier != 0 {
					recovered = append(recovered, ld)
					file.Diagnostics.CarverRecoveredRelaxed++
					break
				}
			}
		}

		// Try Unicode candidates: 24-byte entries (identifier size depends on format)
		if file.FormatType != FormatTypeANSI {
			sz := int(GetIdentifierSize(file.FormatType))
			for i := windowStart; i+sz*3 <= windowEnd; i++ {
				candID := GetIdentifierFromBytes(buf[i:i+sz], file.FormatType)
				dataID := GetIdentifierFromBytes(buf[i+sz:i+sz+sz], file.FormatType)
				localDescID := GetIdentifierFromBytes(buf[i+sz+sz:i+sz+sz+sz], file.FormatType)

				ld := LocalDescriptor{Identifier: candID, DataIdentifier: dataID, LocalDescriptorsIdentifier: localDescID}
				if !file.RecoveryOptions.EnableAggressiveCarverRelaxedValidation {
					if _, err := file.GetBlockBTreeNode(ld.DataIdentifier); err == nil {
						if _, err := file.GetHeapOnNodeFromLocalDescriptor(ld); err == nil {
							recovered = append(recovered, ld)
							file.Diagnostics.CarverRecovered++
							break
						}
					}
				} else {
					if ld.Identifier != 0 {
						recovered = append(recovered, ld)
						file.Diagnostics.CarverRecoveredRelaxed++
						break
					}
				}
			}
		}
	}

	// If nothing recovered from signature-proximal scanning and we're in a relaxed run,
	// optionally run a raw SLENTRY-pattern carver over blocks (opt-in). Promote any
	// validated SLENTRY candidates to strict recovered hits and count relaxed hits
	// for the rest so we surface useful diagnostics.
	if len(recovered) == 0 && file.RecoveryOptions != nil && file.RecoveryOptions.EnableAggressiveCarverRelaxedValidation && file.RecoveryOptions.EnableSLENTRYCarver {
		if sRecovered, serr := file.AggressiveCarveForSLEntries(); serr == nil {
			// For each sampled candidate, try to validate it (strict) when possible.
			validatedCount := 0
			for _, ld := range sRecovered {
				// Attempt strict validation if feasible
				if _, err := file.GetBlockBTreeNode(ld.DataIdentifier); err == nil {
					if _, err := file.GetHeapOnNodeFromLocalDescriptor(ld); err == nil {
						recovered = append(recovered, ld)
						file.Diagnostics.CarverRecovered++
						validatedCount++
						continue
					}
				}
				// If strict validation failed or not available, still include as relaxed recovered
				recovered = append(recovered, ld)
				file.Diagnostics.CarverRecoveredRelaxed++
			}
			// If any validated hits were found, also reflect that in the SLENTRY validation diagnostic
			if file.Diagnostics != nil {
				file.Diagnostics.CarverSLEntryValidated = validatedCount
			}
		}
	}

	// Update aggregate recovered count to reflect any recovered LocalDescriptors (signature-proximal or SLENTRY)
	if file.Diagnostics != nil {
		file.Diagnostics.CarverRecovered = len(recovered)
	}

	if len(recovered) == 0 {
		return nil, ErrLocalDescriptorNotFound
	}

	return recovered, nil
}

// CarverMatch represents a raw signature match along with a context snippet.
type CarverMatch struct {
	Candidate CarverCandidate
	Context   []byte // up to context bytes around match
}

// FindSignatureMatches scans block payloads for the given signatures and returns up to maxMatches matches.
// It's intended as a diagnostic helper to inspect where signatures occur and show surrounding bytes.
func (file *File) FindSignatureMatches(signatures [][]byte, maxMatches int) ([]CarverMatch, error) {
	if file.RecoveryOptions == nil || len(signatures) == 0 {
		return nil, nil
	}

	var matches []CarverMatch
	blocksScanned := 0

	file.BlockBTree.Scan(func(node BTreeNode) bool {
		if node.NodeLevel != 0 || node.Size == 0 {
			return true
		}

		// Respect global cap
		blocksScanned++
		if file.RecoveryOptions.MaxCarverBlocksToScan > 0 && blocksScanned > file.RecoveryOptions.MaxCarverBlocksToScan {
			return false
		}

		buf := make([]byte, node.Size)
		if _, err := file.Reader.ReadAt(buf, node.FileOffset); err != nil {
			return true
		}

		for _, sig := range signatures {
			start := 0
			for {
				o := bytes.Index(buf[start:], sig)
				if o < 0 {
					break
				}
				off := start + o
				// Build context (32 bytes before and after)
				cs := 32
				ctxStart := off - cs
				if ctxStart < 0 {
					ctxStart = 0
				}
				ctxEnd := off + len(sig) + cs
				if ctxEnd > len(buf) {
					ctxEnd = len(buf)
				}
				matches = append(matches, CarverMatch{Candidate: CarverCandidate{BlockIdentifier: node.Identifier, BlockNode: node, Offset: off, Signature: sig}, Context: append([]byte(nil), buf[ctxStart:ctxEnd]...)})
				if maxMatches > 0 && len(matches) >= maxMatches {
					return false
				}
				start = off + 1
			}
		}

		return true
	})

	if len(matches) == 0 {
		return nil, ErrLocalDescriptorNotFound
	}

	return matches, nil
}

// CarvePrintableASCII extracts printable ASCII strings of length >= minLen from block payloads and
// returns up to maxResults unique strings with their block and offset. It's diagnostic-only to see if
// there is readable text in blocks that might hint at where descriptors live.
func (file *File) CarvePrintableASCII(minLen int, maxResults int) ([]struct {
	Str   string
	Block BTreeNode
	Off   int
}, error) {
	type result struct {
		Str   string
		Block BTreeNode
		Off   int
	}

	if minLen < 3 {
		minLen = 3
	}

	var results []result
	seen := make(map[string]bool)
	blocksScanned := 0

	file.BlockBTree.Scan(func(node BTreeNode) bool {
		if node.NodeLevel != 0 || node.Size == 0 {
			return true
		}

		blocksScanned++
		if file.RecoveryOptions.MaxCarverBlocksToScan > 0 && blocksScanned > file.RecoveryOptions.MaxCarverBlocksToScan {
			return false
		}

		buf := make([]byte, node.Size)
		if _, err := file.Reader.ReadAt(buf, node.FileOffset); err != nil {
			return true
		}

		// Scan for printable ASCII runs
		runStart := -1
		for i := 0; i < len(buf); i++ {
			b := buf[i]
			if b >= 0x20 && b <= 0x7e {
				if runStart < 0 {
					runStart = i
				}
			} else {
				if runStart >= 0 {
					if i-runStart >= minLen {
						s := string(buf[runStart:i])
						if !seen[s] {
							seen[s] = true
							results = append(results, result{Str: s, Block: node, Off: runStart})
							if maxResults > 0 && len(results) >= maxResults {
								return false
							}
						}
					}
					runStart = -1
				}
			}
		}

		// Tail
		if runStart >= 0 && len(buf)-runStart >= minLen {
			s := string(buf[runStart:])
			if !seen[s] {
				seen[s] = true
				results = append(results, result{Str: s, Block: node, Off: runStart})
				if maxResults > 0 && len(results) >= maxResults {
					return false
				}
			}
		}

		return true
	})

	if len(results) == 0 {
		return nil, ErrLocalDescriptorNotFound
	}

	out := make([]struct {
		Str   string
		Block BTreeNode
		Off   int
	}, len(results))
	for i, r := range results {
		out[i] = r
	}

	return out, nil
}

// CarvePrintableUTF16 extracts printable UTF-16LE strings (null high bytes) of length >= minLen
// and returns up to maxResults unique strings with their block and offset.
func (file *File) CarvePrintableUTF16(minLen int, maxResults int) ([]struct {
	Str   string
	Block BTreeNode
	Off   int
}, error) {
	type result struct {
		Str   string
		Block BTreeNode
		Off   int
	}

	if minLen < 3 {
		minLen = 3
	}

	var results []result
	seen := make(map[string]bool)
	blocksScanned := 0

	file.BlockBTree.Scan(func(node BTreeNode) bool {
		if node.NodeLevel != 0 || node.Size == 0 {
			return true
		}

		blocksScanned++
		if file.RecoveryOptions.MaxCarverBlocksToScan > 0 && blocksScanned > file.RecoveryOptions.MaxCarverBlocksToScan {
			return false
		}

		buf := make([]byte, node.Size)
		if _, err := file.Reader.ReadAt(buf, node.FileOffset); err != nil {
			return true
		}

		// Scan for UTF-16LE printable runs (low byte printable, high byte == 0)
		runStart := -1
		for i := 0; i+1 < len(buf); i += 2 {
			low := buf[i]
			high := buf[i+1]
			if high == 0 && low >= 0x20 && low <= 0x7e {
				if runStart < 0 {
					runStart = i
				}
			} else {
				if runStart >= 0 {
					charCount := (i - runStart) / 2
					if charCount >= minLen {
						// build string
						b := make([]byte, charCount)
						for j := 0; j < charCount; j++ {
							b[j] = buf[runStart+2*j]
						}
						s := string(b)
						if !seen[s] {
							seen[s] = true
							results = append(results, result{Str: s, Block: node, Off: runStart})
							if maxResults > 0 && len(results) >= maxResults {
								return false
							}
						}
					}
					runStart = -1
				}
			}
		}

		// Tail
		if runStart >= 0 {
			charCount := (len(buf) - runStart) / 2
			if charCount >= minLen {
				b := make([]byte, charCount)
				for j := 0; j < charCount; j++ {
					b[j] = buf[runStart+2*j]
				}
				s := string(b)
				if !seen[s] {
					seen[s] = true
					results = append(results, result{Str: s, Block: node, Off: runStart})
					if maxResults > 0 && len(results) >= maxResults {
						return false
					}
				}
			}
		}

		return true
	})

	if len(results) == 0 {
		return nil, ErrLocalDescriptorNotFound
	}

	out := make([]struct {
		Str   string
		Block BTreeNode
		Off   int
	}, len(results))
	for i, r := range results {
		out[i] = r
	}

	return out, nil
}

// AggressiveCarveForSLEntries scans block payloads for raw SLENTRY-like structures (ANSI & Unicode) and
// returns recovered LocalDescriptors. This is an opt-in, relaxed carver intended for very damaged PSTs.
func (file *File) AggressiveCarveForSLEntries() ([]LocalDescriptor, error) {
	if file.RecoveryOptions == nil || !file.RecoveryOptions.EnableSLENTRYCarver {
		return nil, nil
	}

	// Initialize diagnostics if needed
	if file.Diagnostics == nil {
		file.Diagnostics = &RecoveryDiagnostics{
			NeighborSearchRadiusTried:  make(map[int]int),
			NeighborSearchHitDistances: make(map[int]int),
		}
	}

	file.Diagnostics.CarverAttempts++

	var recovered []LocalDescriptor
	blocksScanned := 0

	file.BlockBTree.Scan(func(node BTreeNode) bool {
		if node.NodeLevel != 0 || node.Size == 0 {
			return true
		}

		blocksScanned++
		if file.RecoveryOptions.MaxCarverBlocksToScan > 0 && blocksScanned > file.RecoveryOptions.MaxCarverBlocksToScan {
			return false
		}

		buf := make([]byte, node.Size)
		if _, err := file.Reader.ReadAt(buf, node.FileOffset); err != nil {
			return true
		}

		perBlockFound := 0

		// ANSI SLENTRY candidates: 12 bytes
		for i := 0; i+12 <= len(buf); i++ {
			identifier := Identifier(binary.LittleEndian.Uint32(buf[i : i+4]))
			dataID := Identifier(binary.LittleEndian.Uint32(buf[i+4 : i+8]))
			localDescID := Identifier(binary.LittleEndian.Uint32(buf[i+8 : i+12]))

			// Basic plausibility check: non-zero identifier
			if identifier == 0 {
				continue
			}

			file.Diagnostics.CarverSLEntryCandidatesFound++
			perBlockFound++

			ld := LocalDescriptor{Identifier: identifier, DataIdentifier: dataID, LocalDescriptorsIdentifier: localDescID}

			if !file.RecoveryOptions.EnableAggressiveCarverRelaxedValidation {
				// strict validation requires DataIdentifier to point to a block and Heap-on-Node to be buildable
				if _, err := file.GetBlockBTreeNode(ld.DataIdentifier); err == nil {
					if _, err := file.GetHeapOnNodeFromLocalDescriptor(ld); err == nil {
						recovered = append(recovered, ld)
						file.Diagnostics.CarverSLEntryRecovered++
					}
				}
			} else {
				// relaxed: accept plausible non-zero identifiers
				recovered = append(recovered, ld)
				file.Diagnostics.CarverSLEntryRecoveredRelaxed++
			}

			if file.RecoveryOptions.MaxSLENTRYCandidatesPerBlock > 0 && perBlockFound >= file.RecoveryOptions.MaxSLENTRYCandidatesPerBlock {
				break
			}
		}

		// Unicode SLENTRY candidates
		if file.FormatType != FormatTypeANSI {
			sz := int(GetIdentifierSize(file.FormatType))
			entrySz := sz * 3
			for i := 0; i+entrySz <= len(buf); i++ {
				candID := GetIdentifierFromBytes(buf[i:i+sz], file.FormatType)
				dataID := GetIdentifierFromBytes(buf[i+sz:i+sz+sz], file.FormatType)
				localDescID := GetIdentifierFromBytes(buf[i+sz+sz:i+sz+sz+sz], file.FormatType)

				if candID == 0 {
					continue
				}

				file.Diagnostics.CarverSLEntryCandidatesFound++
				perBlockFound++

				ld := LocalDescriptor{Identifier: candID, DataIdentifier: dataID, LocalDescriptorsIdentifier: localDescID}

				if !file.RecoveryOptions.EnableAggressiveCarverRelaxedValidation {
					if _, err := file.GetBlockBTreeNode(ld.DataIdentifier); err == nil {
						if _, err := file.GetHeapOnNodeFromLocalDescriptor(ld); err == nil {
							recovered = append(recovered, ld)
							file.Diagnostics.CarverSLEntryRecovered++
						}
					}
				} else {
					recovered = append(recovered, ld)
					file.Diagnostics.CarverSLEntryRecoveredRelaxed++
				}

				if file.RecoveryOptions.MaxSLENTRYCandidatesPerBlock > 0 && perBlockFound >= file.RecoveryOptions.MaxSLENTRYCandidatesPerBlock {
					break
				}
			}
		}

		return true
	})

	if len(recovered) == 0 {
		return nil, ErrLocalDescriptorNotFound
	}

	// Deduplicate recovered candidates (Identifier:DataIdentifier:LocalDescriptorsIdentifier)
	uniqueMap := make(map[string]LocalDescriptor)
	for _, ld := range recovered {
		k := fmt.Sprintf("%d:%d:%d", ld.Identifier, ld.DataIdentifier, ld.LocalDescriptorsIdentifier)
		if _, ok := uniqueMap[k]; !ok {
			uniqueMap[k] = ld
		}
	}

	unique := make([]LocalDescriptor, 0, len(uniqueMap))
	for _, v := range uniqueMap {
		unique = append(unique, v)
	}

	// Update unique candidates diagnostic
	file.Diagnostics.CarverSLEntryUniqueCandidates = len(unique)

	// Apply sampling cap if configured
	sampled := unique
	if file.RecoveryOptions != nil && file.RecoveryOptions.MaxSLENTRYCandidatesToSamplePerRun > 0 && len(unique) > file.RecoveryOptions.MaxSLENTRYCandidatesToSamplePerRun {
		cap := file.RecoveryOptions.MaxSLENTRYCandidatesToSamplePerRun
		// deterministic sampling (first N) for now; can be randomized later
		sampled = unique[:cap]
	}

	file.Diagnostics.CarverSLEntrySampled = len(sampled)

	// Optional: validate up to MaxSLENTRYValidationPerRun sampled candidates to estimate signal/noise
	if file.RecoveryOptions != nil && file.RecoveryOptions.MaxSLENTRYValidationPerRun > 0 {
		validated := 0
		maxVal := file.RecoveryOptions.MaxSLENTRYValidationPerRun
		for i, ld := range sampled {
			if i >= maxVal {
				break
			}
			if _, err := file.GetBlockBTreeNode(ld.DataIdentifier); err == nil {
				if _, err := file.GetHeapOnNodeFromLocalDescriptor(ld); err == nil {
					validated++
				}
			}
		}
		file.Diagnostics.CarverSLEntryValidated = validated
	}

	return sampled, nil
}
