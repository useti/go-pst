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

package pst_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"path/filepath"

	"github.com/rotisserie/eris"
	pst "github.com/useti/go-pst/v6/pkg"
	"github.com/useti/go-pst/v6/pkg/properties"
	"golang.org/x/text/encoding"

	charsets "github.com/emersion/go-message/charset"
)

func TestExample(t *testing.T) {
	pst.ExtendCharsets(func(name string, enc encoding.Encoding) {
		charsets.RegisterEncoding(name, enc)
	})

	startTime := time.Now()

	fmt.Println("Initializing...")

	reader, err := os.Open("../data/enron.pst")

	if err != nil {
		panic(fmt.Sprintf("Failed to open PST file: %+v\n", err))
	}

	pstFile, err := pst.New(reader)

	if err != nil {
		panic(fmt.Sprintf("Failed to open PST file: %+v\n", err))
	}

	defer func() {
		pstFile.Cleanup()

		if errClosing := reader.Close(); errClosing != nil {
			panic(fmt.Sprintf("Failed to close PST file: %+v\n", err))
		}
	}()

	// Create attachments directory
	if _, err := os.Stat("attachments"); err != nil {
		if err := os.Mkdir("attachments", 0755); err != nil {
			panic(fmt.Sprintf("Failed to create attachments directory: %+v", err))
		}
	}

	// Walk through folders.
	if err := pstFile.WalkFolders(func(folder *pst.Folder) error {
		fmt.Printf("Walking folder: %s\n", folder.Name)

		messageIterator, err := folder.GetMessageIterator()

		if eris.Is(err, pst.ErrMessagesNotFound) {
			// Folder has no messages.
			return nil
		} else if err != nil {
			return err
		}

		// Iterate through messages.
		for messageIterator.Next() {
			message := messageIterator.Value()

			switch messageProperties := message.Properties.(type) {
			case *properties.Appointment:
				//fmt.Printf("Appointment: %s\n", messageProperties.String())
			case *properties.Contact:
				//fmt.Printf("Contact: %s\n", messageProperties.String())
			case *properties.Task:
				//fmt.Printf("Task: %s\n", messageProperties.String())
			case *properties.RSS:
				//fmt.Printf("RSS: %s\n", messageProperties.String())
			case *properties.AddressBook:
				//fmt.Printf("Address book: %s\n", messageProperties.String())
			case *properties.Message:
				fmt.Printf("Subject: %s\n", messageProperties.GetSubject())
			case *properties.Note:
				//fmt.Printf("Note: %s\n", messageProperties.String())
			default:
				fmt.Printf("Unknown message type\n")
			}

			attachmentIterator, err := message.GetAttachmentIterator()

			if eris.Is(err, pst.ErrAttachmentsNotFound) {
				// This message has no attachments.
				continue
			} else if err != nil {
				return err
			}

			// Iterate through attachments.
			for attachmentIterator.Next() {
				attachment := attachmentIterator.Value()

				var attachmentOutputPath string

				if attachment.GetAttachLongFilename() != "" {
					attachmentOutputPath = fmt.Sprintf("attachments/%d-%s", attachment.Identifier, attachment.GetAttachLongFilename())
				} else {
					attachmentOutputPath = fmt.Sprintf("attachments/UNKNOWN_%d", attachment.Identifier)
				}

				attachmentOutput, err := os.Create(attachmentOutputPath)

				if err != nil {
					return err
				}

				if _, err := attachment.WriteTo(attachmentOutput); err != nil {
					return err
				}

				if err := attachmentOutput.Close(); err != nil {
					return err
				}
			}

			if attachmentIterator.Err() != nil {
				return attachmentIterator.Err()
			}
		}

		return messageIterator.Err()
	}); err != nil {
		panic(fmt.Sprintf("Failed to walk folders: %+v\n", err))
	}

	fmt.Printf("Time: %s\n", time.Since(startTime).String())
}

// TestRecoveryMode demonstrates how to use recovery mode to work with
// corrupted PST files and recover orphan (deleted) messages.
func TestRecoveryMode(t *testing.T) {
	pst.ExtendCharsets(func(name string, enc encoding.Encoding) {
		charsets.RegisterEncoding(name, enc)
	})

	startTime := time.Now()

	fmt.Println("Initializing recovery mode...")

	// reader, err := os.Open("../data/enron.pst")
	reader, err := os.Open("/volumes/Data/0020_WORK/0020_DATA/extra/extra_pst/file7_bad.pst")
	// reader, err := os.Open("/volumes/Data/0020_WORK/0020_DATA/extra/extra_pst/file3.pst")

	if err != nil {
		t.Fatalf("Failed to open PST file: %+v\n", err)
	}

	// Use NewWithRecoveryMode for corrupted files
	// This enables lenient parsing that skips certain validation errors
	pstFile, err := pst.NewWithRecoveryMode(reader)
	if err != nil {
		t.Fatalf("Failed to open PST file in recovery mode: %+v\n", err)
	}

	defer func() {
		pstFile.Cleanup()
		if errClosing := reader.Close(); errClosing != nil {
			t.Fatalf("Failed to close PST file: %+v\n", errClosing)
		}
	}()

	// First, walk through folders normally (recovery mode handles corrupted table types)
	regularMessageCount := 0
	if err := pstFile.WalkFolders(func(folder *pst.Folder) error {
		fmt.Printf("Recovery mode - Walking folder: %s\n", folder.Name)

		messageIterator, err := folder.GetMessageIterator()
		if eris.Is(err, pst.ErrMessagesNotFound) {
			return nil
		} else if err != nil {
			// In recovery mode, log error but continue
			fmt.Printf("Warning: Could not get messages from folder %s: %v\n", folder.Name, err)
			return nil
		}

		for messageIterator.Next() {
			regularMessageCount++
			message := messageIterator.Value()

			if msgProps, ok := message.Properties.(*properties.Message); ok {
				subject := msgProps.GetSubject()
				if subject != "" {
					fmt.Printf("Regular message: %s\n", subject)
				}
			}
		}

		if messageIterator.Err() != nil {
			fmt.Printf("Warning: Error iterating messages in folder %s: %v\n", folder.Name, messageIterator.Err())
		}

		return nil
	}); err != nil {
		fmt.Printf("Warning: Error walking folders: %v\n", err)
	}

	fmt.Printf("Found %d regular messages in folder hierarchy\n", regularMessageCount)

	// Now attempt to recover orphan (deleted/unreferenced) messages
	fmt.Println("\nSearching for orphan (deleted) messages...")

	orphanIterator, err := pstFile.GetOrphanMessageIterator()
	if err != nil {
		t.Fatalf("Failed to get orphan message iterator: %+v\n", err)
	}

	fmt.Printf("Total nodes in B-tree: %d\n", orphanIterator.TotalNodes)
	fmt.Printf("Message nodes found: %d\n", orphanIterator.Size())

	orphanCount := 0
	for orphanIterator.Next() {
		orphanCount++
		message := orphanIterator.Value()

		subject := ""
		if msgProps, ok := message.Properties.(*properties.Message); ok {
			subject = msgProps.GetSubject()
		}

		if subject != "" {
			fmt.Printf("Recovered orphan message [ID: %d]: %s\n", message.Identifier, subject)
		} else {
			fmt.Printf("Recovered orphan message [ID: %d]: (no subject)\n", message.Identifier)
		}
	}

	if orphanIterator.Err() != nil {
		fmt.Printf("Warning: Error during orphan iteration: %v\n", orphanIterator.Err())
	}

	fmt.Printf("\nOrphan iterator statistics:\n")
	fmt.Printf("  Skipped (referenced): %d\n", orphanIterator.SkippedReferenced)
	fmt.Printf("  Skipped (errors): %d\n", orphanIterator.SkippedErrors)
	if len(orphanIterator.ErrorMessages) > 0 {
		fmt.Printf("  First few errors:\n")
		for i, errMsg := range orphanIterator.ErrorMessages {
			if i >= 5 {
				fmt.Printf("    ... and %d more errors\n", len(orphanIterator.ErrorMessages)-5)
				break
			}
			fmt.Printf("    %s\n", errMsg)
		}
	}

	// Try deep recovery - scan ALL nodes for any recoverable items
	// This includes messages, calendar items, contacts, tasks, notes, etc.
	fmt.Println("\nAttempting deep recovery (scanning all nodes for all item types)...")
	recoverIter, err := pstFile.GetRecoverableMessagesIterator()
	if err != nil {
		t.Fatalf("Failed to get recoverable messages iterator: %+v\n", err)
	}

	fmt.Printf("Total leaf nodes to try: %d\n", recoverIter.Size())

	deepRecoveredCount := 0
	for recoverIter.Next() {
		deepRecoveredCount++
		// Use RecoveredItem for easier access to properties
		item := recoverIter.RecoveredItem()

		displayInfo := item.GetDisplayInfo()
		if displayInfo != "" {
			fmt.Printf("Recovered [%s, NodeType: %d]: %s\n", item.ItemType, item.NodeType, displayInfo)
		} else if deepRecoveredCount <= 20 { // Limit output for items without names
			fmt.Printf("Recovered [%s, ID: %d, NodeType: %d]: (no display info)\n", item.ItemType, item.Identifier, item.NodeType)
		}
	}

	fmt.Printf("\nDeep recovery statistics:\n")
	fmt.Printf("  Nodes tried: %d\n", recoverIter.TriedNodes)
	fmt.Printf("  Successful: %d\n", recoverIter.SuccessNodes)
	fmt.Printf("  Failed: %d\n", recoverIter.FailedNodes)

	fmt.Printf("\nRecovered items by type:\n")
	for itemType, count := range recoverIter.TypeCounts {
		fmt.Printf("  %s: %d\n", itemType, count)
	}

	if len(recoverIter.ErrorCategoryCounts) > 0 {
		fmt.Printf("\nError categories:\n")
		for category, count := range recoverIter.ErrorCategoryCounts {
			fmt.Printf("  %s: %d\n", category, count)
		}
	}

	if len(recoverIter.ErrorMessages) > 0 {
		fmt.Printf("\nSample errors:\n")
		for i, errMsg := range recoverIter.ErrorMessages {
			if i >= 10 {
				fmt.Printf("    ... and %d more errors\n", len(recoverIter.ErrorMessages)-10)
				break
			}
			fmt.Printf("    %s\n", errMsg)
		}
	}

	// Print recovery diagnostics (neighbor/global descriptor search stats)
	if diags := pstFile.GetRecoveryDiagnostics(); true {
		fmt.Printf("\nRecovery diagnostics:\n")
		fmt.Printf("  Neighbor search attempts: %d\n", diags.NeighborSearchAttempts)
		fmt.Printf("  Neighbor search hits: %d\n", diags.NeighborSearchHits)
		fmt.Printf("  Predecessor hits: %d\n", diags.NeighborPredecessorHits)
		fmt.Printf("  Successor hits: %d\n", diags.NeighborSuccessorHits)
		if len(diags.NeighborSearchRadiusTried) > 0 {
			fmt.Printf("  Radius attempts:\n")
			for r, c := range diags.NeighborSearchRadiusTried {
				fmt.Printf("    radius %d: %d attempts\n", r, c)
			}
		}
		if len(diags.NeighborSearchHitDistances) > 0 {
			fmt.Printf("  Hit distances:\n")
			for d, c := range diags.NeighborSearchHitDistances {
				fmt.Printf("    distance %d: %d hits\n", d, c)
			}
		}
		fmt.Printf("  Global descriptor search attempts: %d, hits: %d\n", diags.GlobalSearchAttempts, diags.GlobalSearchHits)
		fmt.Printf("  Block scan attempts: %d, nodes scanned: %d, hits: %d, validated hits: %d, candidates checked: %d\n", diags.BlockScanAttempts, diags.BlockNodesScanned, diags.BlockScanHits, diags.BlockScanValidatedHits, diags.BlockScanCandidatesChecked)
	}

	// Raw property context recovery - the most aggressive approach
	fmt.Println("\nRaw property context scan (all nodes)...")
	rawIter, err := pstFile.GetRawPropertyContextIterator()
	if err != nil {
		t.Fatalf("Failed to get raw property context iterator: %+v\n", err)
	}

	fmt.Printf("Total nodes to scan: %d\n", rawIter.Size())

	rawRecoveredCount := 0
	for rawIter.Next() {
		rawRecoveredCount++
		pc := rawIter.Value()
		node := rawIter.CurrentNode()

		// Try to get display name or subject
		displayName := ""
		subject := ""

		// Property ID 12289 = PidTagDisplayName
		if prop, err := pc.GetPropertyByID(12289); err == nil {
			if reader, err := pc.GetPropertyReader(prop.ID, nil); err == nil {
				if name, err := reader.GetString(); err == nil {
					displayName = name
				}
			}
		}

		// Property ID 55 = PidTagSubject
		if prop, err := pc.GetPropertyByID(55); err == nil {
			if reader, err := pc.GetPropertyReader(prop.ID, nil); err == nil {
				if subj, err := reader.GetString(); err == nil {
					subject = subj
				}
			}
		}

		if displayName != "" || subject != "" {
			fmt.Printf("Raw PC [ID: %d, Type: %d]: DisplayName=%q, Subject=%q\n",
				node.Identifier, node.Identifier.GetType(), displayName, subject)
		}

		// Show all property IDs for debugging
		if rawRecoveredCount <= 3 {
			fmt.Printf("  Properties in this context (%d total):\n", len(pc.Properties))
			for i, prop := range pc.Properties {
				if i >= 10 {
					fmt.Printf("    ... and %d more properties\n", len(pc.Properties)-10)
					break
				}
				fmt.Printf("    ID: %d, Type: %d\n", prop.ID, prop.Type)
			}
		}
	}

	fmt.Printf("\nRaw recovery statistics:\n")
	fmt.Printf("  Nodes tried: %d\n", rawIter.TriedNodes)
	fmt.Printf("  Property contexts found: %d\n", rawIter.SuccessNodes)
	fmt.Printf("  Failed: %d\n", rawIter.FailedNodes)

	// Check Block B-tree size for comparison
	blockCount := 0
	pstFile.BlockBTree.Scan(func(node pst.BTreeNode) bool {
		if node.NodeLevel == 0 {
			blockCount++
		}
		return true
	})
	fmt.Printf("\nBlock B-tree leaf nodes: %d\n", blockCount)

	fmt.Printf("\nRecovery summary:\n")
	fmt.Printf("  Regular messages: %d\n", regularMessageCount)
	fmt.Printf("  Orphan messages recovered: %d\n", orphanCount)
	fmt.Printf("  Deep recovery items: %d\n", deepRecoveredCount)
	fmt.Printf("  Raw property contexts: %d\n", rawRecoveredCount)

	if len(recoverIter.TypeCounts) > 0 {
		fmt.Printf("\nRecovered items breakdown:\n")
		for itemType, count := range recoverIter.TypeCounts {
			fmt.Printf("  - %s: %d\n", itemType, count)
		}
	}

	fmt.Printf("\nNote: If no messages found, the PST file may have been emptied/purged.\n")
	fmt.Printf("The B-tree structure shows %d nodes total.\n", orphanIterator.TotalNodes)
	fmt.Printf("Recovery mode attempts to parse all node types including:\n")
	fmt.Printf("  - Messages (IPM.Note)\n")
	fmt.Printf("  - Calendar/Appointments (IPM.Appointment, IPM.Schedule.Meeting)\n")
	fmt.Printf("  - Contacts (IPM.Contact)\n")
	fmt.Printf("  - Tasks (IPM.Task)\n")
	fmt.Printf("  - Journal entries (IPM.Activity)\n")
	fmt.Printf("  - Notes (IPM.StickyNote)\n")
	fmt.Printf("  - RSS items (IPM.Post.Rss)\n")
	fmt.Printf("  - Distribution lists (IPM.DistList)\n")
	fmt.Printf("  - Attachments and folder metadata\n")
	fmt.Printf("Time: %s\n", time.Since(startTime).String())
}

// TestRecoveryMode_AggressiveCarver demonstrates running the aggressive
// carver against one or more real PST files to collect diagnostics.
// The test attempts a set of paths and will run subtests per file.
func TestRecoveryMode_AggressiveCarver(t *testing.T) {
	paths := []string{"/volumes/Data/0020_WORK/0020_DATA/extra/extra_pst/file7_bad.pst", "/Volumes/Data/0020_WORK/0020_DATA/extra/extra_pst/file11.pst"}
	if env := os.Getenv("PST_INTEGRATION_PST"); env != "" {
		paths = append([]string{env}, paths...)
	}

	foundAny := false
	for _, p := range paths {
		p := p // capture
		t.Run(filepath.Base(p), func(t *testing.T) {
			reader, err := os.Open(p)
			if err != nil {
				t.Logf("skipping %s: %v", p, err)
				return
			}
			defer reader.Close()
			foundAny = true

			pstFile, err := pst.NewWithRecoveryMode(reader)
			if err != nil {
				t.Fatalf("Failed to open PST file in recovery mode (%s): %+v", p, err)
			}
			defer pstFile.Cleanup()

			if pstFile.RecoveryOptions == nil {
				pstFile.RecoveryOptions = &pst.RecoveryOptions{}
			}
			pstFile.RecoveryOptions.EnableAggressiveCarver = true
			pstFile.RecoveryOptions.MaxCarverCandidatesPerBlock = 16
			// If running a relaxed trial, include broader signatures likely to indicate messages
			// or properties. Keep the default set otherwise.
			if len(pstFile.RecoveryOptions.CarverSignatures) == 0 {
				pstFile.RecoveryOptions.CarverSignatures = [][]byte{[]byte("IPM.Note")}
			}

			// Run a relaxed validation trial to be more permissive on badly corrupted files.
			t.Run("relaxed", func(t *testing.T) {
				if pstFile.RecoveryOptions == nil {
					pstFile.RecoveryOptions = &pst.RecoveryOptions{}
				}
				pstFile.RecoveryOptions.EnableAggressiveCarver = true
				pstFile.RecoveryOptions.EnableAggressiveCarverRelaxedValidation = true
				pstFile.RecoveryOptions.MaxCarverCandidatesPerBlock = 64
				pstFile.RecoveryOptions.MaxCarverBlocksToScan = 2048 // Enable SLENTRY-pattern carver for relaxed runs (diagnostic/opt-in)
				pstFile.RecoveryOptions.EnableSLENTRYCarver = true
				pstFile.RecoveryOptions.MaxSLENTRYCandidatesPerBlock = 64
				pstFile.RecoveryOptions.MaxSLENTRYCandidatesToSamplePerRun = 5 // Limit sampled SLENTRY candidates for diagnostic runs
				pstFile.RecoveryOptions.CarverSignatures = append(pstFile.RecoveryOptions.CarverSignatures,
					[]byte("Subject"),
					[]byte("Subject:"),
					[]byte("From:"),
					[]byte("To:"),
					[]byte("Received"),
					[]byte("Message-ID"),
					[]byte("IPM.Appointment"),
					[]byte("IPM.Contact"),
					[]byte("PidTagSubject"),
					[]byte("X-From"),
				)

				// Run with timeouts: signature matches
				var matches []pst.CarverMatch
				var mErr error
				doneM := make(chan struct{})
				go func() {
					matches, mErr = pstFile.FindSignatureMatches(pstFile.RecoveryOptions.CarverSignatures, 32)
					close(doneM)
				}()
				select {
				case <-doneM:
				case <-time.After(20 * time.Second):
					t.Logf("FindSignatureMatches (relaxed) timed out after 20s for %s", p)
					return
				}
				if mErr != nil && mErr != pst.ErrLocalDescriptorNotFound {
					t.Fatalf("FindSignatureMatches failed: %v", mErr)
				}
				t.Logf("(relaxed) signature matches found: %d", len(matches))
				for i, mm := range matches {
					if i >= 16 {
						break
					}
					t.Logf("  Match %d: block=%d offset=%d sig=%q context=%q", i, mm.Candidate.BlockIdentifier, mm.Candidate.Offset, mm.Candidate.Signature, mm.Context)
				}

				// If no signature matches found, try carving printable ASCII strings, then UTF-16LE strings.
				if len(matches) == 0 {
					var ascii []struct {
						Str   string
						Block pst.BTreeNode
						Off   int
					}
					var aErr error
					doneA := make(chan struct{})
					go func() {
						ascii, aErr = pstFile.CarvePrintableASCII(5, 32)
						close(doneA)
					}()
					select {
					case <-doneA:
					case <-time.After(20 * time.Second):
						t.Logf("CarvePrintableASCII timed out after 20s for %s", p)
						return
					}
					if aErr != nil && aErr != pst.ErrLocalDescriptorNotFound {
						t.Fatalf("CarvePrintableASCII failed: %v", aErr)
					}
					t.Logf("(relaxed) ASCII strings carved: %d", len(ascii))
					for i, s := range ascii {
						if i >= 16 {
							break
						}
						t.Logf("  ASCII %d: block=%d off=%d str=%q", i, s.Block.Identifier, s.Off, s.Str)
					}

					// Try UTF-16LE carving as well
					var utf16 []struct {
						Str   string
						Block pst.BTreeNode
						Off   int
					}
					var uErr error
					doneU := make(chan struct{})
					go func() {
						utf16, uErr = pstFile.CarvePrintableUTF16(4, 32)
						close(doneU)
					}()
					select {
					case <-doneU:
					case <-time.After(20 * time.Second):
						t.Logf("CarvePrintableUTF16 timed out after 20s for %s", p)
						return
					}
					if uErr != nil && uErr != pst.ErrLocalDescriptorNotFound {
						t.Fatalf("CarvePrintableUTF16 failed: %v", uErr)
					}
					t.Logf("(relaxed) UTF-16 strings carved: %d", len(utf16))
					for i, s := range utf16 {
						if i >= 16 {
							break
						}
						t.Logf("  UTF16 %d: block=%d off=%d str=%q", i, s.Block.Identifier, s.Off, s.Str)
					}
				}

				// Try raw SLENTRY-pattern carver for diagnostics (relaxed, opt-in)
				var sRecovered []pst.LocalDescriptor
				var sErr error
				doneS := make(chan struct{})
				go func() {
					sRecovered, sErr = pstFile.AggressiveCarveForSLEntries()
					close(doneS)
				}()
				select {
				case <-doneS:
				case <-time.After(20 * time.Second):
					t.Logf("AggressiveCarveForSLEntries (relaxed) timed out after 20s for %s", p)
					return
				}
				if sErr != nil && sErr != pst.ErrLocalDescriptorNotFound {
					t.Fatalf("AggressiveCarveForSLEntries failed: %v", sErr)
				}
				t.Logf("(relaxed) SLENTRY carver recovered: %d descriptors", len(sRecovered))
				for _, rd := range sRecovered {
					t.Logf("  SLENTRY recovered: ID=%d DataID=%d", rd.Identifier, rd.DataIdentifier)
				}
				// Log sampling diagnostics explicitly when sampling is enabled
				diags := pstFile.GetRecoveryDiagnostics()
				t.Logf("  (relaxed-sle) SLENTRY unique candidates=%d sampled=%d validated=%d", diags.CarverSLEntryUniqueCandidates, diags.CarverSLEntrySampled, diags.CarverSLEntryValidated)
				var recovered []pst.LocalDescriptor
				var rerr error
				done2 := make(chan struct{})
				go func() {
					recovered, rerr = pstFile.AggressiveCarveAndRecover()
					close(done2)
				}()
				select {
				case <-done2:
				case <-time.After(30 * time.Second):
					t.Logf("AggressiveCarveAndRecover (relaxed) timed out after 30s for %s", p)
					return
				}
				if rerr != nil && rerr != pst.ErrLocalDescriptorNotFound {
					t.Fatalf("AggressiveCarveAndRecover failed: %v", rerr)
				}
				t.Logf("(relaxed) Carver recovered: %d descriptors", len(recovered))
				for _, rd := range recovered {
					t.Logf("  Recovered (relaxed): ID=%d DataID=%d", rd.Identifier, rd.DataIdentifier)
				}
			})

			// Run AggressiveCarveForSignatures with a timeout to avoid scanning very large files indefinitely.
			var candidates []pst.CarverCandidate
			var cerr error
			done := make(chan struct{})
			go func() {
				candidates, cerr = pstFile.AggressiveCarveForSignatures()
				close(done)
			}()
			select {
			case <-done:
				// completed
			case <-time.After(30 * time.Second):
				t.Logf("AggressiveCarveForSignatures timed out after 30s for %s", p)
				return
			}
			if cerr != nil && cerr != pst.ErrLocalDescriptorNotFound {
				t.Fatalf("AggressiveCarveForSignatures failed: %v", cerr)
			}
			t.Logf("Carver candidates found: %d", len(candidates))

			// Run AggressiveCarveAndRecover with a timeout as well.
			var recovered []pst.LocalDescriptor
			var rerr error
			done2 := make(chan struct{})
			go func() {
				recovered, rerr = pstFile.AggressiveCarveAndRecover()
				close(done2)
			}()
			select {
			case <-done2:
				// completed
			case <-time.After(30 * time.Second):
				t.Logf("AggressiveCarveAndRecover timed out after 30s for %s", p)
				return
			}
			if rerr != nil && rerr != pst.ErrLocalDescriptorNotFound {
				t.Fatalf("AggressiveCarveAndRecover failed: %v", rerr)
			}
			t.Logf("Carver recovered: %d descriptors", len(recovered))
			for i, rd := range recovered {
				if i >= 3 {
					break // Limit output to first 3
				}
				t.Logf("  Recovered: ID=%d DataID=%d", rd.Identifier, rd.DataIdentifier)
				// Try to extract actual message data from recovered LocalDescriptor
				msg, err := pstFile.GetMessageFromLocalDescriptor(rd)
				if err == nil && msg != nil {
					// Extract common properties
					var subject, from string
					if reader, err := msg.PropertyContext.GetPropertyReader(55, nil); err == nil {
						subject, _ = reader.GetString()
					}
					if reader, err := msg.PropertyContext.GetPropertyReader(4097, nil); err == nil {
						from, _ = reader.GetString()
					}
					if subject != "" || from != "" {
						t.Logf("    [DATA] Subject=%q From=%q", subject, from)
					}
				}
			}

			diags := pstFile.GetRecoveryDiagnostics()
			t.Logf("Recovery diagnostics - CarverAttempts=%d CandidatesFound=%d CarverRecovered=%d", diags.CarverAttempts, diags.CarverCandidatesFound, diags.CarverRecovered)

			// Basic sanity assertions
			if diags.CarverAttempts == 0 {
				t.Fatalf("expected aggressive carver to attempt at least once, got 0")
			}
			if diags.CarverCandidatesFound > 0 {
				if diags.CarverRecovered > diags.CarverCandidatesFound {
					t.Fatalf("invalid diagnostics: CarverRecovered (%d) > CarverCandidatesFound (%d)", diags.CarverRecovered, diags.CarverCandidatesFound)
				}
			}
		})
	}

	if !foundAny {
		t.Skip("no PST files available to run aggressive carver integration")
	}
}
