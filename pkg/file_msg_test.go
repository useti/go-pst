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
	"os"
	"path/filepath"
	"testing"

	pst "github.com/useti/go-pst/v6/pkg"
	"github.com/useti/go-pst/v6/pkg/properties"
)

func TestMSGFiles(t *testing.T) {
	msgDir := "../data/msg"
	files, err := os.ReadDir(msgDir)
	if err != nil {
		t.Fatalf("Failed to read msg directory: %v", err)
	}

	for _, file := range files {
		if filepath.Ext(file.Name()) == ".msg" {
			t.Run(file.Name(), func(t *testing.T) {
				testMSGFile(t, filepath.Join(msgDir, file.Name()))
			})
		}
	}
}

func testMSGFile(t *testing.T, filePath string) {
	reader, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("Failed to open MSG file %s: %v", filePath, err)
	}
	defer reader.Close()

	pstFile, err := pst.New(reader)
	if err != nil {
		t.Fatalf("Failed to parse MSG file %s: %v", filePath, err)
	}
	defer pstFile.Cleanup()

	if pstFile.ContentType != pst.ContentTypeMSG {
		t.Errorf("Expected ContentTypeMSG, got %v", pstFile.ContentType)
	}

	message := pstFile.GetRootMessage()
	if message == nil {
		t.Error("GetRootMessage returned nil")
		return
	}

	// Check that message has properties
	if message.PropertyContext == nil {
		t.Error("Message PropertyContext is nil")
		return
	}

	if len(message.PropertyContext.Properties) == 0 {
		t.Error("Message has no properties")
	}

	if message.Properties == nil {
		t.Error("Message properties were not populated")
	}

	// Allow for different message types (Message, Contact, Task, Appointment, etc.)
	// Just verify that some typed property was populated
	switch message.Properties.(type) {
	case *properties.Message, *properties.Contact, *properties.Task,
		*properties.Appointment, *properties.Journal, *properties.Note,
		*properties.AddressBook, *properties.RSS, *properties.Sharing,
		*properties.SMS, *properties.Spam, *properties.Voicemail, *properties.ExtractedEntity:
		// Expected types, all good
	default:
		t.Errorf("Message properties type unexpected: %T", message.Properties)
	}
}

func TestMSGRootMessagePropertySelection(t *testing.T) {
	filePath := "../data/msg/ManyFields2.msg"
	reader, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("Failed to open MSG file %s: %v", filePath, err)
	}
	defer reader.Close()

	pstFile, err := pst.New(reader)
	if err != nil {
		t.Fatalf("Failed to parse MSG file %s: %v", filePath, err)
	}
	defer pstFile.Cleanup()

	message := pstFile.GetRootMessage()
	if message == nil {
		t.Fatal("GetRootMessage returned nil")
	}

	subject := message.Properties.(*properties.Message).GetSubject()
	if subject == "" {
		t.Fatalf("Expected root message subject, got empty")
	}
}
